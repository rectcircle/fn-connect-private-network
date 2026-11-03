package client

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"github.com/rectcircle/fn-connect-private-network/internal/version"
)

func TestUploadUsesSSEReceiverCountAndRequiresCompletion(t *testing.T) {
	for _, tc := range []struct {
		name, events string
		wantError    bool
	}{
		{"complete", "event: progress\ndata: {\"receivedBytes\":262144,\"totalBytes\":524288,\"elapsedMs\":1000}\n\nevent: complete\ndata: {\"receivedBytes\":524288,\"totalBytes\":1048576,\"elapsedMs\":2000}\n\n", false},
		{"idle-before-complete", "event: progress\ndata: {\"receivedBytes\":524288,\"totalBytes\":1048576,\"elapsedMs\":2000}\n\nevent: progress\ndata: {\"receivedBytes\":524288,\"totalBytes\":1048576,\"elapsedMs\":2000}\n\nevent: complete\ndata: {\"receivedBytes\":524288,\"totalBytes\":1048576,\"elapsedMs\":2000}\n\n", false},
		{"sampling-ended-without-confirmation", "event: progress\ndata: {\"receivedBytes\":524288,\"totalBytes\":1048576,\"elapsedMs\":9000}\n\n", true},
		{"interrupted", "event: progress\ndata: {\"receivedBytes\":262144,\"totalBytes\":524288,\"elapsedMs\":1000}\n\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				manager, p, n, _, _ := coordinationFixture(t)
				now := time.Now()
				n.status = model.ClientPrivilegedStatus{Active: true, Interface: "utun9", LastHandshake: &now}
				manager.setStatus(model.ClientDirect, "ipv6", "utun9", nil)
				measurement := &speedMeasurement{Manager: manager, status: SpeedTestStatus{Results: []SpeedTestResult{{Link: "ipv6"}}}, report: func(SpeedTestStatus) error { return nil }}
				h := &speedHTTP{base: "http://nas", client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					if _, err := io.CopyN(io.Discard, r.Body, 1048576); err != nil {
						return nil, err
					}
					return &http.Response{StatusCode: 200, Header: http.Header{"X-Fncpn-Version": {version.Current}, "Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(tc.events))}, nil
				})}}
				snap, _ := p.Snapshot()
				err := measurement.measureUpload(t.Context(), h, model.SpeedTestPath, 0, snap.Fingerprint())
				if (err != nil) != tc.wantError {
					t.Fatal(err)
				}
				if tc.name == "sampling-ended-without-confirmation" {
					if measurement.status.SampleElapsedMS != 9000 || measurement.status.Results[0].UploadMbps != nil || measurement.status.Stage == "upload-complete" {
						t.Fatal("sampling target was mistaken for completed upload", measurement.status)
					}
				}
				if !tc.wantError {
					got := measurement.status.Results[0].UploadMbps
					if got == nil || *got != 2.097152 || measurement.status.Mbps != *got || measurement.status.Stage != "upload-complete" {
						t.Fatal("upload counted socket writes instead of NAS reads", got)
					}
				}
			})
		})
	}
}
func TestSpeedTestDiscoveryAndCancellationRestoreIntent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		manager, _, n, _, _ := coordinationFixture(t)
		stop := runCoordinated(t, manager)
		defer stop()
		var final SpeedTestStatus
		if err := manager.RunSpeedTest(t.Context(), SpeedTestRequest{Link: "ipv6"}, func(s SpeedTestStatus) error { final = s; return nil }); err != nil {
			t.Fatal(err)
		}
		if final.Active || len(final.Results) != 1 || final.Results[0].Outcome != "unavailable" || final.Results[0].Reason != "no_server_ipv6" || final.Results[0].Error != "" || n.applies.Load() != 0 {
			t.Fatal(final)
		}
		entered := make(chan struct{}, 1)
		n.apply = func(ctx context.Context, _ model.ClientPlan) (model.ClientPrivilegedStatus, error) {
			entered <- struct{}{}
			<-ctx.Done()
			return model.ClientPrivilegedStatus{}, ctx.Err()
		}
		finished := make(chan error, 1)
		go func() {
			finished <- manager.RunSpeedTest(t.Context(), SpeedTestRequest{Link: "fn-connect"}, func(SpeedTestStatus) error { return nil })
		}()
		synctest.Wait()
		<-entered
		disconnected := make(chan error, 1)
		go func() { disconnected <- manager.Disconnect(t.Context()) }()
		time.Sleep(200 * time.Millisecond)
		synctest.Wait()
		if err := <-finished; err != nil {
			t.Fatal(err)
		}
		if err := <-disconnected; err != nil {
			t.Fatal(err)
		}
		cfg, _ := manager.store.Load()
		if cfg.AutoConnect {
			t.Fatal("disconnect intent was resurrected")
		}
	})
}

func TestUploadHeartbeatsDoNotBecomeZeroRateSamples(t *testing.T) {
	meter := uploadProgressMeter{}
	started := time.Now()
	rate, stage := meter.observe(model.SpeedTestStats{Bytes: 100000, TotalBytes: 200000, ElapsedMS: 1000}, started, false)
	if rate != 0.8 || stage != "upload" {
		t.Fatal(rate, stage)
	}
	// Repeated SSE frames (including an old server advancing only its timer) carry no new payload.
	rate, stage = meter.observe(model.SpeedTestStats{Bytes: 100000, TotalBytes: 200000, ElapsedMS: 1250}, started.Add(250*time.Millisecond), false)
	if rate != 0.8 || stage != "upload" {
		t.Fatal("heartbeat erased the measured rate", rate, stage)
	}
	rate, stage = meter.observe(model.SpeedTestStats{Bytes: 100000, TotalBytes: 200000, ElapsedMS: 1250}, started.Add(1500*time.Millisecond), false)
	if stage != "upload-waiting" {
		t.Fatal("stalled transfer was displayed as normal throughput", rate, stage)
	}
	_, stage = meter.observe(model.SpeedTestStats{Bytes: 100000, TotalBytes: 200000, ElapsedMS: 1250}, started.Add(1750*time.Millisecond), true)
	if stage != "upload-confirming" {
		t.Fatal("sender completion did not enter acknowledgment wait", stage)
	}
	rate, stage = meter.observe(model.SpeedTestStats{Bytes: 200000, TotalBytes: 300000, ElapsedMS: 2000}, started.Add(2*time.Second), true)
	if stage != "upload" || rate <= 0 {
		t.Fatal("receiver increments failed to resume throughput", rate, stage)
	}
}

func TestSpeedTestDoesNotRejectIPv6WhenInterfaceClassificationIsFalse(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, p, n, d, _ := coordinationFixture(t)
		snapshot, _ := p.Snapshot()
		snapshot.HasPublicIPv6 = false
		p.snapshot.Store(&snapshot)
		d.discover = func(context.Context) (Discovery, error) { return Discovery{IPv6: []string{"240e::1"}}, nil }
		m.ipv6RouteChecker = func(context.Context, netip.Addr, int) bool { return true }
		var entered atomic.Bool
		n.apply = func(ctx context.Context, _ model.ClientPlan) (model.ClientPrivilegedStatus, error) {
			if ctx.Value(forcedLinkKey{}) == "ipv6" {
				entered.Store(true)
			}
			return model.ClientPrivilegedStatus{}, model.NewError(model.ErrorUnavailable, "test handshake reached", true)
		}
		stop := runCoordinated(t, m)
		defer stop()
		if err := m.RunSpeedTest(t.Context(), SpeedTestRequest{Link: "ipv6"}, func(SpeedTestStatus) error { return nil }); err != nil {
			t.Fatal(err)
		}
		if !entered.Load() {
			t.Fatal("IPv6 test bypassed ordinary destination route/handshake path")
		}
	})
}

func TestSpeedTunnelVerificationRetriesTransientFailureOnly(t *testing.T) {
	for _, tc := range []struct {
		name    string
		failure error
		want    int
	}{
		{"timeout", context.DeadlineExceeded, 3},
		{"invalid-proof", model.NewError(model.ErrorProtocol, "invalid identity", false), 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				attempts := 0
				probe := SystemLocalProbe{HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { attempts++; return nil, tc.failure })}}
				cfg := model.PrivateAPIConfiguration{Key: base64.RawURLEncoding.EncodeToString(make([]byte, 32)), Endpoints: []string{"192.168.1.2:54790"}}
				ok, err := verifySpeedTunnel(t.Context(), probe, cfg, "device")
				if ok || err == nil || attempts != tc.want {
					t.Fatalf("ok=%v err=%v attempts=%d", ok, err, attempts)
				}
				if tc.want == 3 && !strings.Contains(err.Error(), "deadline exceeded") {
					t.Fatal("underlying timeout lost", err)
				}
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				_, err = verifySpeedTunnel(ctx, probe, cfg, "device")
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			})
		})
	}
}

type epochBridge struct {
	*coordinatedBridge
	epoch atomic.Uint64
}

func (b *epochBridge) SessionEpoch() uint64 { return b.epoch.Load() }

func TestSpeedPathCancelsBlockedIOAfterRelayReconnect(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		manager, probe, network, _, _ := coordinationFixture(t)
		bridge := &epochBridge{coordinatedBridge: &coordinatedBridge{events: make(chan error, 1)}}
		bridge.connected.Store(true)
		manager.bridge = bridge
		now := time.Now()
		network.status = model.ClientPrivilegedStatus{Active: true, Interface: "utun9", LastHandshake: &now}
		manager.setStatus(model.ClientRelay, "fn-connect", "utun9", nil)
		measurement := &speedMeasurement{Manager: manager, status: SpeedTestStatus{Link: "fn-connect"}}
		snapshot, _ := probe.Snapshot()
		ctx, stop := measurement.watchSpeedPath(t.Context(), snapshot.Fingerprint())
		defer stop()
		// The bridge is already connected again when the observer runs: a boolean
		// health check alone would silently continue waiting on the stale HTTP body.
		bridge.epoch.Add(1)
		time.Sleep(101 * time.Millisecond)
		synctest.Wait()
		if ctx.Err() == nil {
			t.Fatal("stale measurement survived reconnect")
		}
		outcome, reason := speedFailure(context.Cause(ctx))
		if outcome != "failed" || reason != "relay_interrupted" {
			t.Fatal(outcome, reason, context.Cause(ctx))
		}
	})
}

func TestLatencyPreservesServerAuthorizationFailure(t *testing.T) {
	measurement := &speedMeasurement{report: func(SpeedTestStatus) error { return nil }}
	h := &speedHTTP{base: "http://nas", client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusUnauthorized, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"code":"AUTH_REQUIRED","message":"expired speed test authorization","retryable":false}}`)), Request: r}, nil
	})}}
	err := measurement.measureLatency(t.Context(), h, model.SpeedTestPath, 0)
	if err == nil || !strings.Contains(err.Error(), "expired speed test authorization") {
		t.Fatalf("lost server rejection: %v", err)
	}
}

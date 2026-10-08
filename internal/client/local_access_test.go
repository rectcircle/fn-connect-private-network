package client

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

type permissionProbe struct {
	LocalProbe
	events chan localAccessState
}

func (p *permissionProbe) WatchLocalAccess(context.Context, string) (<-chan localAccessState, func()) {
	return p.events, func() { close(p.events) }
}

func TestLocalAccessWaitGrantSkipAndTimeout(t *testing.T) {
	for _, action := range []string{"grant", "skip", "timeout", "cancel", "unavailable"} {
		t.Run(action, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				m, _, _, _, _ := coordinationFixture(t)
				p := &permissionProbe{LocalProbe: m.probe, events: make(chan localAccessState, 2)}
				m.probe = p
				m.localProbeConfig = &model.LocalProbeConfiguration{Endpoints: []string{"192.168.71.2:54790"}}
				m.localAccessContext = t.Context()
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				done := make(chan struct{})
				if action == "unavailable" {
					p.events <- localAccessUnavailable
				} else {
					p.events <- localAccessWaiting
				}
				go func() { defer close(done); m.waitForLocalAccess(ctx, nil, LocalConfig{DeviceID: "fixture"}) }()
				synctest.Wait()
				if action == "unavailable" {
					<-done
					if m.Status().LocalNetworkWaiting {
						t.Fatal("ordinary unreachability presented as permission wait")
					}
					return
				}
				time.Sleep(localEndpointTimeout)
				synctest.Wait()
				if !m.Status().LocalNetworkWaiting {
					t.Fatal("missing permission waiting status")
				}
				select {
				case <-done:
					t.Fatal("permission wait consumed the ordinary probe budget")
				default:
				}
				switch action {
				case "grant":
					p.events <- localAccessAllowed
				case "skip":
					m.ContinueRemoteConnection()
				case "cancel":
					cancel()
				case "timeout":
					time.Sleep(localAccessWaitTimeout)
				}
				synctest.Wait()
				<-done
				if m.Status().LocalNetworkWaiting {
					t.Fatal("wait status was not cleared")
				}
				if action == "skip" {
					// The native observer remains active after fallback and sees a later grant.
					p.events <- localAccessAllowed
					synctest.Wait()
					if m.Status().LocalNetworkAccess != "allowed" {
						t.Fatal("late permission grant was lost")
					}
				}
				if action == "timeout" || action == "cancel" {
					m.localAccessCancel()
					<-m.localAccessReady
				}
			})
		})
	}
}

func TestDisconnectCancelsNativePermissionWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, _, _, _, _ := coordinationFixture(t)
		p := &permissionProbe{LocalProbe: m.probe, events: make(chan localAccessState, 1)}
		p.events <- localAccessWaiting
		m.probe = p
		stop := runCoordinated(t, m)
		defer stop()
		result := make(chan error, 1)
		go func() { result <- m.Connect(t.Context()) }()
		synctest.Wait()
		time.Sleep(localEndpointTimeout)
		synctest.Wait()
		if !m.Status().LocalNetworkWaiting {
			t.Fatal("startup did not wait for native permission")
		}
		if err := m.Disconnect(t.Context()); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		config, err := m.store.Load()
		if err != nil {
			t.Fatal(err)
		}
		if config.AutoConnect || m.Status().State != model.ClientPaused || m.Status().LocalNetworkWaiting {
			t.Fatal("disconnect did not cancel permission wait and preserve paused intent")
		}
		<-result
		if m.localAccessCancel != nil {
			t.Fatal("disconnect retained native access observer")
		}
	})
}

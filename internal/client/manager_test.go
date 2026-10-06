package client

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/ipc"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"github.com/rectcircle/fn-connect-private-network/internal/privileged"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

func TestManagerAuthorizesAndConnectsThroughRelay(t *testing.T) {
	store := NewConfigStore(
		filepath.Join(t.TempDir(), "config.json"),
		newMemorySecretStore(),
	)
	configuration := managerClientConfiguration()
	remote := &fakeRemoteService{
		bootstrap: Bootstrap{
			Administrator: true,
			Network: model.ServerNetworkSnapshot{
				Fresh: true,
				Network: model.ServerPrivilegedStatus{
					Active:    true,
					PublicKey: configuration.ServerPublicKey,
				},
			},
		},
		registration: model.DeviceRegistration{
			Device: model.Device{
				ID:             "device-1",
				PublicKey:      managerKey(3),
				OverlayAddress: configuration.ClientAddress,
				Enabled:        true,
			},
			Configuration: configuration,
		},
		configuration: configuration,
	}
	privileged := &fakePrivilegedNetwork{}
	bridge := &fakeBridge{endpoint: "127.0.0.1:51821"}
	manager, err := NewManager(ManagerOptions{
		Store: store,
		Discoverer: fakeDiscoverer{result: Discovery{
			FN:   []string{"home-nas.fnos.net:443"},
			Port: DiscoveryPorts{HTTP: 5666, HTTPS: 5667},
		}},
		Remote: func(string, []Cookie, func([]Cookie) error) (RemoteService, error) {
			return remote, nil
		},
		Privileged: privileged,
		Bridge:     bridge,
		Probe:      fakeLocalProbe{},
		DeviceName: "MacBook",
	})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	if err := manager.Authorize(
		context.Background(),
		"home-nas",
		[]Cookie{{Name: "fnos-token", Value: "token"}},
	); err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if status := manager.Status(); status.State != model.ClientRelay ||
		status.Path != "fn-connect" ||
		status.Interface != "utun9" {
		t.Fatalf("status = %+v", status)
	}
	if len(privileged.plans) != 1 ||
		privileged.plans[0].Mode != "relay" ||
		privileged.plans[0].Endpoint != bridge.endpoint {
		t.Fatalf("plans = %+v", privileged.plans)
	}
	saved, err := store.Load()
	if err != nil || saved == nil || saved.DeviceID != "device-1" {
		t.Fatalf("saved config = %+v, err=%v", saved, err)
	}
}

func TestManagerPrefersLocalThenDirect(t *testing.T) {
	tests := []struct {
		name        string
		local       bool
		ipv6        []string
		wantState   model.ClientState
		wantMode    string
		wantRemoved int
	}{
		{name: "local", local: true, wantState: model.ClientLocal, wantRemoved: 1},
		{
			name:        "direct",
			ipv6:        []string{"2606:4700:4700::1111"},
			wantState:   model.ClientDirect,
			wantMode:    "direct",
			wantRemoved: 1,
		},
		{
			name:        "direct without local global address",
			ipv6:        []string{"2606:4700:4700::1111"},
			wantState:   model.ClientDirect,
			wantMode:    "direct",
			wantRemoved: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := configuredManagerStore(t)
			privileged := &fakePrivilegedNetwork{}
			manager, err := NewManager(ManagerOptions{
				Store: store,
				Discoverer: fakeDiscoverer{result: Discovery{
					IPv4:       []string{"192.168.71.2"},
					PublicIPv6: test.ipv6,
					FN:         []string{"home-nas.fnos.net:443"},
					Port:       DiscoveryPorts{HTTP: 5666},
				}},
				Remote: func(string, []Cookie, func([]Cookie) error) (RemoteService, error) {
					return &fakeRemoteService{
						configuration: managerClientConfiguration(),
					}, nil
				},
				Privileged: privileged,
				Bridge:     &fakeBridge{endpoint: "127.0.0.1:51821"},
				Probe: fakeLocalProbe{
					reachable: test.local,
					addresses: func() []netip.Addr {
						if test.wantMode == "direct" {
							return []netip.Addr{netip.MustParseAddr("240e::10")}
						}
						return nil
					}(),
				},
				IPv6RouteChecker: func(context.Context, netip.Addr, int) bool { return true },
			})
			if err != nil {
				t.Fatalf("new manager: %v", err)
			}
			if err := manager.Connect(context.Background()); err != nil {
				t.Fatalf("connect: %v", err)
			}
			if status := manager.Status(); status.State != test.wantState {
				t.Fatalf("status = %+v", status)
			}
			if privileged.removed != test.wantRemoved {
				t.Fatalf("remove count = %d", privileged.removed)
			}
			if test.wantMode != "" &&
				(len(privileged.plans) != 1 ||
					privileged.plans[0].Mode != test.wantMode) {
				t.Fatalf("plans = %+v", privileged.plans)
			}
		})
	}
}

func TestManagerTriesNextDirectAddressAfterHandshakeTimeout(t *testing.T) {
	store := configuredManagerStore(t)
	firstEndpoint := "[2606:4700:4700::1001]:51820"
	privileged := &fakePrivilegedNetwork{
		failHandshakeEndpoints: map[string]bool{firstEndpoint: true},
	}
	manager, err := NewManager(ManagerOptions{
		Store: store,
		Discoverer: fakeDiscoverer{result: Discovery{
			PublicIPv6: []string{
				"2606:4700:4700::1001",
				"2606:4700:4700::1002",
			},
			FN: []string{"home-nas.fnos.net:443"},
		}},
		Remote: func(string, []Cookie, func([]Cookie) error) (RemoteService, error) {
			return &fakeRemoteService{
				configuration: managerClientConfiguration(),
			}, nil
		},
		Privileged:       privileged,
		Bridge:           &fakeBridge{endpoint: "127.0.0.1:51821"},
		Probe:            fakeLocalProbe{addresses: []netip.Addr{netip.MustParseAddr("2606:4700::1")}},
		HandshakeTimeout: time.Millisecond,
		IPv6RouteChecker: func(context.Context, netip.Addr, int) bool { return true },
	})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	if err := manager.Connect(context.Background()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	if status := manager.Status(); status.State != model.ClientDirect {
		t.Fatalf("status = %+v", status)
	}
	if len(privileged.plans) != 2 ||
		privileged.plans[0].Endpoint != firstEndpoint ||
		privileged.plans[1].Endpoint != "[2606:4700:4700::1002]:51820" {
		t.Fatalf("plans = %+v", privileged.plans)
	}
	if privileged.removed != 2 {
		t.Fatalf("failed DIRECT candidate cleanup count = %d", privileged.removed)
	}
}

func TestManagerReauthorizationReusesDevice(t *testing.T) {
	store := configuredManagerStore(t)
	remote := &fakeRemoteService{
		bootstrap:     Bootstrap{Administrator: true},
		configuration: managerClientConfiguration(),
	}
	manager, err := NewManager(ManagerOptions{
		Store: store,
		Discoverer: fakeDiscoverer{result: Discovery{
			FN: []string{"home-nas.fnos.net:443"},
		}},
		Remote: func(string, []Cookie, func([]Cookie) error) (RemoteService, error) {
			return remote, nil
		},
		Privileged: &fakePrivilegedNetwork{},
		Bridge:     &fakeBridge{endpoint: "127.0.0.1:51821"},
		Probe:      fakeLocalProbe{},
	})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	if err := manager.Authorize(
		context.Background(),
		"home-nas",
		[]Cookie{{Name: "fnos-token", Value: "new-token"}},
	); err != nil {
		t.Fatalf("reauthorize: %v", err)
	}
	if remote.registrations != 0 {
		t.Fatalf("device was registered again")
	}
}

func TestManagerKeepsLastValidCookiesWhenAuthorizationFails(t *testing.T) {
	store := configuredManagerStore(t)
	manager, err := NewManager(ManagerOptions{
		Store:      store,
		Discoverer: fakeDiscoverer{},
		Remote: func(string, []Cookie, func([]Cookie) error) (RemoteService, error) {
			return &fakeRemoteService{err: model.NewError(
				model.ErrorAuthRequired,
				"invalid session",
				false,
			)}, nil
		},
		Privileged: &fakePrivilegedNetwork{},
		Bridge:     &fakeBridge{},
		Probe:      fakeLocalProbe{},
	})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	err = manager.Authorize(
		context.Background(),
		"home-nas",
		[]Cookie{{Name: "fnos-token", Value: "invalid"}},
	)
	if err == nil {
		t.Fatal("authorization unexpectedly succeeded")
	}
	cookies, err := store.LoadCookies("home-nas")
	if err != nil {
		t.Fatalf("load cookies: %v", err)
	}
	if len(cookies) != 1 || cookies[0].Value != "token" {
		t.Fatalf("stored cookies changed: %+v", cookies)
	}
}

func TestManagerDisconnectCleanupFailureStaysPaused(t *testing.T) {
	store := configuredManagerStore(t)
	cleanupError := model.NewError(
		model.ErrorUnavailable,
		"privileged cleanup unavailable",
		true,
	)
	privileged := &fakePrivilegedNetwork{
		err: cleanupError,
		status: model.ClientPrivilegedStatus{
			Active:    true,
			Degraded:  true,
			Interface: "utun9",
		},
	}
	manager, err := NewManager(ManagerOptions{
		Store:      store,
		Discoverer: fakeDiscoverer{},
		Remote: func(string, []Cookie, func([]Cookie) error) (RemoteService, error) {
			return &fakeRemoteService{}, nil
		},
		Privileged: privileged,
		Bridge:     &fakeBridge{},
		Probe:      fakeLocalProbe{},
	})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	err = manager.Disconnect(context.Background())
	if !errors.Is(err, cleanupError) {
		t.Fatalf("disconnect error = %v", err)
	}
	config, loadErr := store.Load()
	if loadErr != nil {
		t.Fatalf("load config: %v", loadErr)
	}
	status := manager.Status()
	if config.AutoConnect ||
		status.State != model.ClientPaused ||
		status.LastError == nil ||
		status.LastError.Code != model.ErrorUnavailable {
		t.Fatalf("config=%+v status=%+v", config, status)
	}
	privileged.err = nil
	manager.maintainHealth(context.Background())
	status = manager.Status()
	if privileged.removed != 2 ||
		status.State != model.ClientPaused ||
		status.LastError != nil {
		t.Fatalf(
			"cleanup retry removed=%d status=%+v",
			privileged.removed,
			status,
		)
	}
}

func TestManagerStartCleansPausedResidualNetwork(t *testing.T) {
	store := configuredManagerStore(t)
	config, err := store.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	config.AutoConnect = false
	if err := store.Save(*config); err != nil {
		t.Fatalf("save paused config: %v", err)
	}
	privileged := &fakePrivilegedNetwork{status: model.ClientPrivilegedStatus{
		Active:    true,
		Degraded:  true,
		Interface: "utun9",
	}}
	manager, err := NewManager(ManagerOptions{
		Store:      store,
		Discoverer: fakeDiscoverer{},
		Remote: func(string, []Cookie, func([]Cookie) error) (RemoteService, error) {
			return &fakeRemoteService{}, nil
		},
		Privileged: privileged,
		Bridge:     &fakeBridge{},
		Probe:      fakeLocalProbe{},
	})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	if err := manager.Start(); err != nil {
		t.Fatalf("start manager: %v", err)
	}
	if privileged.removed != 0 {
		t.Fatal("startup performed network work before IPC readiness")
	}
	manager.resume(context.Background())
	if privileged.removed != 1 ||
		manager.Status().State != model.ClientPaused ||
		manager.Status().LastError != nil {
		t.Fatalf(
			"startup cleanup removed=%d status=%+v",
			privileged.removed,
			manager.Status(),
		)
	}
}

func TestManagerRejectsInvalidConfigurationUpdate(t *testing.T) {
	store := configuredManagerStore(t)
	invalid := managerClientConfiguration()
	invalid.ServerAddress = "broken"
	manager, err := NewManager(ManagerOptions{
		Store:      store,
		Discoverer: fakeDiscoverer{result: Discovery{}},
		Remote: func(string, []Cookie, func([]Cookie) error) (RemoteService, error) {
			return &fakeRemoteService{configuration: invalid}, nil
		},
		Privileged: &fakePrivilegedNetwork{},
		Bridge:     &fakeBridge{endpoint: "127.0.0.1:51821"},
		Probe:      fakeLocalProbe{},
	})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	if err := manager.Connect(context.Background()); err == nil {
		t.Fatal("invalid configuration update was accepted")
	}
	saved, err := store.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if saved.Configuration.ServerAddress != "10.203.0.1/24" {
		t.Fatalf("last valid configuration was overwritten: %+v", saved.Configuration)
	}
}

func TestManagerAcceptsConfigurationContentChange(t *testing.T) {
	store := configuredManagerStore(t)
	changed := managerClientConfiguration()
	changed.AllowedIPs = append(changed.AllowedIPs, "192.168.72.0/24")
	manager, err := NewManager(ManagerOptions{
		Store:      store,
		Discoverer: fakeDiscoverer{result: Discovery{}},
		Remote: func(string, []Cookie, func([]Cookie) error) (RemoteService, error) {
			return &fakeRemoteService{configuration: changed}, nil
		},
		Privileged: &fakePrivilegedNetwork{},
		Bridge:     &fakeBridge{endpoint: "127.0.0.1:51821"},
		Probe:      fakeLocalProbe{},
	})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	err = manager.Connect(context.Background())
	if err != nil {
		t.Fatalf("connect with changed configuration: %v", err)
	}
	saved, loadErr := store.Load()
	if loadErr != nil {
		t.Fatalf("load config: %v", loadErr)
	}
	if !slices.Equal(saved.Configuration.AllowedIPs, changed.AllowedIPs) {
		t.Fatalf("changed configuration was not saved: %+v", saved.Configuration)
	}
}

func TestRoutedPrefixesUsesHostRouteForOverlap(t *testing.T) {
	configuration := managerClientConfiguration()
	actual := routedPrefixes(
		configuration,
		[]netip.Prefix{netip.MustParsePrefix("192.168.71.0/24")},
	)
	expected := []string{"10.203.0.1/32"}
	if !slices.Equal(actual, expected) {
		t.Fatalf("routes = %v, want %v", actual, expected)
	}

	actual = routedPrefixes(
		configuration,
		[]netip.Prefix{netip.MustParsePrefix("192.168.71.0/24")},
	)
	if !slices.Equal(actual, []string{"10.203.0.1/32"}) {
		t.Fatalf("conflicting routes = %v", actual)
	}
}

func TestManagerPrimaryNetworkChangeReconnectsWithSameFingerprint(t *testing.T) {
	store := configuredManagerStore(t)
	privileged := &fakePrivilegedNetwork{}
	manager, err := NewManager(ManagerOptions{
		Store: store,
		Discoverer: fakeDiscoverer{result: Discovery{
			FN: []string{"home-nas.fnos.net:443"},
		}},
		Remote: func(string, []Cookie, func([]Cookie) error) (RemoteService, error) {
			return &fakeRemoteService{
				configuration: managerClientConfiguration(),
			}, nil
		},
		Privileged: privileged,
		Bridge:     &fakeBridge{endpoint: "127.0.0.1:51821"},
		Probe: fakeLocalProbe{
			prefixes: []netip.Prefix{netip.MustParsePrefix("192.168.1.0/24")},
		},
	})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	previous := manager.networkFingerprint()
	manager.handleNetworkChange(context.Background(), &previous, false)
	if len(privileged.plans) != 0 {
		t.Fatalf("unchanged periodic fingerprint triggered reconnect: %+v", privileged.plans)
	}

	manager.handleNetworkChange(context.Background(), &previous, true)
	if len(privileged.plans) != 1 ||
		manager.Status().State != model.ClientRelay {
		t.Fatalf(
			"primary network change did not reconnect: plans=%+v status=%+v",
			privileged.plans,
			manager.Status(),
		)
	}
}

func TestManagerStopsRelayAfterAuthorizationEvent(t *testing.T) {
	bridgeEvents := make(chan error, 1)
	bridge := &fakeBridge{events: bridgeEvents}
	cleanupError := model.NewError(
		model.ErrorUnavailable,
		"cleanup unavailable",
		true,
	)
	privileged := &fakePrivilegedNetwork{
		err: cleanupError,
		status: model.ClientPrivilegedStatus{
			Active:    true,
			Degraded:  true,
			Interface: "utun9",
		},
	}
	manager, err := NewManager(ManagerOptions{
		Store:      configuredManagerStore(t),
		Discoverer: fakeDiscoverer{},
		Remote: func(string, []Cookie, func([]Cookie) error) (RemoteService, error) {
			return &fakeRemoteService{}, nil
		},
		Privileged: privileged,
		Bridge:     bridge,
		Probe:      fakeLocalProbe{},
	})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go manager.Run(ctx)
	bridgeEvents <- model.NewError(
		model.ErrorAuthRequired,
		"authorization required",
		false,
	)
	deadline := time.Now().Add(time.Second)
	for {
		if manager.Status().State == model.ClientAuthRequired {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("relay authorization event was not handled")
		}
		time.Sleep(time.Millisecond)
	}
	if bridge.stopped != 1 || privileged.removed != 1 {
		t.Fatalf(
			"relay cleanup bridge=%d privileged=%d",
			bridge.stopped,
			privileged.removed,
		)
	}
	privileged.err = nil
	manager.maintainHealth(context.Background())
	status := manager.Status()
	if privileged.removed != 2 ||
		status.State != model.ClientAuthRequired ||
		status.LastError == nil ||
		status.LastError.Code != model.ErrorAuthRequired {
		t.Fatalf(
			"auth cleanup retry removed=%d status=%+v",
			privileged.removed,
			status,
		)
	}
}

func TestManagerHealthMaintenanceDoesNotPollConfiguration(t *testing.T) {
	remote := &fakeRemoteService{
		configuration: managerClientConfiguration(),
	}
	manager, err := NewManager(ManagerOptions{
		Store:      configuredManagerStore(t),
		Discoverer: fakeDiscoverer{},
		Remote: func(string, []Cookie, func([]Cookie) error) (RemoteService, error) {
			return remote, nil
		},
		Privileged: &fakePrivilegedNetwork{},
		Bridge:     &fakeBridge{},
		Probe:      fakeLocalProbe{reachable: true},
	})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	manager.setStatus(model.ClientLocal, "local", "", nil)
	manager.localProbeConfig = &model.LocalProbeConfiguration{
		Endpoints: []string{"192.168.71.2:54790"},
		Key:       "probe-key",
	}

	manager.maintainHealth(context.Background())

	if remote.configurationRequests != 0 {
		t.Fatalf(
			"health maintenance fetched configuration %d times",
			remote.configurationRequests,
		)
	}
}

func TestManagerConfigurationWatchNotifiesAfterRecovery(t *testing.T) {
	watchCalls := 0
	remote := &fakeWatchingRemoteService{
		fakeRemoteService: &fakeRemoteService{
			configuration: managerClientConfiguration(),
			err: model.NewError(
				model.ErrorUnavailable,
				"configuration unavailable",
				true,
			),
		},
		watch: func(
			ctx context.Context,
			_ string,
			_ string,
		) (ConfigurationWatchResult, error) {
			watchCalls++
			switch watchCalls {
			case 1:
				return ConfigurationWatchResult{}, model.NewError(
					model.ErrorUnavailable,
					"configuration watch unavailable",
					true,
				)
			case 2:
				return ConfigurationWatchResult{Cursor: "unchanged"}, nil
			default:
				<-ctx.Done()
				return ConfigurationWatchResult{}, ctx.Err()
			}
		},
	}
	manager, err := NewManager(ManagerOptions{
		Store:      configuredManagerStore(t),
		Discoverer: fakeDiscoverer{},
		Remote: func(string, []Cookie, func([]Cookie) error) (RemoteService, error) {
			return remote, nil
		},
		Privileged: &fakePrivilegedNetwork{},
		Bridge:     &fakeBridge{},
		Probe:      fakeLocalProbe{reachable: true},
	})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	manager.setStatus(model.ClientLocal, "local", "", nil)
	manager.localProbeConfig = &model.LocalProbeConfiguration{
		Endpoints: []string{"192.168.71.2:54790"},
		Key:       "probe-key",
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go manager.watchConfiguration(ctx)

	deadline := time.Now().Add(time.Second)
	for manager.Status().LastError == nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if status := manager.Status(); status.LastError == nil {
		t.Fatal("configuration failure was not recorded")
	}

	deadline = time.Now().Add(2 * time.Second)
	for manager.Status().LastError != nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if status := manager.Status(); status.LastError != nil {
		t.Fatalf("configuration recovery status = %+v", status)
	}
}

func TestManagerConfigurationWatchAppliesResponseBeforeCursor(t *testing.T) {
	changed := managerClientConfiguration()
	changed.AllowedIPs = append(changed.AllowedIPs, "192.168.72.0/24")
	nextCursor := make(chan string, 1)
	watchCalls := 0
	remote := &fakeWatchingRemoteService{
		fakeRemoteService: &fakeRemoteService{
			configuration: managerClientConfiguration(),
		},
		watch: func(
			ctx context.Context,
			_ string,
			cursor string,
		) (ConfigurationWatchResult, error) {
			watchCalls++
			if watchCalls == 1 {
				return ConfigurationWatchResult{
					Changed:       true,
					Cursor:        "changed",
					Configuration: changed,
				}, nil
			}
			nextCursor <- cursor
			<-ctx.Done()
			return ConfigurationWatchResult{}, ctx.Err()
		},
	}
	store := configuredManagerStore(t)
	manager, err := NewManager(ManagerOptions{
		Store:      store,
		Discoverer: fakeDiscoverer{},
		Remote: func(string, []Cookie, func([]Cookie) error) (RemoteService, error) {
			return remote, nil
		},
		Privileged: &fakePrivilegedNetwork{},
		Bridge:     &fakeBridge{},
		Probe:      fakeLocalProbe{reachable: true},
	})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	manager.setStatus(model.ClientLocal, "local", "", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go manager.watchConfiguration(ctx)

	select {
	case cursor := <-nextCursor:
		if cursor != "changed" {
			t.Fatalf("next cursor = %q", cursor)
		}
	case <-time.After(time.Second):
		t.Fatal("configuration response was not acknowledged")
	}
	saved, err := store.Load()
	if err != nil {
		t.Fatalf("load configuration: %v", err)
	}
	if !slices.Equal(saved.Configuration.AllowedIPs, changed.AllowedIPs) {
		t.Fatalf("watched configuration was not saved: %+v", saved.Configuration)
	}
	if remote.configurationRequests != 0 {
		t.Fatalf(
			"watched configuration triggered %d extra fetches",
			remote.configurationRequests,
		)
	}
}

func TestManagerMaintenanceReportsLocalConfigurationReadFailure(t *testing.T) {
	store := configuredManagerStore(t)
	config, err := store.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	manager, err := NewManager(ManagerOptions{
		Store:      store,
		Discoverer: fakeDiscoverer{},
		Remote: func(string, []Cookie, func([]Cookie) error) (RemoteService, error) {
			return &fakeRemoteService{
				configuration: managerClientConfiguration(),
			}, nil
		},
		Privileged: &fakePrivilegedNetwork{},
		Bridge:     &fakeBridge{},
		Probe:      fakeLocalProbe{reachable: true},
	})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	manager.setStatus(model.ClientLocal, "local", "", nil)
	manager.localProbeConfig = &model.LocalProbeConfiguration{
		Endpoints: []string{"192.168.71.2:54790"},
		Key:       "probe-key",
	}
	if err := os.WriteFile(store.path, []byte("{"), 0o600); err != nil {
		t.Fatalf("corrupt config: %v", err)
	}

	manager.maintainHealth(context.Background())
	status := manager.Status()
	if status.State != model.ClientLocal ||
		status.LastError == nil ||
		status.LastError.Code != model.ErrorFailedPrecondition {
		t.Fatalf("configuration read failure status = %+v", status)
	}

	if err := store.Save(*config); err != nil {
		t.Fatalf("restore config: %v", err)
	}
	if err := manager.applyWatchedConfiguration(context.Background(), config.Configuration); err != nil {
		t.Fatal(err)
	}
	if status = manager.Status(); status.LastError != nil {
		t.Fatalf("recovered configuration read status = %+v", status)
	}
}

func TestManagerMaintenanceAppliesConfigurationContentChange(t *testing.T) {
	changed := managerClientConfiguration()
	changed.AllowedIPs = append(changed.AllowedIPs, "192.168.72.0/24")
	remote := &fakeRemoteService{configuration: changed}
	store := configuredManagerStore(t)
	manager, err := NewManager(ManagerOptions{
		Store:      store,
		Discoverer: fakeDiscoverer{},
		Remote: func(string, []Cookie, func([]Cookie) error) (RemoteService, error) {
			return remote, nil
		},
		Privileged: &fakePrivilegedNetwork{},
		Bridge:     &fakeBridge{},
		Probe:      fakeLocalProbe{reachable: true},
	})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	manager.setStatus(model.ClientLocal, "local", "", nil)
	manager.localProbeConfig = &model.LocalProbeConfiguration{
		Endpoints: []string{"192.168.71.2:54790"},
		Key:       "probe-key",
	}

	if err := manager.applyWatchedConfiguration(context.Background(), changed); err != nil {
		t.Fatal(err)
	}
	status := manager.Status()
	if status.LastError != nil {
		t.Fatalf("configuration update status = %+v", status)
	}
	saved, loadErr := store.Load()
	if loadErr != nil {
		t.Fatalf("load config: %v", loadErr)
	}
	if !slices.Equal(saved.Configuration.AllowedIPs, changed.AllowedIPs) {
		t.Fatalf("changed configuration was not saved: %+v", saved.Configuration)
	}
}

func TestManagerPrivilegedMaintenanceRebuildsEmptyRoot(t *testing.T) {
	privileged := &fakePrivilegedNetwork{}
	manager, err := NewManager(ManagerOptions{
		Store:      configuredManagerStore(t),
		Discoverer: fakeDiscoverer{},
		Remote: func(string, []Cookie, func([]Cookie) error) (RemoteService, error) {
			return &fakeRemoteService{
				configuration: managerClientConfiguration(),
			}, nil
		},
		Privileged: privileged,
		Bridge:     &fakeBridge{},
		Probe:      fakeLocalProbe{reachable: true},
	})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	manager.setStatus(model.ClientDirect, "ipv6", "utun9", nil)

	manager.maintainPrivileged(context.Background())

	if status := manager.Status(); status.State != model.ClientLocal {
		t.Fatalf("rebuilt status = %+v", status)
	}
	if privileged.removed != 1 {
		t.Fatalf("privileged remove count = %d", privileged.removed)
	}

	manager.setStatus(model.ClientReconnecting, "ipv6", "utun9", nil)
	manager.maintainPrivileged(context.Background())
	if status := manager.Status(); status.State != model.ClientLocal {
		t.Fatalf("reconnecting recovery status = %+v", status)
	}
	if privileged.removed != 2 {
		t.Fatalf("reconnecting remove count = %d", privileged.removed)
	}
}

func TestManagerPrivilegedMaintenanceRecordsStatusFailure(t *testing.T) {
	privileged := &fakePrivilegedNetwork{
		err: model.NewError(
			model.ErrorUnavailable,
			"privileged daemon unavailable",
			true,
		),
	}
	manager, err := NewManager(ManagerOptions{
		Store:      configuredManagerStore(t),
		Discoverer: fakeDiscoverer{},
		Remote: func(string, []Cookie, func([]Cookie) error) (RemoteService, error) {
			return &fakeRemoteService{}, nil
		},
		Privileged: privileged,
		Bridge:     &fakeBridge{},
		Probe:      fakeLocalProbe{},
	})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	manager.setStatus(model.ClientDirect, "ipv6", "utun9", nil)

	manager.maintainPrivileged(context.Background())

	if status := manager.Status(); status.State != model.ClientReconnecting ||
		status.LastError == nil ||
		status.LastError.Code != model.ErrorUnavailable {
		t.Fatalf("status failure was not published: %+v", status)
	}
	if privileged.removed != 0 {
		t.Fatalf("status failure removed=%d", privileged.removed)
	}
}

func TestManagerMaintainsLocalWithCachedProbeConfiguration(t *testing.T) {
	remote := &fakeRemoteService{
		configuration: managerClientConfiguration(),
		probeConfiguration: model.LocalProbeConfiguration{
			Endpoints: []string{"192.168.71.2:54790"},
			Key:       "probe-key",
		},
	}
	manager, err := NewManager(ManagerOptions{
		Store:      configuredManagerStore(t),
		Discoverer: fakeDiscoverer{},
		Remote: func(string, []Cookie, func([]Cookie) error) (RemoteService, error) {
			return remote, nil
		},
		Privileged: &fakePrivilegedNetwork{},
		Bridge:     &fakeBridge{},
		Probe:      fakeLocalProbe{reachable: true},
	})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	if err := manager.Connect(context.Background()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	if manager.Status().State != model.ClientLocal || remote.probeRequests != 1 {
		t.Fatalf(
			"initial local state=%s probeRequests=%d",
			manager.Status().State,
			remote.probeRequests,
		)
	}
	remote.probeErr = model.NewError(
		model.ErrorUnavailable,
		"control plane unavailable",
		true,
	)
	manager.maintainHealth(context.Background())
	if manager.Status().State != model.ClientLocal || remote.probeRequests != 1 {
		t.Fatalf(
			"cached local state=%s probeRequests=%d",
			manager.Status().State,
			remote.probeRequests,
		)
	}
}

func TestManagerDiagnoseIncludesPrivilegedAndOverlapState(t *testing.T) {
	now := time.Now().UTC().Add(-time.Minute)
	privileged := &fakePrivilegedNetwork{status: model.ClientPrivilegedStatus{
		Active:        true,
		Degraded:      true,
		Interface:     "utun9",
		MTU:           1280,
		LastHandshake: &now,
	}}
	manager, err := NewManager(ManagerOptions{
		Store:      configuredManagerStore(t),
		Discoverer: fakeDiscoverer{},
		Remote: func(string, []Cookie, func([]Cookie) error) (RemoteService, error) {
			return &fakeRemoteService{}, nil
		},
		Privileged: privileged,
		Bridge:     &fakeBridge{},
		Probe: fakeLocalProbe{
			prefixes: []netip.Prefix{
				netip.MustParsePrefix("192.168.71.0/24"),
			},
		},
	})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	manager.setStatus(model.ClientDirect, "ipv6", "utun9", nil)
	manager.mu.Lock()
	manager.status.MTU = 1420
	manager.mu.Unlock()
	diagnostics := manager.Diagnose(context.Background())
	if !diagnostics.PrivilegedAvailable ||
		!diagnostics.PrivilegedActive ||
		!diagnostics.PrivilegedDegraded ||
		diagnostics.NetworkInterface != "utun9" ||
		diagnostics.MTU != 1280 ||
		!diagnostics.LANOverlap ||
		diagnostics.HandshakeAge == "" {
		t.Fatalf("diagnostics = %+v", diagnostics)
	}
}

func TestClientNetworkHealthErrorIncludesCause(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name      string
		state     model.ClientState
		network   model.ClientPrivilegedStatus
		connected bool
		code      model.ErrorCode
	}{
		{
			name:  "inactive",
			state: model.ClientDirect,
			code:  model.ErrorUnavailable,
		},
		{
			name:    "missing handshake",
			state:   model.ClientDirect,
			network: model.ClientPrivilegedStatus{Active: true},
			code:    model.ErrorUnavailable,
		},
		{
			name:  "stale handshake",
			state: model.ClientDirect,
			network: model.ClientPrivilegedStatus{
				Active:        true,
				LastHandshake: func() *time.Time { value := now.Add(-4 * time.Minute); return &value }(),
			},
			code: model.ErrorTimeout,
		},
		{
			name:  "relay disconnected",
			state: model.ClientRelay,
			network: model.ClientPrivilegedStatus{
				Active:        true,
				LastHandshake: &now,
			},
			code: model.ErrorUnavailable,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := clientNetworkHealthError(
				test.state,
				test.network,
				nil,
				test.connected,
			)
			if err == nil || model.AsError(err).Code != test.code {
				t.Fatalf("health error = %v", err)
			}
		})
	}
}

func TestIPCPrivilegedNetworkRejectsUnavailableEngine(t *testing.T) {
	directory, err := os.MkdirTemp("/tmp", "fncpn-ipc-")
	if err != nil {
		t.Fatalf("create short socket directory: %v", err)
	}
	defer os.RemoveAll(directory)
	socketPath := filepath.Join(directory, "privileged.sock")
	ctx, cancel := context.WithCancel(context.Background())
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- (ipc.Server{
			SocketPath: socketPath,
			Handler: ipc.HandlerFunc(func(
				_ context.Context,
				request ipc.Request,
			) ipc.Response {
				return ipc.Success(request.ID, privileged.ClientStatus{
					Role:      "client",
					Available: false,
				})
			}),
		}).Serve(ctx)
	}()
	deadline := time.Now().Add(time.Second)
	for {
		if _, err := os.Stat(socketPath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("privileged test socket was not created")
		}
		time.Sleep(time.Millisecond)
	}
	network := IPCPrivilegedNetwork{Client: ipc.Client{
		SocketPath: socketPath,
	}}
	_, err = network.Status(context.Background())
	if err == nil || model.AsError(err).Code != model.ErrorUnavailable {
		t.Fatalf("unavailable engine error = %v", err)
	}
	cancel()
	if err := <-serverErr; err != nil {
		t.Fatalf("serve privileged test socket: %v", err)
	}
}

func TestSystemLocalProbeUsesDeviceHMACWithoutCookies(t *testing.T) {
	key := make([]byte, sha256.Size)
	for index := range key {
		key[index] = 7
	}
	var requestSeen bool
	probe := SystemLocalProbe{HTTPClient: &http.Client{
		Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			requestSeen = true
			if request.Method != http.MethodPost ||
				request.URL.String() != "http://192.168.1.10:54790/probe" {
				t.Fatalf("probe URL = %s", request.URL)
			}
			if request.Header.Get("Cookie") != "" {
				t.Fatalf("probe sent cookies: %q", request.Header.Get("Cookie"))
			}
			var input model.LocalProbeRequest
			if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			if input.DeviceID != "device-1" {
				t.Fatalf("probe device ID = %q", input.DeviceID)
			}
			nonce, err := base64.RawURLEncoding.DecodeString(input.Nonce)
			if err != nil {
				t.Fatalf("decode nonce: %v", err)
			}
			mac := hmac.New(sha256.New, key)
			_, _ = mac.Write([]byte(model.LocalProbeDomain))
			_, _ = mac.Write(nonce)
			responseBody, err := json.Marshal(model.LocalProbeResponse{
				Proof: base64.RawURLEncoding.EncodeToString(mac.Sum(nil)),
			})
			if err != nil {
				t.Fatalf("encode response: %v", err)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(string(responseBody))),
				Request:    request,
			}, nil
		}),
	}}
	reachable, err := probe.Reachable(
		context.Background(),
		model.LocalProbeConfiguration{
			Endpoints: []string{"192.168.1.10:54790"},
			Key:       base64.RawURLEncoding.EncodeToString(key),
		},
		"device-1",
		NetworkSnapshot{InterfaceIndex: 7},
	)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if !requestSeen || !reachable {
		t.Fatalf("requestSeen=%t reachable=%t", requestSeen, reachable)
	}
	wrongKey := make([]byte, sha256.Size)
	reachable, err = probe.Reachable(
		context.Background(),
		model.LocalProbeConfiguration{
			Endpoints: []string{"192.168.1.10:54790"},
			Key:       base64.RawURLEncoding.EncodeToString(wrongKey),
		},
		"device-1",
		NetworkSnapshot{InterfaceIndex: 7},
	)
	if err == nil || model.AsError(err).Code != model.ErrorProtocol {
		t.Fatalf("probe with wrong key should report protocol failure: %v", err)
	}
	if reachable {
		t.Fatal("probe accepted a proof for another device key")
	}
}

func TestUsablePhysicalInterfaceAllowsOnlyDefaultBridge(t *testing.T) {
	if !usablePhysicalInterface("bridge0", "bridge0") {
		t.Fatal("default Thunderbolt bridge was rejected")
	}
	if usablePhysicalInterface("bridge100", "en0") {
		t.Fatal("non-default bridge was accepted")
	}
	if usablePhysicalInterface("utun4", "utun4") {
		t.Fatal("default utun was accepted")
	}
	if !usablePhysicalInterface("en5", "en5") {
		t.Fatal("default Ethernet interface was rejected")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	return function(request)
}

func configuredManagerStore(t *testing.T) *ConfigStore {
	t.Helper()
	store := NewConfigStore(
		filepath.Join(t.TempDir(), "config.json"),
		newMemorySecretStore(),
	)
	privateKey, err := store.EnsureWireGuardKey("home-nas")
	if err != nil {
		t.Fatalf("ensure key: %v", err)
	}
	configuration := managerClientConfiguration()
	if err := store.Save(LocalConfig{
		Version:       localConfigVersion,
		FNID:          "home-nas",
		DeviceID:      configuration.DeviceID,
		PublicKey:     privateKey.PublicKey().String(),
		Configuration: configuration,
		AutoConnect:   true,
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}
	if err := store.SaveCookies(
		"home-nas",
		[]Cookie{{Name: "fnos-token", Value: "token"}},
	); err != nil {
		t.Fatalf("save cookies: %v", err)
	}
	return store
}

func managerClientConfiguration() model.ClientConfiguration {
	return model.ClientConfiguration{
		DeviceID:        "device-1",
		ServerPublicKey: managerKey(2),
		ServerAddress:   "10.203.0.1/24",
		ClientAddress:   "10.203.0.2/32",
		ListenPort:      51820,
		AllowedIPs:      []string{"10.203.0.1/32", "192.168.71.0/24"},
	}
}

func managerKey(value byte) string {
	var key wgtypes.Key
	key[0] = value
	return key.String()
}

type fakeDiscoverer struct {
	result Discovery
	err    error
}

func (d fakeDiscoverer) Discover(context.Context, string) (Discovery, error) {
	return d.result, d.err
}

type fakeRemoteService struct {
	bootstrap             Bootstrap
	registration          model.DeviceRegistration
	configuration         model.ClientConfiguration
	probeConfiguration    model.LocalProbeConfiguration
	err                   error
	probeErr              error
	registrations         int
	configurationRequests int
	probeRequests         int
	// failConfigOnce makes the first Configuration request return AUTH_REQUIRED
	// so the deferred on-demand native session recovery can be exercised.
	failConfigOnce bool
}

type fakeWatchingRemoteService struct {
	*fakeRemoteService
	watch func(
		context.Context,
		string,
		string,
	) (ConfigurationWatchResult, error)
}

func (s *fakeWatchingRemoteService) WatchConfiguration(
	ctx context.Context,
	deviceID string,
	cursor string,
) (ConfigurationWatchResult, error) {
	return s.watch(ctx, deviceID, cursor)
}

func (s *fakeRemoteService) Bootstrap(context.Context) (Bootstrap, error) {
	return s.bootstrap, s.err
}

func (s *fakeRemoteService) RegisterDevice(
	_ context.Context,
	_ string,
	publicKey string,
) (model.DeviceRegistration, error) {
	s.registrations++
	registration := s.registration
	registration.Device.PublicKey = publicKey
	return registration, s.err
}

func (s *fakeRemoteService) Configuration(
	context.Context,
	string,
) (model.ClientConfiguration, error) {
	s.configurationRequests++
	if s.failConfigOnce {
		s.failConfigOnce = false
		return model.ClientConfiguration{}, model.NewError(
			model.ErrorAuthRequired, "fnOS gateway returned invalid token", false,
		)
	}
	return s.configuration, s.err
}

func (s *fakeRemoteService) LocalProbeConfiguration(
	context.Context,
	string,
) (model.LocalProbeConfiguration, error) {
	s.probeRequests++
	return s.probeConfiguration, errors.Join(s.err, s.probeErr)
}

type fakePrivilegedNetwork struct {
	plans                  []model.ClientPlan
	removed                int
	err                    error
	status                 model.ClientPrivilegedStatus
	failHandshakeEndpoints map[string]bool
}

func (n *fakePrivilegedNetwork) Apply(
	_ context.Context,
	plan model.ClientPlan,
) (model.ClientPrivilegedStatus, error) {
	n.plans = append(n.plans, plan)
	var handshake *time.Time
	if !n.failHandshakeEndpoints[plan.Endpoint] {
		now := time.Now().UTC()
		handshake = &now
	}
	n.status = model.ClientPrivilegedStatus{
		Active:        true,
		Interface:     "utun9",
		ListenPort:    49152,
		MTU:           plan.MTU,
		LastHandshake: handshake,
	}
	return n.status, n.err
}

func (n *fakePrivilegedNetwork) Status(
	context.Context,
) (model.ClientPrivilegedStatus, error) {
	return n.status, n.err
}

func (n *fakePrivilegedNetwork) Remove(context.Context) error {
	n.removed++
	if n.err == nil {
		n.status = model.ClientPrivilegedStatus{}
	}
	return n.err
}

type fakeBridge struct {
	endpoint  string
	started   int
	stopped   int
	connected bool
	err       error
	events    chan error
}

func (b *fakeBridge) Start(
	context.Context,
	string,
	CookieProvider,
	...func([]Cookie, []Cookie) error,
) (string, error) {
	b.started++
	b.connected = b.err == nil
	return b.endpoint, b.err
}

func (*fakeBridge) BindPeer(uint16) error {
	return nil
}

func (b *fakeBridge) Events() <-chan error {
	return b.events
}

func (b *fakeBridge) Stop() {
	b.stopped++
	b.connected = false
}

func (b *fakeBridge) Connected() bool {
	return b.connected
}

type fakeLocalProbe struct {
	reachable bool
	prefixes  []netip.Prefix
	addresses []netip.Addr
	err       error
}

func (p fakeLocalProbe) Reachable(
	context.Context,
	model.LocalProbeConfiguration,
	string,
	NetworkSnapshot,
) (bool, error) {
	return p.reachable, p.err
}

func (p fakeLocalProbe) Snapshot() (NetworkSnapshot, error) {
	return NetworkSnapshot{
		InterfaceName:  "en0",
		InterfaceIndex: 1,
		Prefixes:       p.prefixes,
		Addresses:      p.addresses,
		HasPublicIPv6:  len(p.addresses) > 0,
	}, p.err
}

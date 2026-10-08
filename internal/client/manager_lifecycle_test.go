package client

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"github.com/rectcircle/fn-connect-private-network/internal/version"
)

func newRecoveryTestManager(t *testing.T, store *ConfigStore, network *fakePrivilegedNetwork, remote RemoteFactory) *Manager {
	t.Helper()
	if remote == nil {
		remote = func(string, []Cookie, func([]Cookie) error) (RemoteService, error) {
			return &fakeRemoteService{
				bootstrap:     Bootstrap{ServerVersion: version.Current, Administrator: true},
				configuration: managerClientConfiguration(),
			}, nil
		}
	}
	manager, err := NewManager(ManagerOptions{
		Store: store, Discoverer: fakeDiscoverer{}, Remote: remote,
		Privileged: network, Bridge: &fakeBridge{}, Probe: fakeLocalProbe{reachable: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func TestManagerWatchDiscardsOldSessionCookiesAndConfiguration(t *testing.T) {
	for _, action := range []string{"logout", "forget", "authorize", "disconnect"} {
		t.Run(action, func(t *testing.T) {
			store := configuredManagerStore(t)
			started := make(chan context.Context, 1)
			release := make(chan struct{})
			var calls atomic.Int32
			remote := func(_ string, _ []Cookie, save func([]Cookie) error) (RemoteService, error) {
				return &fakeWatchingRemoteService{
					fakeRemoteService: &fakeRemoteService{
						bootstrap:     Bootstrap{ServerVersion: version.Current, Administrator: true},
						configuration: managerClientConfiguration(),
					},
					watch: func(ctx context.Context, _, _ string) (ConfigurationWatchResult, error) {
						if calls.Add(1) != 1 {
							<-ctx.Done()
							return ConfigurationWatchResult{}, ctx.Err()
						}
						started <- ctx
						// Model a response already delivered despite cancellation.
						<-release
						if err := save([]Cookie{{Name: "fnos-token", Value: "obsolete"}}); err != nil {
							return ConfigurationWatchResult{}, err
						}
						changed := managerClientConfiguration()
						changed.AllowedIPs = append(changed.AllowedIPs, "192.168.72.0/24")
						return ConfigurationWatchResult{Changed: true, Cursor: "obsolete", Configuration: changed}, nil
					},
				}, nil
			}
			manager := newRecoveryTestManager(t, store, &fakePrivilegedNetwork{}, remote)
			manager.setStatus(model.ClientLocal, "local", "", nil)
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() { defer close(done); manager.watchConfiguration(ctx) }()
			var requestContext context.Context
			select {
			case requestContext = <-started:
			case <-time.After(time.Second):
				cancel()
				t.Fatal("watch did not start")
			}
			var err error
			switch action {
			case "logout":
				err = manager.Logout(context.Background())
			case "forget":
				err = manager.Forget(context.Background())
			case "authorize":
				err = manager.Authorize(context.Background(), "home-nas", []Cookie{{Name: "fnos-token", Value: "new"}})
			case "disconnect":
				err = manager.Disconnect(context.Background())
			}
			canceled := requestContext.Err() != nil
			cancel()
			close(release)
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("watch did not stop")
			}
			if err != nil || !canceled {
				t.Fatalf("action err=%v request canceled=%v", err, canceled)
			}
			cookies, err := store.LoadCookies("home-nas")
			if err != nil {
				t.Fatal(err)
			}
			switch action {
			case "logout", "forget":
				if len(cookies) != 0 {
					t.Fatal("old response restored deleted credentials")
				}
			case "authorize":
				if len(cookies) != 1 || cookies[0].Value != "new" {
					t.Fatal("old response overwrote new authorization")
				}
			case "disconnect":
				if len(cookies) != 1 || cookies[0].Value != "token" {
					t.Fatal("old response committed after disconnect")
				}
			}
			config, err := store.Load()
			if err != nil {
				t.Fatal(err)
			}
			if config != nil && len(config.Configuration.AllowedIPs) != 2 {
				t.Fatal("old response overwrote configuration")
			}
		})
	}
}

func TestManagerBackgroundRecoveryHonorsConnectionIntent(t *testing.T) {
	store := configuredManagerStore(t)
	network := &fakePrivilegedNetwork{}
	manager := newRecoveryTestManager(t, store, network, nil)
	manager.setStatus(model.ClientAuthRequired, "", "", nil)
	changed := managerClientConfiguration()
	changed.AllowedIPs = append(changed.AllowedIPs, "192.168.72.0/24")
	if err := manager.applyWatchedConfiguration(context.Background(), changed); err != nil {
		t.Fatal(err)
	}
	if err := manager.runConnectionWork(context.Background(), workReconcile, nil); err != nil {
		t.Fatal(err)
	}
	manager.maintainPrivileged(context.Background())
	manager.maintainHealth(context.Background())
	if manager.Status().State != model.ClientAuthRequired || network.removed != 0 {
		t.Fatal("background activity resumed an authorization-blocked client")
	}
	if err := manager.Disconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := manager.runConnectionWork(context.Background(), workReconcile, nil); err != nil {
		t.Fatal(err)
	}
	config, _ := store.Load()
	if config.AutoConnect || manager.Status().State != model.ClientPaused {
		t.Fatal("network event enabled automatic connection")
	}
}

func TestManagerResumesWhenConsolePermissionReturns(t *testing.T) {
	network := &fakePrivilegedNetwork{err: model.NewError(model.ErrorPermissionDenied, "not console owner", false)}
	store := configuredManagerStore(t)
	manager := newRecoveryTestManager(t, store, network, nil)
	manager.setStatus(model.ClientDirect, "ipv6", "utun9", nil)
	manager.maintainPrivileged(context.Background())
	manager.maintainHealth(context.Background())
	if manager.Status().State != model.ClientPaused {
		t.Fatalf("permission blocked state=%s", manager.Status().State)
	}
	network.err = nil
	manager.maintainPrivileged(context.Background())
	if manager.Status().State != model.ClientLocal {
		t.Fatalf("permission recovery state=%s", manager.Status().State)
	}
	config, _ := store.Load()
	if !config.AutoConnect {
		t.Fatal("temporary permission failure changed user intent")
	}
}

func TestDegradedRootIsUnhealthyEvenWithRecentHandshake(t *testing.T) {
	now := time.Now()
	err := clientNetworkHealthError(model.ClientDirect, model.ClientPrivilegedStatus{
		Active: true, Degraded: true, LastHandshake: &now,
	}, nil, false)
	if err == nil {
		t.Fatal("degraded root was considered healthy")
	}
}

func TestWatchRetriesWhenRelayRenewedCredentialsInFlight(t *testing.T) {
	store := configuredManagerStore(t)
	var calls atomic.Int32
	recovered := make(chan struct{}, 1)
	remote := func(_ string, cookies []Cookie, _ func([]Cookie) error) (RemoteService, error) {
		return &fakeWatchingRemoteService{
			fakeRemoteService: &fakeRemoteService{configuration: managerClientConfiguration()},
			watch: func(ctx context.Context, _, _ string) (ConfigurationWatchResult, error) {
				if calls.Add(1) == 1 {
					updated := append([]Cookie(nil), cookies...)
					updated[0].Value = "renewed-by-relay"
					if err := store.MergeCookies("home-nas", cookies, updated); err != nil {
						return ConfigurationWatchResult{}, err
					}
					return ConfigurationWatchResult{}, model.NewError(model.ErrorAuthRequired, "old session rejected", false)
				}
				if len(cookies) == 1 && cookies[0].Value == "renewed-by-relay" {
					recovered <- struct{}{}
				}
				<-ctx.Done()
				return ConfigurationWatchResult{}, ctx.Err()
			},
		}, nil
	}
	manager := newRecoveryTestManager(t, store, &fakePrivilegedNetwork{}, remote)
	manager.setStatus(model.ClientLocal, "local", "", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); manager.watchConfiguration(ctx) }()
	select {
	case <-recovered:
	case <-time.After(3 * time.Second):
		cancel()
		<-done
		t.Fatal("watch did not retry with concurrently renewed credentials")
	}
	cancel()
	<-done
	if manager.Status().State == model.ClientAuthRequired {
		t.Fatal("stale authentication rejection disconnected the client")
	}
}

func TestWatchRecoversNativeSessionBeforeRequiringLogin(t *testing.T) {
	store := configuredManagerStore(t)
	secret := "c2VjcmV0"
	if err := store.SaveNativeSession(NativeSession{
		Version: nativeSessionVersion, FNID: "home-nas", Username: "admin",
		DeviceID: "stable-device", Token: "expired", LongToken: "long",
		Secret: secret, UpdatedAt: time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	authenticator := &fakeNativeAuthenticator{recovered: NativeSession{
		Version: nativeSessionVersion, FNID: "home-nas", Username: "admin",
		DeviceID: "stable-device", Token: "recovered", LongToken: "long",
		Secret: secret, UpdatedAt: time.Now(),
	}}
	var calls atomic.Int32
	retried := make(chan struct{}, 1)
	remote := func(_ string, cookies []Cookie, _ func([]Cookie) error) (RemoteService, error) {
		return &fakeWatchingRemoteService{
			fakeRemoteService: &fakeRemoteService{configuration: managerClientConfiguration()},
			watch: func(ctx context.Context, _, _ string) (ConfigurationWatchResult, error) {
				if calls.Add(1) == 1 {
					return ConfigurationWatchResult{}, model.NewError(
						model.ErrorAuthRequired, "expired gateway session", false,
					)
				}
				for _, cookie := range cookies {
					if cookie.Name == "fnos-token" && cookie.Value == "recovered" {
						retried <- struct{}{}
						break
					}
				}
				<-ctx.Done()
				return ConfigurationWatchResult{}, ctx.Err()
			},
		}, nil
	}
	manager, err := NewManager(ManagerOptions{
		Store: store, Discoverer: fakeDiscoverer{}, Remote: remote,
		Privileged: &fakePrivilegedNetwork{}, Bridge: &fakeBridge{},
		Probe: fakeLocalProbe{reachable: true}, Authenticator: authenticator,
	})
	if err != nil {
		t.Fatal(err)
	}
	manager.setStatus(model.ClientLocal, "local", "", nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); manager.watchConfiguration(ctx) }()
	select {
	case <-retried:
	case <-time.After(3 * time.Second):
		cancel()
		<-done
		t.Fatal("configuration watch did not retry with recovered native session")
	}
	cancel()
	<-done
	if authenticator.recoverCalls.Load() != 1 ||
		manager.Status().State == model.ClientAuthRequired {
		t.Fatal("native recovery did not preserve the connected state")
	}
}

func TestRelayAuthenticationFailureRecoversNativeSession(t *testing.T) {
	store := configuredManagerStore(t)
	secret := "c2VjcmV0"
	if err := store.SaveNativeSession(NativeSession{
		Version: nativeSessionVersion, FNID: "home-nas", Username: "admin",
		DeviceID: "stable-device", Token: "expired", LongToken: "long",
		Secret: secret, UpdatedAt: time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	authenticator := &fakeNativeAuthenticator{recovered: NativeSession{
		Version: nativeSessionVersion, FNID: "home-nas", Username: "admin",
		DeviceID: "stable-device", Token: "recovered", LongToken: "long",
		Secret: secret, UpdatedAt: time.Now(),
	}}
	bridge := &fakeBridge{connected: true, events: make(chan error, 1)}
	manager, err := NewManager(ManagerOptions{
		Store: store, Discoverer: fakeDiscoverer{},
		Remote: func(string, []Cookie, func([]Cookie) error) (RemoteService, error) {
			return &fakeRemoteService{configuration: managerClientConfiguration()}, nil
		},
		Privileged: &fakePrivilegedNetwork{}, Bridge: bridge,
		Probe: fakeLocalProbe{reachable: true}, Authenticator: authenticator,
	})
	if err != nil {
		t.Fatal(err)
	}
	manager.setStatus(model.ClientRelay, "fn-connect", "utun9", nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); manager.Run(ctx) }()
	bridge.events <- model.NewError(model.ErrorAuthRequired, "relay token expired", false)
	deadline := time.Now().Add(3 * time.Second)
	for manager.Status().State != model.ClientLocal && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	if authenticator.recoverCalls.Load() != 1 ||
		manager.Status().State != model.ClientLocal {
		t.Fatalf("relay recovery state=%s calls=%d",
			manager.Status().State, authenticator.recoverCalls.Load())
	}
}

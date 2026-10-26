package client

import (
	"context"
	"testing"
)

func TestManagerCloseIntentAndSingleCleanup(t *testing.T) {
	for _, pause := range []bool{false, true} {
		t.Run(map[bool]string{false: "preserve", true: "pause"}[pause], func(t *testing.T) {
			store := configuredManagerStore(t)
			config, err := store.Load()
			if err != nil {
				t.Fatal(err)
			}
			config.AutoConnect = true
			if err := store.Save(*config); err != nil {
				t.Fatal(err)
			}
			network := &fakePrivilegedNetwork{}
			m, err := NewManager(ManagerOptions{Store: store, Discoverer: fakeDiscoverer{},
				Remote:     func(string, []Cookie, func([]Cookie) error) (RemoteService, error) { return &fakeRemoteService{}, nil },
				Privileged: network, Bridge: &fakeBridge{}, Probe: fakeLocalProbe{}})
			if err != nil {
				t.Fatal(err)
			}
			if pause {
				err = m.ClosePaused(context.Background())
			} else {
				err = m.Close(context.Background())
			}
			if err != nil {
				t.Fatal(err)
			}
			current, err := store.Load()
			if err != nil {
				t.Fatal(err)
			}
			if current.AutoConnect == pause {
				t.Fatalf("pause=%v, saved AutoConnect=%v", pause, current.AutoConnect)
			}
			if current.DeviceID != config.DeviceID || current.FNID != config.FNID {
				t.Fatal("close changed identity")
			}
			if network.removed != 1 {
				t.Fatalf("cleanup count=%d", network.removed)
			}
		})
	}
}

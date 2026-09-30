package server

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/ipc"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

func TestServerPlanContainsOnlyEnabledDevices(t *testing.T) {
	state := model.ServerState{
		Settings: model.ServerSettings{
			OverlayCIDR: "10.203.0.0/24",
			ListenPort:  51820,
			LANCIDRs:    []string{"192.168.71.0/24"},
		},
		Devices: []model.Device{
			{
				ID:             "enabled",
				Name:           "MacBook",
				PublicKey:      testPublicKey(1),
				OverlayAddress: "10.203.0.2/32",
				Enabled:        true,
			},
			{
				ID:             "disabled",
				Name:           "Old Mac",
				PublicKey:      testPublicKey(2),
				OverlayAddress: "10.203.0.3/32",
				Enabled:        false,
			},
		},
	}
	plan, err := ServerPlan(state)
	if err != nil {
		t.Fatalf("server plan: %v", err)
	}
	if plan.ServerAddress != "10.203.0.1/24" ||
		len(plan.Peers) != 1 ||
		plan.Peers[0].PublicKey != state.Devices[0].PublicKey {
		t.Fatalf("plan = %+v", plan)
	}
	configuration, err := ClientConfiguration(
		state,
		state.Devices[0],
		model.ServerPrivilegedStatus{
			Active:    true,
			PublicKey: testPublicKey(9),
		},
	)
	if err != nil {
		t.Fatalf("client configuration: %v", err)
	}
	if configuration.DeviceID != state.Devices[0].ID ||
		configuration.ServerAddress != "10.203.0.1/24" {
		t.Fatalf("client configuration = %+v", configuration)
	}
}

func TestServerPlanRejectsOverlayNetworkAddress(t *testing.T) {
	state := model.ServerState{
		Settings: model.ServerSettings{
			OverlayCIDR: "10.203.0.0/24",
			ListenPort:  51820,
		},
		Devices: []model.Device{{
			ID:             "invalid",
			Name:           "MacBook",
			PublicKey:      testPublicKey(1),
			OverlayAddress: "10.203.0.0/32",
			Enabled:        true,
		}},
	}
	if _, err := ServerPlan(state); err == nil {
		t.Fatal("overlay network address unexpectedly accepted")
	}
}

func TestNetworkControllerAppliesPlan(t *testing.T) {
	socket := testNetworkControllerServer(t)
	controller := IPCNetwork{Client: ipc.Client{SocketPath: socket}}
	state := model.ServerState{
		Settings: model.ServerSettings{
			OverlayCIDR: "10.203.0.0/24",
			ListenPort:  51820,
		},
	}
	status, err := controller.Apply(context.Background(), state)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !status.Active ||
		status.PublicKey != testPublicKey(9) ||
		status.ListenPort != 51820 {
		t.Fatalf("status = %+v", status)
	}
}

func testNetworkControllerServer(t *testing.T) string {
	t.Helper()
	socket := fmt.Sprintf("/tmp/fncpn-network-%d.sock", os.Getpid())
	_ = os.Remove(socket)
	ctx, cancel := context.WithCancel(context.Background())
	server := ipc.Server{
		SocketPath: socket,
		Handler: ipc.HandlerFunc(func(
			_ context.Context,
			request ipc.Request,
		) ipc.Response {
			plan, err := ipc.DecodeParams[model.ServerPlan](request)
			if err != nil {
				return ipc.Failure(request.ID, err)
			}
			return ipc.Success(request.ID, model.ServerPrivilegedStatus{
				Active:     true,
				Interface:  "fncpn0",
				PublicKey:  testPublicKey(9),
				ListenPort: plan.ListenPort,
			})
		}),
	}
	errs := make(chan error, 1)
	go func() {
		errs <- server.Serve(ctx)
	}()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(socket); err == nil {
			t.Cleanup(func() {
				cancel()
				if err := <-errs; err != nil {
					t.Errorf("serve: %v", err)
				}
				_ = os.Remove(socket)
			})
			return socket
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	t.Fatal("network controller socket was not created")
	return ""
}

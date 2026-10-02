package command

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/client"
	"github.com/rectcircle/fn-connect-private-network/internal/ipc"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"github.com/rectcircle/fn-connect-private-network/internal/privileged"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

func TestClientHealthcheckDoesNotWaitForCredentials(t *testing.T) {
	// Short paths also fit Darwin's Unix socket path limit.
	directory, err := os.MkdirTemp("", "fncpn-start-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(directory)
	privilegedSocket := filepath.Join(directory, "priv.sock")
	clientSocket := filepath.Join(directory, "client.sock")
	processLogger := slog.New(slog.NewTextHandler(io.Discard, nil))
	privilegedContext, stopPrivileged := context.WithCancel(context.Background())
	privilegedDone := make(chan error, 1)
	credentialRequested := make(chan struct{}, 1)
	go func() {
		privilegedDone <- (ipc.Server{
			SocketPath: privilegedSocket,
			Logger:     processLogger,
			Handler: ipc.HandlerFunc(func(ctx context.Context, request ipc.Request) ipc.Response {
				switch request.Method {
				case privileged.MethodStatus:
					return ipc.Success(request.ID, privileged.ClientStatus{Available: true})
				case privileged.MethodRemove:
					return ipc.Success(request.ID, model.ClientPrivilegedStatus{})
				case privileged.MethodGetSecret:
					select {
					case credentialRequested <- struct{}{}:
					default:
					}
					<-ctx.Done()
					return ipc.Failure(request.ID, ctx.Err())
				default:
					return ipc.Failure(request.ID, model.NewError(model.ErrorInvalidArgument, "unexpected method", false))
				}
			}),
		}).Serve(privilegedContext)
	}()
	defer func() {
		stopPrivileged()
		if err := <-privilegedDone; err != nil {
			t.Errorf("privileged IPC: %v", err)
		}
	}()
	waitForHealthcheck(t, privilegedSocket, "client-privileged")

	key, err := wgtypes.GeneratePrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(directory, "config.json")
	if err := client.NewConfigStore(configPath, nil).Save(client.LocalConfig{
		FNID: "home-nas", DeviceID: "device-1", PublicKey: key.PublicKey().String(), AutoConnect: true,
		Configuration: model.ClientConfiguration{
			DeviceID: "device-1", ServerPublicKey: key.PublicKey().String(),
			ServerAddress: "10.203.0.1/24", ClientAddress: "10.203.0.2/32",
			ListenPort: 51820, AllowedIPs: []string{"10.203.0.1/32"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	ctx, stopClient := context.WithCancel(context.Background())
	clientDone := make(chan error, 1)
	go func() {
		clientDone <- runClientProcess(ctx, []string{
			"daemon", "--socket", clientSocket, "--privileged-socket", privilegedSocket,
			"--config", configPath, "--log-file", filepath.Join(directory, "client.log"),
		}, Environment{Stderr: io.Discard})
	}()
	defer func() {
		stopClient()
		select {
		case err := <-clientDone:
			if err != nil {
				t.Errorf("client shutdown: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Error("client did not cancel its blocked credential request")
		}
	}()
	select {
	case <-credentialRequested:
	case <-time.After(3 * time.Second):
		t.Fatal("automatic connection did not request credentials")
	}
	waitForHealthcheck(t, clientSocket, "client")
	var status model.ClientStatus
	if err := (ipc.Client{SocketPath: clientSocket, Timeout: time.Second}).Call(
		context.Background(), client.MethodStatus, nil, &status,
	); err != nil {
		t.Fatal(err)
	}
	if status.State != model.ClientProbing {
		t.Fatalf("status while credentials are blocked = %s", status.State)
	}
}

func waitForHealthcheck(t *testing.T, socket, role string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		err := runHealthcheck(ctx, []string{"--socket", socket, "--role", role})
		cancel()
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s healthcheck remained unavailable: %v", role, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

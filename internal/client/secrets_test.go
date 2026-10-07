package client

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/ipc"
	"github.com/rectcircle/fn-connect-private-network/internal/privileged"
)

func TestConfigStoreUsesPrivilegedFileCredentialsOverIPC(t *testing.T) {
	directory, err := os.MkdirTemp("", "fncpn-secret-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(directory)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	secrets, err := privileged.OpenSecretStore(filepath.Join(directory, "credentials"), logger)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	socket := filepath.Join(directory, "priv.sock")
	go func() {
		done <- (ipc.Server{
			SocketPath: socket,
			Authorize:  privileged.AuthorizePeer("client", os.Geteuid()),
			Handler:    privileged.NewClientService(privileged.NewUnavailableClientEngine("test"), secrets),
			Logger:     logger,
		}).Serve(ctx)
	}()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("serve credentials: %v", err)
		}
	}()
	transport := ipc.Client{SocketPath: socket, Timeout: time.Second}
	deadline := time.Now().Add(2 * time.Second)
	for {
		err := transport.Call(ctx, privileged.MethodStatus, nil, nil)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("privileged IPC was not ready: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	store := NewConfigStore(filepath.Join(directory, "config.json"), IPCSecretStore{Context: ctx, Client: transport})
	key, err := store.EnsureWireGuardKey("home-nas")
	if err != nil {
		t.Fatal(err)
	}
	again, err := store.EnsureWireGuardKey("home-nas")
	if err != nil || again != key {
		t.Fatalf("private key did not persist: %v", err)
	}
	cookies := []Cookie{{Name: "session", Value: "cookie-secret", Domain: "home-nas.fnos.net", Path: "/", Secure: true}}
	if err := store.SaveCookies("home-nas", cookies); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAdminWebSession(AdminWebSession{
		Version: adminWebSessionVersion, FNID: "home-nas", Username: "admin",
		DeviceID: "admin-web-device", Token: "web-short",
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	actual, err := store.LoadCookies("home-nas")
	if err != nil || !reflect.DeepEqual(actual, cookies) {
		t.Fatalf("cookies did not persist: %v", err)
	}
	if err := store.Forget("home-nas"); err != nil {
		t.Fatal(err)
	}
	if actual, err := store.LoadCookies("home-nas"); err != nil || len(actual) != 0 {
		t.Fatalf("cookies were retained after forget: %v", err)
	}
	if _, found, err := store.LoadAdminWebSession("home-nas"); err != nil || found {
		t.Fatalf("admin Web session was retained after forget: found=%v err=%v", found, err)
	}
	fresh, err := store.EnsureWireGuardKey("home-nas")
	if err != nil || fresh == key {
		t.Fatalf("private key was retained after forget: %v", err)
	}
}

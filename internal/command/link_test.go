//go:build !windows

package command

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/rectcircle/fn-connect-private-network/internal/client"
	"github.com/rectcircle/fn-connect-private-network/internal/ipc"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

func TestLinkCLIUsesSharedDaemonAndProtectsLocal(t *testing.T) {
	directory, err := os.MkdirTemp("", "fncpn-link-cli-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(directory)
	socket := filepath.Join(directory, "client.sock")
	var mu sync.Mutex
	settings := client.LinkSettings{FNID: "home-nas", Links: []client.LinkPreference{{Link: "ipv6", Enabled: true}, {Link: "fn-connect", Enabled: true}}}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- (ipc.Server{SocketPath: socket, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Handler: ipc.HandlerFunc(func(ctx context.Context, r ipc.Request) ipc.Response {
			mu.Lock()
			defer mu.Unlock()
			switch r.Method {
			case client.MethodStatus:
				return ipc.Success(r.ID, model.ClientStatus{})
			case client.MethodLinkSettings:
				return ipc.Success(r.ID, settings)
			case client.MethodSetLinkSettings:
				next, err := ipc.DecodeParams[client.LinkSettings](r)
				if err != nil {
					return ipc.Failure(r.ID, err)
				}
				settings = next
				return ipc.Success(r.ID, settings)
			default:
				return ipc.Failure(r.ID, model.NewError(model.ErrorInvalidArgument, "unexpected method", false))
			}
		})}).Serve(ctx)
	}()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	waitForHealthcheck(t, socket, "client")
	var output bytes.Buffer
	env := Environment{Stdout: &output, Stderr: io.Discard}
	for _, args := range [][]string{
		{"priority", "fn-connect", "ipv6", "--socket", socket, "--json"},
		{"disable", "ipv6", "--socket", socket},
	} {
		if err := runLinkCommand(t.Context(), args, env); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	if settings.Links[0].Link != "fn-connect" || settings.Links[1].Enabled {
		t.Fatal(settings)
	}
	mu.Unlock()
	if err := runLinkCommand(t.Context(), []string{"disable", "local", "--socket", socket}, env); err == nil {
		t.Fatal("CLI allowed disabling local")
	}
	if err := runLinkCommand(t.Context(), []string{"reset", "--socket", socket}, env); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if settings.Links[0].Link != "ipv6" || !settings.Links[1].Enabled {
		t.Fatal(settings)
	}
}

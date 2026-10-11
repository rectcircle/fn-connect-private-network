//go:build windows

package command

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/client"
	"github.com/rectcircle/fn-connect-private-network/internal/ipc"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"golang.org/x/sys/windows"
)

func TestDefaultClientPipeAuthorizerAllowsOnlySystemSessionZero(t *testing.T) {
	authorize := defaultClientPipeAuthorizer()
	if err := authorize(context.Background(), ipc.PeerIdentity{PID: 42, SID: "S-1-5-18", SessionID: 0}); err != nil {
		t.Fatalf("authorize LocalSystem maintenance caller: %v", err)
	}
	if err := authorize(context.Background(), ipc.PeerIdentity{PID: 42, SID: "S-1-5-18", SessionID: 1}); err == nil {
		t.Fatal("authorized LocalSystem peer outside session 0")
	}
	token := windows.GetCurrentProcessToken()
	user, err := token.GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	session, err := currentWindowsSession()
	if err != nil {
		t.Fatal(err)
	}
	if err := authorize(context.Background(), ipc.PeerIdentity{PID: 42, SID: user.User.Sid.String(), SessionID: session}); err != nil {
		t.Fatalf("authorize current user session: %v", err)
	}
	if err := authorize(context.Background(), ipc.PeerIdentity{PID: 42, SID: user.User.Sid.String(), SessionID: session + 1}); err == nil {
		t.Fatal("authorized the same user SID from another session")
	}
}

func TestInternalStopDaemonsRejectsArguments(t *testing.T) {
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"internal-stop-daemons", "--anything"}, Environment{Stderr: &stderr})
	if code != 2 {
		t.Fatalf("exit code = %d, want invalid-argument 2; stderr=%q", code, stderr.String())
	}
}

func TestClientPipeNameIncludesSessionID(t *testing.T) {
	if got, want := defaultClientSocketForSession(17), `\\.\pipe\FnCPN.Client.v1.17`; got != want {
		t.Fatalf("session pipe = %q, want %q", got, want)
	}
}

func TestConcurrentClientDaemonBootstrapConvergesOnNamedPipe(t *testing.T) {
	pipe := fmt.Sprintf(`\\.\pipe\fncpn-bootstrap-%d-%d`, windows.GetCurrentProcessId(), time.Now().UnixNano())
	startBarrier := make(chan struct{})
	var startCalls atomic.Int32
	var childMu sync.Mutex
	var children []*exec.Cmd
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	start := func() error {
		if startCalls.Add(1) == 2 {
			close(startBarrier)
		}
		<-startBarrier
		command := exec.Command(executable, "-test.run=^TestClientDaemonBootstrapHelperProcess$")
		command.Env = append(os.Environ(), "FNCPN_TEST_DAEMON_HELPER=1", "FNCPN_TEST_DAEMON_PIPE="+pipe)
		command.Stdout = io.Discard
		command.Stderr = io.Discard
		if err := command.Start(); err != nil {
			return err
		}
		childMu.Lock()
		children = append(children, command)
		childMu.Unlock()
		return nil
	}
	t.Cleanup(func() {
		childMu.Lock()
		startedChildren := append([]*exec.Cmd(nil), children...)
		childMu.Unlock()
		for _, child := range startedChildren {
			if child.ProcessState == nil {
				_ = child.Process.Kill()
			}
			_ = child.Wait()
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	results := make(chan error, 2)
	var callers sync.WaitGroup
	for range 2 {
		callers.Add(1)
		go func() {
			defer callers.Done()
			results <- ensureClientDaemonWithStarter(ctx, pipe, start)
		}()
	}
	callers.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatalf("concurrent daemon bootstrap failed: %v", err)
		}
	}
	if got := startCalls.Load(); got != 2 {
		t.Fatalf("daemon starter called %d times, want both racing callers to request startup", got)
	}
}

func TestClientDaemonBootstrapHelperProcess(t *testing.T) {
	if os.Getenv("FNCPN_TEST_DAEMON_HELPER") != "1" {
		return
	}
	pipe := os.Getenv("FNCPN_TEST_DAEMON_PIPE")
	err := (ipc.Server{
		SocketPath: pipe,
		Handler: ipc.HandlerFunc(func(_ context.Context, request ipc.Request) ipc.Response {
			if request.Method != client.MethodStatus {
				return ipc.Failure(request.ID, model.NewError(model.ErrorInvalidArgument, "unexpected method", false))
			}
			return ipc.Success(request.ID, model.ClientStatus{State: model.ClientUnconfigured})
		}),
	}).Serve(context.Background())
	if err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

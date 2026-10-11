//go:build windows

package ipc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

func TestInstalledLocalSystemServiceIdentity(t *testing.T) {
	if os.Getenv("FNCPN_TEST_INSTALLED_SERVICE") != "1" {
		t.Skip("requires installed FnCPNClient service")
	}
	timeout := 3 * time.Second
	connection, err := winio.DialPipe(`\\.\pipe\FnCPN.Service.v1`, &timeout)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if err := VerifyLocalSystemServiceServer("FnCPNClient")(connection); err != nil {
		t.Fatal(err)
	}
}

func TestNamedPipeRoundTripIdentifiesActualPeer(t *testing.T) {
	pipe := fmt.Sprintf(`\\.\pipe\fncpn-test-%d-%d`, os.Getpid(), time.Now().UnixNano())
	sidDescriptor, err := CurrentUserPipeSecurityDescriptor()
	if err != nil {
		t.Fatal(err)
	}
	identityReady := make(chan PeerIdentity, 1)
	listenerReady := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- (Server{
			SocketPath: pipe, PipeSecurityDescriptor: sidDescriptor,
			OnReady:   func() { close(listenerReady) },
			Authorize: func(_ context.Context, peer PeerIdentity) error { identityReady <- peer; return nil },
			Handler: HandlerFunc(func(_ context.Context, req Request) Response {
				return Success(req.ID, map[string]bool{"ready": true})
			}),
		}).Serve(ctx)
	}()
	select {
	case <-listenerReady:
	case <-time.After(5 * time.Second):
		t.Fatal("server did not report named pipe listener readiness")
	}
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("stop pipe server: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Error("pipe server did not stop")
		}
	})
	var callErr error
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		callErr = (Client{SocketPath: pipe, Timeout: time.Second}).Call(context.Background(), "test", nil, nil)
		if callErr == nil {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if callErr != nil {
		t.Fatalf("named pipe round trip: %v", callErr)
	}
	var identity PeerIdentity
	select {
	case identity = <-identityReady:
	case <-time.After(time.Second):
		t.Fatal("server did not observe the named pipe client identity")
	}
	if identity.PID != os.Getpid() || identity.SID == "" || !identity.Authenticated {
		t.Fatalf("peer identity = %+v, want current process %d with SID", identity, os.Getpid())
	}
	var expectedSession uint32
	if err := windows.ProcessIdToSessionId(uint32(os.Getpid()), &expectedSession); err != nil {
		t.Fatal(err)
	}
	if identity.SessionID != expectedSession {
		t.Fatalf("peer session = %d, want %d", identity.SessionID, expectedSession)
	}
}

func TestInteractiveSessionIDUsesActiveConsoleForSessionZero(t *testing.T) {
	tests := []struct {
		name          string
		process       uint32
		activeConsole uint32
		want          uint32
		wantErr       bool
	}{
		{name: "interactive process keeps its own session", process: 3, activeConsole: 7, want: 3},
		{name: "service resolves active console", process: 0, activeConsole: 7, want: 7},
		{name: "service rejects missing console", process: 0, activeConsole: 0xffffffff, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := interactiveSessionID(test.process, test.activeConsole)
			if (err != nil) != test.wantErr {
				t.Fatalf("interactiveSessionID() error = %v, wantErr %t", err, test.wantErr)
			}
			if err == nil && got != test.want {
				t.Fatalf("interactiveSessionID() = %d, want %d", got, test.want)
			}
		})
	}
}

func TestPipeACLAllowsOnlyGrantedPrincipals(t *testing.T) {
	token := windows.GetCurrentProcessToken()
	user, err := token.GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	userSID := user.User.Sid.String()
	userDescriptor := fmt.Sprintf("D:P(A;;GA;;;%s)(A;;GA;;;%s)", windowsSystemSID, userSID)
	for _, test := range []struct {
		name          string
		descriptor    string
		sid           string
		authenticated bool
		want          bool
	}{
		{name: "explicit user", descriptor: userDescriptor, sid: userSID, want: true},
		{name: "explicit LocalSystem", descriptor: userDescriptor, sid: windowsSystemSID, want: true},
		{name: "unlisted user", descriptor: userDescriptor, sid: "S-1-5-21-100-200-300-400", want: false},
		{name: "authenticated group permits authenticated peer", descriptor: InteractivePipeSecurityDescriptor(), sid: "S-1-5-21-100-200-300-400", authenticated: true, want: true},
		{name: "LocalSystem explicitly permitted by service pipe", descriptor: InteractivePipeSecurityDescriptor(), sid: windowsSystemSID, want: true},
		{name: "anonymous is not an authenticated peer", descriptor: InteractivePipeSecurityDescriptor(), sid: "S-1-5-7", want: false},
		{name: "deny ACE is not an allow grant", descriptor: "D:P(D;;GA;;;S-1-5-21-100-200-300-400)", sid: "S-1-5-21-100-200-300-400", want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := pipeACLAllowsSID(test.descriptor, test.sid, test.authenticated); got != test.want {
				t.Fatalf("pipeACLAllowsSID(%q, %q, %t) = %t, want %t", test.descriptor, test.sid, test.authenticated, got, test.want)
			}
		})
	}
	if pipeACLAllowsSID(InteractivePipeSecurityDescriptor(), "S-1-5-21-100-200-300-400", false) {
		t.Fatal("peer outside Authenticated Users passed the group ACE check")
	}
}

func TestTokenHasEnabledGroupRequiresEnabledNonDenyOnlyMembership(t *testing.T) {
	sid, err := windows.StringToSid(windowsAuthenticatedUsersSID)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name       string
		attributes uint32
		want       bool
	}{
		{name: "enabled", attributes: windows.SE_GROUP_ENABLED, want: true},
		{name: "disabled", attributes: windows.SE_GROUP_ENABLED_BY_DEFAULT, want: false},
		{name: "deny only", attributes: windows.SE_GROUP_ENABLED | windows.SE_GROUP_USE_FOR_DENY_ONLY, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			groups := &windows.Tokengroups{GroupCount: 1}
			groups.Groups[0] = windows.SIDAndAttributes{Sid: sid, Attributes: test.attributes}
			if got := tokenHasEnabledGroup(groups, windowsAuthenticatedUsersSID); got != test.want {
				t.Fatalf("tokenHasEnabledGroup() = %t, want %t", got, test.want)
			}
		})
	}
	if tokenHasEnabledGroup(nil, windowsAuthenticatedUsersSID) {
		t.Fatal("nil token groups were treated as authenticated")
	}
}

func TestAuthenticatedUserCanReachServicePipeForRequestAuthorization(t *testing.T) {
	pipe := fmt.Sprintf(`\\.\pipe\fncpn-service-user-test-%d-%d`, os.Getpid(), time.Now().UnixNano())
	identityReady := make(chan PeerIdentity, 1)
	listenerReady := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- (Server{
			SocketPath: pipe, PipeSecurityDescriptor: InteractivePipeSecurityDescriptor(),
			OnReady:   func() { close(listenerReady) },
			Authorize: func(_ context.Context, peer PeerIdentity) error { identityReady <- peer; return nil },
			Handler: HandlerFunc(func(_ context.Context, req Request) Response {
				return Success(req.ID, map[string]bool{"ready": true})
			}),
		}).Serve(ctx)
	}()
	select {
	case <-listenerReady:
	case <-time.After(5 * time.Second):
		t.Fatal("service listener did not report readiness")
	}
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("stop service pipe: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Error("service pipe did not stop")
		}
	})
	var callErr error
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		callErr = (Client{SocketPath: pipe, Timeout: time.Second}).Call(context.Background(), "test", nil, nil)
		if callErr == nil {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if callErr != nil {
		t.Fatalf("authenticated current user could not reach service authorization layer: %v", callErr)
	}
	token := windows.GetCurrentProcessToken()
	user, err := token.GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sessionID, err := currentSessionID()
	if err != nil {
		t.Fatal(err)
	}
	select {
	case peer := <-identityReady:
		if peer.SID != user.User.Sid.String() || peer.SessionID != sessionID {
			t.Fatalf("service authorizer received peer %+v, want current user SID %s/session %d", peer, user.User.Sid.String(), sessionID)
		}
	case <-time.After(time.Second):
		t.Fatal("service authorizer did not receive the authenticated peer")
	}
}

func TestServerShutdownBoundsStalledPipeListenerClose(t *testing.T) {
	listener := &stalledCloseListener{
		acceptStarted: make(chan struct{}),
		closeStarted:  make(chan struct{}),
		release:       make(chan struct{}),
	}
	t.Cleanup(listener.unblock)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- (Server{
			SocketPath:             fmt.Sprintf(`\\.\pipe\fncpn-close-stall-%d-%d`, os.Getpid(), time.Now().UnixNano()),
			PipeSecurityDescriptor: "D:P(A;;GA;;;S-1-5-18)(A;;GA;;;S-1-5-21-1-2-3-1000)",
			listenPipe: func(string, *winio.PipeConfig) (net.Listener, error) {
				return listener, nil
			},
			Handler: HandlerFunc(func(context.Context, Request) Response { return Response{} }),
		}).Serve(ctx)
	}()
	select {
	case <-listener.acceptStarted:
	case <-time.After(time.Second):
		t.Fatal("server did not start accepting")
	}
	cancel()
	select {
	case <-listener.closeStarted:
	case <-time.After(time.Second):
		t.Fatal("server did not begin closing the listener")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("server shutdown: %v", err)
		}
	case <-time.After(listenerCloseTimeout + time.Second):
		t.Fatal("server shutdown waited indefinitely for the pipe listener")
	}
}

func TestWinioListenerCloseRacesPendingConnect(t *testing.T) {
	// Regression coverage for go-winio's makeConnectedServerPipe/Close race.
	// Use a per-iteration pipe name so this remains safe when different target
	// architectures run the package concurrently on the same Windows host.
	securityDescriptor, err := CurrentUserPipeSecurityDescriptor()
	if err != nil {
		t.Fatal(err)
	}
	for iteration := range 100 {
		pipe := fmt.Sprintf(`\\.\pipe\fncpn-winio-close-race-%d-%d`, os.Getpid(), time.Now().UnixNano())
		listener, err := winio.ListenPipe(pipe, &winio.PipeConfig{SecurityDescriptor: securityDescriptor})
		if err != nil {
			t.Fatalf("iteration %d: listen: %v", iteration, err)
		}
		acceptResult := make(chan error, 1)
		go func() {
			for {
				connection, acceptErr := listener.Accept()
				if acceptErr != nil {
					acceptResult <- acceptErr
					return
				}
				_ = connection.Close()
			}
		}()
		dialDone := make(chan struct{})
		go func() {
			defer close(dialDone)
			timeout := 250 * time.Millisecond
			connection, dialErr := winio.DialPipe(pipe, &timeout)
			if dialErr == nil {
				_ = connection.Close()
			}
		}()
		time.Sleep(time.Duration(iteration%5) * 100 * time.Microsecond)
		closeDone := make(chan struct{})
		go func() {
			_ = listener.Close()
			close(closeDone)
		}()
		select {
		case <-closeDone:
		case <-time.After(2 * time.Second):
			t.Fatalf("iteration %d: go-winio listener Close stalled", iteration)
		}
		select {
		case acceptErr := <-acceptResult:
			if !errors.Is(acceptErr, net.ErrClosed) {
				t.Errorf("iteration %d: Accept error = %v, want net.ErrClosed", iteration, acceptErr)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("iteration %d: Accept did not return after listener Close", iteration)
		}
		select {
		case <-dialDone:
		case <-time.After(2 * time.Second):
			t.Fatalf("iteration %d: pipe client dial did not finish", iteration)
		}
	}
}

type stalledCloseListener struct {
	acceptStarted chan struct{}
	closeStarted  chan struct{}
	release       chan struct{}
	unblockOnce   sync.Once
}

func (l *stalledCloseListener) Accept() (net.Conn, error) {
	select {
	case <-l.acceptStarted:
	default:
		close(l.acceptStarted)
	}
	<-l.release
	return nil, net.ErrClosed
}

func (l *stalledCloseListener) Close() error {
	close(l.closeStarted)
	<-l.release
	return nil
}

func (l *stalledCloseListener) Addr() net.Addr { return &net.TCPAddr{} }

func (l *stalledCloseListener) unblock() {
	l.unblockOnce.Do(func() { close(l.release) })
}

func TestLocalSystemServicePeerRequiresSCMProcessIdentity(t *testing.T) {
	valid := PeerIdentity{PID: 451, SID: windowsSystemSID, SessionID: 0}
	if err := validateLocalSystemServicePeer(valid, 451, windows.SERVICE_RUNNING); err != nil {
		t.Fatalf("valid LocalSystem service peer rejected: %v", err)
	}
	for _, test := range []struct {
		name      string
		identity  PeerIdentity
		serviceID uint32
		state     uint32
	}{
		{name: "different process", identity: valid, serviceID: 452, state: windows.SERVICE_RUNNING},
		{name: "not system", identity: PeerIdentity{PID: 451, SID: "S-1-5-21-100", SessionID: 0}, serviceID: 451, state: windows.SERVICE_RUNNING},
		{name: "interactive session", identity: PeerIdentity{PID: 451, SID: windowsSystemSID, SessionID: 1}, serviceID: 451, state: windows.SERVICE_RUNNING},
		{name: "service not running", identity: valid, serviceID: 451, state: windows.SERVICE_STOPPED},
		{name: "unknown service process", identity: valid, serviceID: 0, state: windows.SERVICE_RUNNING},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := validateLocalSystemServicePeer(test.identity, test.serviceID, test.state); err == nil {
				t.Fatal("untrusted named pipe server was accepted")
			}
		})
	}
}

func TestStandardUserCanReadLocalSystemServiceProcessFromSCM(t *testing.T) {
	pid, state, err := localSystemServiceProcess("EventLog")
	if err != nil {
		t.Fatalf("query EventLog with read-only SCM access: %v", err)
	}
	if pid == 0 || state != windows.SERVICE_RUNNING {
		t.Fatalf("EventLog SCM status = pid %d, state %d; want running process", pid, state)
	}
}

func TestNamedPipeClientRejectsNonServiceServerBeforeSendingRequest(t *testing.T) {
	pipe := fmt.Sprintf(`\\.\pipe\fncpn-spoof-test-%d-%d`, os.Getpid(), time.Now().UnixNano())
	descriptor, err := CurrentUserPipeSecurityDescriptor()
	if err != nil {
		t.Fatal(err)
	}
	listener, err := winio.ListenPipe(pipe, &winio.PipeConfig{SecurityDescriptor: descriptor})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	requestRead := make(chan error, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			requestRead <- acceptErr
			return
		}
		defer connection.Close()
		_, readErr := ReadRequest(connection)
		requestRead <- readErr
	}()
	client := Client{
		SocketPath:   pipe,
		Timeout:      time.Second,
		VerifyServer: VerifyLocalSystemServiceServer("FnCPN-Test-Service-Does-Not-Exist"),
	}
	if err := client.Call(context.Background(), "test", nil, nil); err == nil {
		t.Fatal("accepted a user-owned named pipe as the privileged service")
	}
	select {
	case err := <-requestRead:
		if err == nil {
			t.Fatal("client sent an IPC request before rejecting the pipe server")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("fake pipe server did not observe the connection")
	}
}

func TestNamedPipeServiceListenerRejectsPreexistingName(t *testing.T) {
	pipe := fmt.Sprintf(`\\.\pipe\fncpn-preempt-test-%d-%d`, os.Getpid(), time.Now().UnixNano())
	descriptor, err := CurrentUserPipeSecurityDescriptor()
	if err != nil {
		t.Fatal(err)
	}
	preexisting, err := winio.ListenPipe(pipe, &winio.PipeConfig{SecurityDescriptor: descriptor})
	if err != nil {
		t.Fatal(err)
	}
	defer preexisting.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err = (Server{
		SocketPath: pipe, PipeSecurityDescriptor: descriptor,
		Handler: HandlerFunc(func(context.Context, Request) Response { return Response{} }),
	}).Serve(ctx)
	if err == nil {
		t.Fatal("service listener accepted a preexisting named pipe")
	}
}

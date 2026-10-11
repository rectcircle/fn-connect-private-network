//go:build windows

package ipc

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"unsafe"

	"github.com/Microsoft/go-winio"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc/mgr"
)

const windowsSystemSID = "S-1-5-18"
const windowsAuthenticatedUsersSID = "S-1-5-11"

func IdentifyPeer(connection net.Conn) (PeerIdentity, error) {
	pipe, ok := connection.(winio.PipeConn)
	if !ok {
		return PeerIdentity{}, errors.New("connection is not a Windows named pipe")
	}
	handle, err := pipeHandle(pipe)
	if err != nil {
		return PeerIdentity{}, err
	}
	var pid uint32
	if err := windows.GetNamedPipeClientProcessId(handle, &pid); err != nil {
		return PeerIdentity{}, fmt.Errorf("identify named pipe client process: %w", err)
	}
	return identifyWindowsProcess(pid)
}

func IdentifyServerPeer(connection net.Conn) (PeerIdentity, error) {
	pipe, ok := connection.(winio.PipeConn)
	if !ok {
		return PeerIdentity{}, errors.New("connection is not a Windows named pipe")
	}
	handle, err := pipeHandle(pipe)
	if err != nil {
		return PeerIdentity{}, err
	}
	var pid uint32
	if err := windows.GetNamedPipeServerProcessId(handle, &pid); err != nil {
		return PeerIdentity{}, fmt.Errorf("identify named pipe server process: %w", err)
	}
	return identifyWindowsProcess(pid)
}

func identifyWindowsProcess(pid uint32) (PeerIdentity, error) {
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return PeerIdentity{}, fmt.Errorf("open named pipe client process: %w", err)
	}
	defer windows.CloseHandle(process)
	var token windows.Token
	if err := windows.OpenProcessToken(process, windows.TOKEN_QUERY, &token); err != nil {
		return PeerIdentity{}, fmt.Errorf("open named pipe client token: %w", err)
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return PeerIdentity{}, fmt.Errorf("read named pipe client SID: %w", err)
	}
	groups, err := token.GetTokenGroups()
	if err != nil {
		return PeerIdentity{}, fmt.Errorf("read named pipe client token groups: %w", err)
	}
	authenticated := tokenHasEnabledGroup(groups, windowsAuthenticatedUsersSID)
	var sessionID uint32
	if err := windows.ProcessIdToSessionId(pid, &sessionID); err != nil {
		return PeerIdentity{}, fmt.Errorf("read named pipe client session: %w", err)
	}
	return PeerIdentity{PID: int(pid), SID: user.User.Sid.String(), SessionID: sessionID, Authenticated: authenticated}, nil
}

func tokenHasEnabledGroup(groups *windows.Tokengroups, expectedSID string) bool {
	if groups == nil {
		return false
	}
	for _, group := range groups.AllGroups() {
		if group.Sid == nil || group.Sid.String() != expectedSID {
			continue
		}
		return group.Attributes&windows.SE_GROUP_ENABLED != 0 &&
			group.Attributes&windows.SE_GROUP_USE_FOR_DENY_ONLY == 0
	}
	return false
}

// VerifyLocalSystemServiceServer returns a verifier for a named-pipe connection
// that must terminate in the currently running SCM-managed LocalSystem service.
// This prevents a same-name pipe created before the service from impersonating
// the privileged endpoint to an unprivileged client daemon.
func VerifyLocalSystemServiceServer(serviceName string) func(net.Conn) error {
	return func(connection net.Conn) error {
		pipe, ok := connection.(winio.PipeConn)
		if !ok {
			return errors.New("connection is not a Windows named pipe")
		}
		handle, err := pipeHandle(pipe)
		if err != nil {
			return model.WrapError(model.ErrorPermissionDenied, "named pipe server identity could not be verified", false, err)
		}
		var pid uint32
		if err := windows.GetNamedPipeServerProcessId(handle, &pid); err != nil {
			return err
		}
		// Standard users cannot open the SYSTEM process/token. SCM is the
		// authoritative source for the service account and running process;
		// bind it to the kernel-reported pipe PID instead. SCM-managed
		// own-process LocalSystem services run in session zero.
		manager, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
		if err != nil {
			return err
		}
		defer windows.CloseServiceHandle(manager)
		name, err := windows.UTF16PtrFromString(serviceName)
		if err != nil {
			return err
		}
		service, err := windows.OpenService(manager, name, windows.SERVICE_QUERY_CONFIG)
		if err != nil {
			return err
		}
		defer windows.CloseServiceHandle(service)
		config, err := (&mgr.Service{Name: serviceName, Handle: service}).Config()
		if err != nil {
			return err
		}
		if config.ServiceType != windows.SERVICE_WIN32_OWN_PROCESS || !strings.EqualFold(config.ServiceStartName, "LocalSystem") {
			return model.NewError(model.ErrorPermissionDenied, "named pipe service is not an own-process LocalSystem service", false)
		}
		servicePID, state, err := localSystemServiceProcess(serviceName)
		if err != nil {
			return model.WrapError(model.ErrorUnavailable, "Windows privileged service could not be verified", true, err)
		}
		return validateLocalSystemServicePeer(PeerIdentity{PID: int(pid), SID: windowsSystemSID, SessionID: 0}, servicePID, state)
	}
}

func validateLocalSystemServicePeer(identity PeerIdentity, servicePID, state uint32) error {
	if state != windows.SERVICE_RUNNING || servicePID == 0 {
		return model.WrapError(model.ErrorUnavailable, "Windows privileged service is not running", true,
			fmt.Errorf("SCM service state %d, process ID %d", state, servicePID))
	}
	if identity.PID <= 0 || uint32(identity.PID) != servicePID {
		return model.WrapError(model.ErrorPermissionDenied, "named pipe server does not match the SCM service process", false,
			fmt.Errorf("pipe server PID %d, SCM service PID %d", identity.PID, servicePID))
	}
	if identity.SID != windowsSystemSID || identity.SessionID != 0 {
		return model.WrapError(model.ErrorPermissionDenied, "named pipe server is not the LocalSystem service", false,
			fmt.Errorf("server SID %s, session %d", identity.SID, identity.SessionID))
	}
	return nil
}

func localSystemServiceProcess(serviceName string) (uint32, uint32, error) {
	manager, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return 0, 0, fmt.Errorf("open Service Control Manager: %w", err)
	}
	defer windows.CloseServiceHandle(manager)
	name, err := windows.UTF16PtrFromString(serviceName)
	if err != nil {
		return 0, 0, err
	}
	service, err := windows.OpenService(manager, name, windows.SERVICE_QUERY_STATUS)
	if err != nil {
		return 0, 0, fmt.Errorf("open Windows service %q: %w", serviceName, err)
	}
	defer windows.CloseServiceHandle(service)
	var status windows.SERVICE_STATUS_PROCESS
	var needed uint32
	err = windows.QueryServiceStatusEx(
		service,
		windows.SC_STATUS_PROCESS_INFO,
		(*byte)(unsafe.Pointer(&status)),
		uint32(unsafe.Sizeof(status)),
		&needed,
	)
	if err != nil {
		return 0, 0, fmt.Errorf("query Windows service %q process: %w", serviceName, err)
	}
	return status.ProcessId, status.CurrentState, nil
}

func pipeHandle(connection winio.PipeConn) (windows.Handle, error) {
	// go-winio's concrete pipe connections expose the public Fd method.
	// The handle is borrowed: the connection retains ownership.
	accessor, ok := connection.(interface{ Fd() uintptr })
	if !ok {
		return 0, errors.New("Windows named pipe does not expose a handle")
	}
	handle := windows.Handle(accessor.Fd())
	if handle == 0 || handle == windows.InvalidHandle {
		return 0, errors.New("Windows named pipe handle is invalid")
	}
	return handle, nil
}

func CurrentUserPipeSecurityDescriptor() (string, error) {
	token, err := pipeUserToken()
	if err != nil {
		return "", err
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("D:P(A;;GA;;;%s)(A;;GA;;;%s)", windowsSystemSID, user.User.Sid.String()), nil
}

// InteractivePipeSecurityDescriptor permits a local authenticated process to
// reach the service pipe. The service verifies the actual peer SID and session
// on every one-request connection and only authorizes the active console user.
func InteractivePipeSecurityDescriptor() string {
	return fmt.Sprintf("D:P(A;;GA;;;%s)(A;;GA;;;%s)", windowsSystemSID, windowsAuthenticatedUsersSID)
}

func pipeACLAllowsSID(descriptor, peerSID string, authenticated bool) bool {
	for _, ace := range strings.Split(descriptor, ")") {
		start := strings.LastIndex(ace, "(")
		if start < 0 {
			continue
		}
		ace = strings.TrimSpace(ace[start+1:])
		fields := strings.Split(ace, ";")
		if len(fields) != 6 || fields[0] != "A" {
			continue
		}
		trustee := fields[5]
		if trustee == peerSID {
			return true
		}
		// The service endpoint deliberately grants access to Authenticated
		// Users, then applies the stricter active-console SID/session policy
		// using the actual peer token for every request. Require token group
		// membership here instead of inferring it from the user SID.
		if trustee == windowsAuthenticatedUsersSID && peerSID != "" && authenticated {
			return true
		}
	}
	return false
}

func pipeUserToken() (windows.Token, error) {
	current := windows.GetCurrentProcessToken()
	currentUser, err := current.GetTokenUser()
	if err != nil {
		return 0, err
	}
	if currentUser.User.Sid.String() != windowsSystemSID {
		return windows.OpenCurrentProcessToken()
	}
	sessionID, err := currentSessionID()
	if err != nil {
		return 0, err
	}
	sessionID, err = interactiveSessionID(sessionID, windows.WTSGetActiveConsoleSessionId())
	if err != nil {
		return 0, err
	}
	var token windows.Token
	if err := windows.WTSQueryUserToken(sessionID, &token); err != nil {
		return 0, fmt.Errorf("query current console user token: %w", err)
	}
	return token, nil
}

func currentSessionID() (uint32, error) {
	var sessionID uint32
	if err := windows.ProcessIdToSessionId(uint32(os.Getpid()), &sessionID); err != nil {
		// LocalSystem services run in session 0; resolve their authorized user
		// against the active physical console session.
		active := windows.WTSGetActiveConsoleSessionId()
		if active == 0xffffffff {
			return 0, err
		}
		return active, nil
	}
	return sessionID, nil
}

func interactiveSessionID(processSession, activeConsoleSession uint32) (uint32, error) {
	if processSession != 0 {
		return processSession, nil
	}
	if activeConsoleSession == 0xffffffff {
		return 0, errors.New("no active local console session")
	}
	return activeConsoleSession, nil
}

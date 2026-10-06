package model

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"syscall"
	"time"
)

const (
	ProtocolVersion  = 4
	LocalProbeDomain = "fncpn-local-probe-v1\x00"
)

type ErrorCode string

const (
	ErrorInvalidArgument    ErrorCode = "INVALID_ARGUMENT"
	ErrorAuthRequired       ErrorCode = "AUTH_REQUIRED"
	ErrorPermissionDenied   ErrorCode = "PERMISSION_DENIED"
	ErrorNotFound           ErrorCode = "NOT_FOUND"
	ErrorAlreadyExists      ErrorCode = "ALREADY_EXISTS"
	ErrorFailedPrecondition ErrorCode = "FAILED_PRECONDITION"
	ErrorDeviceRevoked      ErrorCode = "DEVICE_REVOKED"
	ErrorUnavailable        ErrorCode = "UNAVAILABLE"
	ErrorConflict           ErrorCode = "CONFLICT"
	ErrorTimeout            ErrorCode = "TIMEOUT"
	ErrorCanceled           ErrorCode = "CANCELED"
	ErrorInternal           ErrorCode = "INTERNAL"
	ErrorProtocol           ErrorCode = "PROTOCOL_ERROR"
	ErrorResourceExhausted  ErrorCode = "RESOURCE_EXHAUSTED"
	ErrorDiscoveryFailed    ErrorCode = "DISCOVERY_FAILED"
)

type Error struct {
	Code       ErrorCode `json:"code"`
	Message    string    `json:"message"`
	Retryable  bool      `json:"retryable"`
	Operation  string    `json:"operation,omitempty"`
	Detail     string    `json:"detail,omitempty"`
	HTTPStatus int       `json:"httpStatus,omitempty"`
	RemoteCode string    `json:"remoteCode,omitempty"`
	RequestID  string    `json:"requestId,omitempty"`
	Cause      error     `json:"-"`
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func NewError(code ErrorCode, message string, retryable bool) *Error {
	return &Error{Code: code, Message: message, Retryable: retryable}
}

func WrapError(code ErrorCode, message string, retryable bool, cause error) *Error {
	return &Error{Code: code, Message: message, Retryable: retryable, Cause: cause}
}

func AsError(err error) *Error {
	return NormalizeError(err, ErrorInternal, "internal error", false)
}

func NormalizeError(
	err error,
	fallbackCode ErrorCode,
	fallbackMessage string,
	fallbackRetryable bool,
) *Error {
	if err == nil {
		return nil
	}
	var typed *Error
	if errors.As(err, &typed) {
		if err != typed {
			result := *typed
			result.Cause = err
			return &result
		}
		return typed
	}
	if errors.Is(err, context.Canceled) {
		return WrapError(ErrorCanceled, "operation canceled", false, err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return WrapError(ErrorTimeout, "operation timed out", true, err)
	}
	if errors.Is(err, os.ErrPermission) {
		return WrapError(ErrorPermissionDenied, "system permission denied", false, err)
	}
	if errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.EMFILE) || errors.Is(err, syscall.ENFILE) {
		return WrapError(ErrorResourceExhausted, "system resource limit reached", false, err)
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return WrapError(ErrorTimeout, "operation timed out", true, err)
	}
	return WrapError(fallbackCode, fallbackMessage, fallbackRetryable, err)
}

func PublicError(err error) *Error {
	typed := AsError(err)
	if typed == nil {
		return nil
	}
	result := *typed
	result.Message = SafeText(typed.Message)
	result.Detail = SafeText(typed.Detail)
	if typed.Cause != nil {
		result.Detail = joinDetail(result.Detail, ErrorDetail(typed.Cause))
	}
	result.Operation = SafeText(typed.Operation)
	result.RemoteCode = SafeText(typed.RemoteCode)
	result.RequestID = SafeRequestID(typed.RequestID)
	result.Cause = nil
	return &result
}

type ClientState string

const (
	ClientUnconfigured ClientState = "UNCONFIGURED"
	ClientAuthorizing  ClientState = "AUTHORIZING"
	ClientProbing      ClientState = "PROBING"
	ClientLocal        ClientState = "LOCAL"
	ClientDirect       ClientState = "DIRECT"
	ClientRelay        ClientState = "RELAY"
	ClientReconnecting ClientState = "RECONNECTING"
	ClientAuthRequired ClientState = "AUTH_REQUIRED"
	ClientPaused       ClientState = "PAUSED"
	ClientError        ClientState = "ERROR"
)

type ClientStatus struct {
	State         ClientState `json:"state"`
	Path          string      `json:"path,omitempty"`
	Interface     string      `json:"interface,omitempty"`
	MTU           int         `json:"mtu,omitempty"`
	LastHandshake *time.Time  `json:"lastHandshake,omitempty"`
	LastError     *Error      `json:"lastError,omitempty"`
	UpdatedAt     time.Time   `json:"updatedAt"`
}

type ClientDiagnostics struct {
	Status              ClientStatus      `json:"status"`
	FNID                string            `json:"fnId,omitempty"`
	Username            string            `json:"username,omitempty"`
	ClientAddress       string            `json:"clientAddress,omitempty"`
	Direct              DirectDiagnostics `json:"direct"`
	Errors              []*Error          `json:"errors,omitempty"`
	PrivilegedAvailable bool              `json:"privilegedAvailable"`
	PrivilegedActive    bool              `json:"privilegedActive"`
	PrivilegedDegraded  bool              `json:"privilegedDegraded"`
	NetworkInterface    string            `json:"networkInterface,omitempty"`
	NetworkFingerprint  string            `json:"networkFingerprint,omitempty"`
	HandshakeAge        string            `json:"handshakeAge,omitempty"`
	MTU                 int               `json:"mtu,omitempty"`
	LANOverlap          bool              `json:"lanOverlap"`
	RoutePolicy         string            `json:"routePolicy,omitempty"`
	CheckedAt           time.Time         `json:"checkedAt"`
}

type DirectDiagnostics struct {
	LocalPublicIPv6 bool       `json:"localPublicIPv6"`
	CandidateCount  int        `json:"candidateCount"`
	AttemptCount    int        `json:"attemptCount"`
	Endpoint        string     `json:"endpoint,omitempty"`
	Reason          string     `json:"reason,omitempty"`
	RetryAfter      *time.Time `json:"retryAfter,omitempty"`
	LastError       *Error     `json:"lastError,omitempty"`
}

// EqualTo reports whether the observable diagnostic fields match another value.
// Transient fields (RetryAfter) are ignored so the UI only refreshes on a real change.
func (d DirectDiagnostics) EqualTo(other DirectDiagnostics) bool {
	if d.LocalPublicIPv6 != other.LocalPublicIPv6 ||
		d.CandidateCount != other.CandidateCount ||
		d.AttemptCount != other.AttemptCount ||
		d.Endpoint != other.Endpoint ||
		d.Reason != other.Reason {
		return false
	}
	return errorsEqual(d.LastError, other.LastError)
}

func errorsEqual(first, second *Error) bool {
	if first == nil || second == nil {
		return first == second
	}
	return first.Code == second.Code &&
		first.Message == second.Message &&
		first.Operation == second.Operation &&
		first.Detail == second.Detail
}

type NetworkChange struct {
	PrimaryNetworkChanged bool
}

type LocalProbeConfiguration struct {
	Endpoints []string `json:"endpoints"`
	Key       string   `json:"key"`
}

type LocalProbeRequest struct {
	DeviceID string `json:"deviceId"`
	Nonce    string `json:"nonce"`
}

type LocalProbeResponse struct {
	Proof string `json:"proof"`
}

type ServerSettings struct {
	OverlayCIDR string   `json:"overlayCIDR"`
	ListenPort  uint16   `json:"listenPort"`
	LANCIDRs    []string `json:"lanCIDRs"`
}

type Device struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	PublicKey      string     `json:"publicKey"`
	OverlayAddress string     `json:"overlayAddress"`
	Enabled        bool       `json:"enabled"`
	CreatedAt      time.Time  `json:"createdAt"`
	UpdatedAt      time.Time  `json:"updatedAt"`
	LastHandshake  *time.Time `json:"lastHandshake,omitempty"`
	ReceiveBytes   uint64     `json:"receiveBytes"`
	TransmitBytes  uint64     `json:"transmitBytes"`
}

type ServerState struct {
	Settings ServerSettings `json:"settings"`
	Devices  []Device       `json:"devices"`
}

type ClientPrivilegedStatus struct {
	LastError     *Error     `json:"lastError,omitempty"`
	Active        bool       `json:"active"`
	Degraded      bool       `json:"degraded"`
	Interface     string     `json:"interface,omitempty"`
	ListenPort    uint16     `json:"listenPort,omitempty"`
	MTU           int        `json:"mtu,omitempty"`
	LastHandshake *time.Time `json:"lastHandshake,omitempty"`
}

type ServerPrivilegedStatus struct {
	LastError  *Error                 `json:"lastError,omitempty"`
	Active     bool                   `json:"active"`
	Degraded   bool                   `json:"degraded"`
	Interface  string                 `json:"interface,omitempty"`
	PublicKey  string                 `json:"publicKey,omitempty"`
	ListenPort uint16                 `json:"listenPort,omitempty"`
	Peers      []PrivilegedPeerStatus `json:"peers,omitempty"`
}

type ServerNetworkSnapshot struct {
	Network     ServerPrivilegedStatus `json:"network"`
	Fresh       bool                   `json:"fresh"`
	RefreshedAt time.Time              `json:"refreshedAt,omitempty"`
	LastError   *Error                 `json:"lastError,omitempty"`
}

type PrivilegedPeerStatus struct {
	PublicKey     string     `json:"publicKey"`
	LastHandshake *time.Time `json:"lastHandshake,omitempty"`
	ReceiveBytes  uint64     `json:"receiveBytes"`
	TransmitBytes uint64     `json:"transmitBytes"`
}

type ClientPlan struct {
	Mode            string   `json:"mode"`
	PrivateKey      string   `json:"privateKey"`
	ClientAddress   string   `json:"clientAddress"`
	ServerPublicKey string   `json:"serverPublicKey"`
	Endpoint        string   `json:"endpoint"`
	AllowedIPs      []string `json:"allowedIPs"`
	MTU             int      `json:"mtu"`
}

type ClientConfiguration struct {
	DeviceID        string   `json:"deviceId"`
	ServerPublicKey string   `json:"serverPublicKey"`
	ServerAddress   string   `json:"serverAddress"`
	ClientAddress   string   `json:"clientAddress"`
	ListenPort      uint16   `json:"listenPort"`
	AllowedIPs      []string `json:"allowedIPs"`
}

type DeviceRegistration struct {
	Device        Device              `json:"device"`
	Configuration ClientConfiguration `json:"configuration"`
}

type ServerPlan struct {
	OverlayCIDR   string   `json:"overlayCIDR"`
	ServerAddress string   `json:"serverAddress"`
	ListenPort    uint16   `json:"listenPort"`
	LANCIDRs      []string `json:"lanCIDRs"`
	Peers         []Peer   `json:"peers"`
}

type Peer struct {
	PublicKey string `json:"publicKey"`
	Address   string `json:"address"`
}

func DecodeStrict(data []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("multiple JSON values")
	}
	return nil
}

func ValidateProtocolVersion(version int) error {
	if version != ProtocolVersion {
		return NewError(
			ErrorProtocol,
			fmt.Sprintf("unsupported protocol version %d", version),
			false,
		)
	}
	return nil
}

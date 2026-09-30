package model

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
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
)

type Error struct {
	Code      ErrorCode `json:"code"`
	Message   string    `json:"message"`
	Retryable bool      `json:"retryable"`
	Cause     error     `json:"-"`
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
		return typed
	}
	if errors.Is(err, context.Canceled) {
		return WrapError(ErrorCanceled, "operation canceled", false, err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return WrapError(ErrorTimeout, "operation timed out", true, err)
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
	return &Error{
		Code:      typed.Code,
		Message:   typed.Message,
		Retryable: typed.Retryable,
	}
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
	Status              ClientStatus `json:"status"`
	PrivilegedAvailable bool         `json:"privilegedAvailable"`
	PrivilegedActive    bool         `json:"privilegedActive"`
	PrivilegedDegraded  bool         `json:"privilegedDegraded"`
	NetworkInterface    string       `json:"networkInterface,omitempty"`
	NetworkFingerprint  string       `json:"networkFingerprint,omitempty"`
	HandshakeAge        string       `json:"handshakeAge,omitempty"`
	MTU                 int          `json:"mtu,omitempty"`
	LANOverlap          bool         `json:"lanOverlap"`
	RoutePolicy         string       `json:"routePolicy,omitempty"`
	CheckedAt           time.Time    `json:"checkedAt"`
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
	Active        bool       `json:"active"`
	Degraded      bool       `json:"degraded"`
	Interface     string     `json:"interface,omitempty"`
	ListenPort    uint16     `json:"listenPort,omitempty"`
	MTU           int        `json:"mtu,omitempty"`
	LastHandshake *time.Time `json:"lastHandshake,omitempty"`
}

type ServerPrivilegedStatus struct {
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
			ErrorInvalidArgument,
			fmt.Sprintf("unsupported protocol version %d", version),
			false,
		)
	}
	return nil
}

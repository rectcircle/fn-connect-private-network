package client

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/coder/websocket"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

const (
	nativeSessionVersion = 1
	nativeRPCReadLimit   = 256 * 1024
	nativeRPCTimeout     = 20 * time.Second
)

type NativeSession struct {
	Version   int       `json:"version"`
	FNID      string    `json:"fnId"`
	Username  string    `json:"username"`
	DeviceID  string    `json:"deviceId"`
	Token     string    `json:"token"`
	LongToken string    `json:"longToken"`
	Secret    string    `json:"secret"`
	BackID    string    `json:"backId,omitempty"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type NativeAuthenticator interface {
	Login(context.Context, string, string, string, string, string) (NativeSession, error)
	Recover(context.Context, NativeSession, string) (NativeSession, error)
}

type NativeSessionClient struct {
	Dial func(context.Context, string) (*websocket.Conn, error)
}

func (c NativeSessionClient) Login(
	ctx context.Context,
	fnID string,
	username string,
	password string,
	deviceID string,
	deviceName string,
) (NativeSession, error) {
	fnID, err := normalizeFNID(fnID)
	if err != nil {
		return NativeSession{}, model.WrapError(model.ErrorInvalidArgument, "invalid FN ID", false, err)
	}
	username = strings.TrimSpace(username)
	if err := validateLoginInput(username, password); err != nil {
		return NativeSession{}, err
	}
	if deviceID == "" {
		deviceID, err = nativeDeviceID()
		if err != nil {
			return NativeSession{}, model.WithOperation(err, "native_session.device_id")
		}
	}
	connection, err := c.dial(ctx, fnID)
	if err != nil {
		return NativeSession{}, err
	}
	defer connection.CloseNow()
	response, err := nativeRPC(ctx, connection, map[string]any{
		"req":        "user.login",
		"reqid":      nativeRequestID(),
		"user":       username,
		"password":   password,
		"stay":       1,
		"deviceName": valueOrDefault(deviceName, "FnCPN for macOS"),
		"deviceType": "CLI",
		"did":        deviceID,
	}, "")
	if err != nil {
		return NativeSession{}, err
	}
	if nativeString(response, "token") == "" &&
		(nativeBool(response, "isTwofaEnforced") || nativeString(response, "accessToken") != "") {
		return NativeSession{}, model.WithOperation(model.NewError(
			model.ErrorFailedPrecondition,
			"this fnOS account requires two-factor authentication, which is not supported yet",
			false,
		), "native_session.login")
	}
	if !nativeSuccess(response) {
		return NativeSession{}, nativeAuthenticationError("fnOS username or password is invalid", response)
	}
	session := NativeSession{
		Version:   nativeSessionVersion,
		FNID:      fnID,
		Username:  username,
		DeviceID:  deviceID,
		Token:     nativeString(response, "token"),
		LongToken: nativeString(response, "longToken"),
		Secret:    nativeString(response, "secret"),
		BackID:    nativeString(response, "backId"),
		UpdatedAt: time.Now().UTC(),
	}
	if err := validateNativeSession(session); err != nil {
		return NativeSession{}, model.WithOperation(model.WrapError(
			model.ErrorProtocol, "fnOS login returned an incomplete session", false, err,
		), "native_session.login")
	}
	return session, nil
}

func (c NativeSessionClient) Recover(
	ctx context.Context,
	session NativeSession,
	deviceName string,
) (NativeSession, error) {
	if err := validateNativeSession(session); err != nil {
		return NativeSession{}, model.WithOperation(model.WrapError(
			model.ErrorFailedPrecondition, "stored fnOS session is invalid", false, err,
		), "native_session.recover")
	}
	if recovered, err := c.recoverToken(ctx, session, "user.authToken", session.Token, deviceName); err == nil {
		return recovered, nil
	}
	recovered, err := c.recoverToken(ctx, session, "user.tokenLogin", session.LongToken, deviceName)
	if err != nil {
		if model.AsError(err).Code == model.ErrorAuthRequired {
			return NativeSession{}, model.WithOperation(model.NewError(
				model.ErrorAuthRequired,
				"fnOS session expired; sign in again",
				false,
			), "native_session.recover")
		}
		return NativeSession{}, err
	}
	return recovered, nil
}

func (c NativeSessionClient) recoverToken(
	ctx context.Context,
	session NativeSession,
	method string,
	token string,
	deviceName string,
) (NativeSession, error) {
	connection, err := c.dial(ctx, session.FNID)
	if err != nil {
		return NativeSession{}, err
	}
	defer connection.CloseNow()
	siResponse, err := nativeRPC(ctx, connection, map[string]any{
		"req":   "util.getSI",
		"reqid": nativeRequestID(),
	}, "")
	if err != nil {
		return NativeSession{}, err
	}
	si := nativeString(siResponse, "si")
	if !nativeSuccess(siResponse) || si == "" {
		return NativeSession{}, model.WithOperation(model.NewError(
			model.ErrorProtocol, "fnOS did not return a session integrity value", false,
		), "native_session.get_si")
	}
	request := map[string]any{
		"req":   method,
		"reqid": nativeRequestID(),
		"token": token,
		"si":    si,
	}
	if method == "user.authToken" {
		request["main"] = true
		request["active"] = true
	} else {
		request["deviceName"] = valueOrDefault(deviceName, "FnCPN for macOS")
		request["deviceType"] = "CLI"
	}
	response, err := nativeRPC(ctx, connection, request, session.Secret)
	if err != nil {
		return NativeSession{}, err
	}
	if !nativeSuccess(response) {
		return NativeSession{}, nativeAuthenticationError("fnOS session token was rejected", response)
	}
	recovered := session
	for field, target := range map[string]*string{
		"token": &recovered.Token, "longToken": &recovered.LongToken,
		"secret": &recovered.Secret, "backId": &recovered.BackID,
	} {
		if value := nativeString(response, field); value != "" {
			*target = value
		}
	}
	recovered.UpdatedAt = time.Now().UTC()
	if err := validateNativeSession(recovered); err != nil {
		return NativeSession{}, model.WithOperation(model.WrapError(
			model.ErrorProtocol, "fnOS returned an invalid recovered session", false, err,
		), "native_session.recover")
	}
	return recovered, nil
}

func (c NativeSessionClient) dial(ctx context.Context, fnID string) (*websocket.Conn, error) {
	if c.Dial != nil {
		connection, err := c.Dial(ctx, fnID)
		if err != nil {
			return nil, nativeTransportError("connect", err)
		}
		connection.SetReadLimit(nativeRPCReadLimit)
		return connection, nil
	}
	target := url.URL{
		Scheme:   "wss",
		Host:     fnID + ".fnos.net",
		Path:     "/websocket",
		RawQuery: "type=main",
	}
	headers := make(http.Header)
	headers.Set("Cookie", "mode=relay")
	headers.Set("Origin", "https://"+target.Host)
	dialContext, cancel := context.WithTimeout(ctx, nativeRPCTimeout)
	defer cancel()
	connection, response, err := websocket.Dial(dialContext, target.String(), &websocket.DialOptions{
		HTTPHeader: headers,
		HTTPClient: &http.Client{
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	})
	if err != nil {
		if response != nil && response.Body != nil {
			response.Body.Close()
		}
		return nil, nativeTransportError("connect", err)
	}
	connection.SetReadLimit(nativeRPCReadLimit)
	return connection, nil
}

func nativeRPC(
	ctx context.Context,
	connection *websocket.Conn,
	request map[string]any,
	secret string,
) (map[string]any, error) {
	data, err := json.Marshal(request)
	if err != nil {
		return nil, model.WithOperation(err, "native_session.encode")
	}
	payload := data
	if secret != "" {
		key, decodeErr := decodeNativeSecret(secret)
		if decodeErr != nil || len(key) == 0 {
			return nil, model.WithOperation(model.NewError(
				model.ErrorFailedPrecondition, "stored fnOS signing key is invalid", false,
			), "native_session.sign")
		}
		signature := hmac.New(sha256.New, key)
		_, _ = signature.Write(data)
		prefix := base64.StdEncoding.EncodeToString(signature.Sum(nil))
		payload = append([]byte(prefix), data...)
	}
	requestContext, cancel := context.WithTimeout(ctx, nativeRPCTimeout)
	defer cancel()
	if err := connection.Write(requestContext, websocket.MessageText, payload); err != nil {
		return nil, nativeTransportError("write", err)
	}
	requestID := fmt.Sprint(request["reqid"])
	merged := make(map[string]any)
	for {
		messageType, data, err := connection.Read(requestContext)
		if err != nil {
			return nil, nativeTransportError("read", err)
		}
		if messageType != websocket.MessageText {
			continue
		}
		var response map[string]any
		if err := json.Unmarshal(data, &response); err != nil {
			continue
		}
		if fmt.Sprint(response["reqid"]) != requestID {
			continue
		}
		for key, value := range response {
			merged[key] = value
		}
		if _, complete := response["result"]; complete {
			return merged, nil
		}
	}
}

func nativeSuccess(response map[string]any) bool {
	result := strings.ToLower(nativeString(response, "result"))
	return result == "succ" || result == "success"
}

func nativeString(response map[string]any, name string) string {
	if value, ok := response[name].(string); ok {
		return value
	}
	if data, ok := response["data"].(map[string]any); ok {
		value, _ := data[name].(string)
		return value
	}
	return ""
}

func nativeBool(response map[string]any, name string) bool {
	if value, ok := response[name].(bool); ok {
		return value
	}
	if data, ok := response["data"].(map[string]any); ok {
		value, _ := data[name].(bool)
		return value
	}
	return false
}

func nativeAuthenticationError(message string, response map[string]any) error {
	failure := model.NewError(model.ErrorAuthRequired, message, false)
	if value, ok := response["errno"]; ok {
		failure.RemoteCode = fmt.Sprint(value)
	}
	if detail := nativeString(response, "errmsg"); detail != "" {
		failure.Detail = model.SafeText(detail)
	}
	return model.WithOperation(failure, "native_session.authenticate")
}

func nativeTransportError(operation string, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return model.WithOperation(model.AsError(err), "native_session."+operation)
	}
	return model.WithOperation(model.NormalizeError(
		err, model.ErrorUnavailable, "fnOS authentication service is unavailable", true,
	), "native_session."+operation)
}

func validateLoginInput(username, password string) error {
	if username == "" || len(username) > 128 || strings.IndexFunc(username, unicode.IsControl) >= 0 {
		return model.NewError(model.ErrorInvalidArgument, "invalid fnOS username", false)
	}
	if password == "" || len(password) > 4096 ||
		strings.IndexFunc(password, func(value rune) bool { return value == 0 }) >= 0 {
		return model.NewError(model.ErrorInvalidArgument, "invalid fnOS password", false)
	}
	return nil
}

func validateNativeSession(session NativeSession) error {
	fnID, err := normalizeFNID(session.FNID)
	if err != nil || fnID != session.FNID {
		return errors.New("invalid session FN ID")
	}
	if session.Version != nativeSessionVersion {
		return errors.New("unsupported native session version")
	}
	if err := validateLoginInput(session.Username, "stored"); err != nil {
		return err
	}
	if session.DeviceID == "" || len(session.DeviceID) > 128 ||
		session.Token == "" || session.LongToken == "" || session.Secret == "" {
		return errors.New("missing native session field")
	}
	key, err := decodeNativeSecret(session.Secret)
	if err != nil || len(key) == 0 {
		return errors.New("invalid native session secret")
	}
	return nil
}

func decodeNativeSecret(value string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(value)
	if err == nil {
		return key, nil
	}
	return base64.RawStdEncoding.DecodeString(value)
}

func nativeDeviceID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return "fncpn-macos-" + hex.EncodeToString(value[:]), nil
}

func nativeRequestID() string {
	var value [8]byte
	_, _ = rand.Read(value[:])
	return fmt.Sprintf("%x%s", time.Now().UnixMilli(), hex.EncodeToString(value[:]))
}

func nativeGatewayCookies(fnID, token string) []Cookie {
	domain := fnID + ".fnos.net"
	return []Cookie{
		{Name: "mode", Value: "relay", Domain: domain, Path: "/", Secure: true, HTTPOnly: true, HostOnly: true},
		{Name: "fnos-token", Value: token, Domain: domain, Path: "/", Secure: true, HTTPOnly: true, HostOnly: true},
	}
}

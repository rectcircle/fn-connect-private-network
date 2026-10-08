package client

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/coder/websocket"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

const (
	nativeSessionVersion   = 1
	adminWebSessionVersion = 1
	nativeRPCReadLimit     = 256 * 1024
	nativeRPCTimeout       = 20 * time.Second
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

type AdminWebSession struct {
	Version   int       `json:"version"`
	FNID      string    `json:"fnId"`
	Username  string    `json:"username"`
	DeviceID  string    `json:"deviceId"`
	Token     string    `json:"token"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type NativeAuthenticator interface {
	Login(context.Context, string, string, string, string, string) (NativeSession, error)
	Recover(context.Context, NativeSession, string) (NativeSession, error)
}

type AdminWebAuthenticator interface {
	WebLogin(context.Context, string, string, string, string, string) (AdminWebSession, error)
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
		failure := model.WithOperation(model.NewError(
			model.ErrorFailedPrecondition,
			"this fnOS account requires two-factor authentication, which is not supported yet",
			false,
		), "native_session.login")
		failure.RemoteCode = "TWO_FACTOR_REQUIRED"
		return NativeSession{}, failure
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
	if recovered, err := c.recoverToken(
		ctx, session, "user.authToken", session.Token, deviceName,
	); err == nil {
		return recovered, nil
	}
	recovered, err := c.recoverToken(
		ctx, session, "user.tokenLogin", session.LongToken, deviceName,
	)
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

// WebLogin performs an encrypted fnOS Web login for the management UI. Its
// token tuple is kept separate from NativeSession and is never used by the
// tunnel path. This mirrors the browser protocol:
//
//  1. util.crypto.getRSAPub returns an RSA public key.
//  2. A fresh 32-byte AES key and 16-byte IV are generated.
//  3. user+password+did are AES-256-CBC encrypted (PKCS#7 padding).
//  4. The AES key is RSA PKCS#1 v1.5 encrypted with the NAS public key.
//  5. A single `{"req":"encrypted","iv","rsa","aes"}` frame carries both.
func (c NativeSessionClient) WebLogin(
	ctx context.Context,
	fnID string,
	username string,
	password string,
	deviceID string,
	deviceName string,
) (AdminWebSession, error) {
	return c.webLogin(ctx, fnID, username, password, deviceID, deviceName)
}

func (c NativeSessionClient) webLogin(
	ctx context.Context, fnID, username, password, deviceID, deviceName string,
) (AdminWebSession, error) {
	fnID, err := normalizeFNID(fnID)
	if err != nil {
		return AdminWebSession{}, model.WrapError(model.ErrorInvalidArgument, "invalid FN ID", false, err)
	}
	username = strings.TrimSpace(username)
	if err := validateLoginInput(username, password); err != nil {
		return AdminWebSession{}, err
	}
	if deviceID == "" {
		deviceID, err = nativeDeviceID()
		if err != nil {
			return AdminWebSession{}, model.WithOperation(err, "native_session.web_login.device_id")
		}
	}
	connection, err := c.dial(ctx, fnID)
	if err != nil {
		return AdminWebSession{}, err
	}
	defer connection.CloseNow()

	// 1. Fetch the NAS RSA public key.
	pubResponse, err := nativeRPC(ctx, connection, map[string]any{
		"req":   "util.crypto.getRSAPub",
		"reqid": nativeRequestID(),
	}, "")
	if err != nil {
		return AdminWebSession{}, err
	}
	pemKey := nativeString(pubResponse, "pub")
	if pemKey == "" {
		pemKey = nativeString(pubResponse, "pubkey")
	}
	if pemKey == "" {
		pemKey = nativeString(pubResponse, "pubKey")
	}
	pemKey = strings.TrimSpace(pemKey)
	if pemKey == "" || !strings.Contains(pemKey, "BEGIN") {
		return AdminWebSession{}, model.WithOperation(model.NewError(
			model.ErrorProtocol, "fnOS did not return an RSA public key", false,
		), "native_session.web_login.rsa_pub")
	}
	pub, err := parseRSAPublicKey(pemKey)
	if err != nil {
		return AdminWebSession{}, model.WithOperation(err, "native_session.web_login.rsa_pub")
	}
	// The NAS returns a session-integrity value `si` with the public key; it must
	// be echoed inside the encrypted login payload. If getRSAPub did not include
	// it, fetch it explicitly via util.getSI.
	si := nativeString(pubResponse, "si")
	if si == "" {
		siResponse, siErr := nativeRPC(ctx, connection, map[string]any{
			"req":   "util.getSI",
			"reqid": nativeRequestID(),
		}, "")
		if siErr == nil {
			si = nativeString(siResponse, "si")
		}
	}
	if si == "" {
		return AdminWebSession{}, model.WithOperation(model.NewError(
			model.ErrorProtocol, "fnOS did not return a session integrity value", false,
		), "native_session.web_login.si")
	}

	// 2. Generate a fresh 32-byte AES key (base62 UTF-8) and 16-byte IV.
	aesKeyBytes := make([]byte, 32)
	for i := range aesKeyBytes {
		index, randomErr := cryptoRandInt(len(base62Chars))
		if randomErr != nil {
			return AdminWebSession{}, model.WithOperation(
				randomErr, "native_session.web_login.aes_key",
			)
		}
		aesKeyBytes[i] = base62Chars[index]
	}
	ivBytes := make([]byte, aes.BlockSize)
	if _, err := rand.Read(ivBytes); err != nil {
		return AdminWebSession{}, model.WithOperation(err, "native_session.web_login.iv")
	}

	// 4. Encrypt the AES key with RSA. The browser frontend calls jsencrypt's
	// encrypt() which uses RSA PKCS#1 v1.5 padding (it falls back to the legacy
	// JSEncrypt path; the OAEP branch is only used when the newer WebCrypto
	// path is active, which defaults off).
	rsaBytes, err := rsa.EncryptPKCS1v15(rand.Reader, pub, aesKeyBytes)
	if err != nil {
		return AdminWebSession{}, model.WithOperation(err, "native_session.web_login.rsa")
	}

	// 3. AES-256-CBC encrypt the FULL user.login request plus `si`. The browser
	// interceptor encrypts `{...request, si}` where request is the whole login
	// RPC (req, reqid, user, password, ...). If we encrypted only the credentials
	// the NAS cannot recognise it as a login and never answers.
	reqid := nativeRequestID()
	loginData := map[string]any{
		"req":        "user.login",
		"reqid":      reqid,
		"user":       username,
		"password":   password,
		"stay":       1,
		"deviceName": valueOrDefault(deviceName, "FnCPN for macOS"),
		"deviceType": "Browser",
		"did":        deviceID,
		"si":         si,
	}
	plain, err := json.Marshal(loginData)
	if err != nil {
		return AdminWebSession{}, model.WithOperation(err, "native_session.web_login.encode")
	}
	aesEnc, err := aesCBCEncrypt(aesKeyBytes, ivBytes, plain)
	if err != nil {
		return AdminWebSession{}, model.WithOperation(err, "native_session.web_login.aes")
	}

	// 5. Fire the encrypted login frame. Unlike the authenticated RPCs this is
	// a bare handshake frame with no reqid; the NAS answers with a session
	// handshake response whose reqid does not echo ours, so it cannot go through
	// nativeRPC (which waits for a matching reqid).
	response, err := nativeHandshake(ctx, connection, map[string]any{
		"req": "encrypted",
		"iv":  base64.StdEncoding.EncodeToString(ivBytes),
		"rsa": base64.StdEncoding.EncodeToString(rsaBytes),
		"aes": aesEnc,
	})
	if err != nil {
		return AdminWebSession{}, err
	}
	if !nativeSuccess(response) {
		return AdminWebSession{}, nativeAuthenticationError("fnOS web login failed", response)
	}
	session := AdminWebSession{
		Version:   adminWebSessionVersion,
		FNID:      fnID,
		Username:  username,
		DeviceID:  deviceID,
		UpdatedAt: time.Now().UTC(),
	}
	if value := nativeString(response, "token"); value != "" {
		session.Token = value
	}
	return session, nil
}

const base62Chars = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"

func cryptoRandInt(max int) (int, error) {
	value, err := rand.Int(rand.Reader, big.NewInt(int64(max)))
	if err != nil {
		return 0, err
	}
	return int(value.Int64()), nil
}

func parseRSAPublicKey(pemData string) (*rsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(pemData))
	if block == nil {
		return nil, errors.New("invalid RSA public key PEM")
	}
	if key, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
		if rsaKey, ok := key.(*rsa.PublicKey); ok {
			return rsaKey, nil
		}
		return nil, errors.New("public key is not RSA")
	}
	if key, err := x509.ParsePKCS1PublicKey(block.Bytes); err == nil {
		return key, nil
	}
	return nil, errors.New("failed to parse RSA public key")
}

func aesCBCEncrypt(key, iv, plain []byte) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	padded := pkcs7Pad(plain, block.BlockSize())
	out := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, padded)
	return base64.StdEncoding.EncodeToString(out), nil
}

func pkcs7Pad(data []byte, blockSize int) []byte {
	padLen := blockSize - len(data)%blockSize
	pad := make([]byte, padLen)
	for i := range pad {
		pad[i] = byte(padLen)
	}
	return append(data, pad...)
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

// nativeHandshake writes an un-signed, reqid-less frame (used by the encrypted
// web-login handshake) and returns the first response inside the timeout that
// carries a `result` field. Unlike nativeRPC it does not require the NAS to echo
// our reqid, because the login handshake response uses its own session reqid.
func nativeHandshake(
	ctx context.Context,
	connection *websocket.Conn,
	request map[string]any,
) (map[string]any, error) {
	data, err := json.Marshal(request)
	if err != nil {
		return nil, model.WithOperation(err, "native_session.encode")
	}
	requestContext, cancel := context.WithTimeout(ctx, nativeRPCTimeout)
	defer cancel()
	if err := connection.Write(requestContext, websocket.MessageText, data); err != nil {
		return nil, nativeTransportError("write", err)
	}
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

func validateAdminWebSession(session AdminWebSession) error {
	if session.Version != adminWebSessionVersion {
		return errors.New("unsupported admin Web session version")
	}
	fnID, err := normalizeFNID(session.FNID)
	if err != nil || fnID != session.FNID {
		return errors.New("invalid session FN ID")
	}
	if err := validateLoginInput(session.Username, "stored"); err != nil {
		return err
	}
	if session.DeviceID == "" || len(session.DeviceID) > 128 || session.Token == "" {
		return errors.New("missing admin Web session field")
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

package client

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

// Optional interfaces keep existing login-only authenticators usable.
type adminWebRecoverer interface {
	RecoverWeb(context.Context, AdminWebSession, string) (AdminWebSession, error)
}
type adminWebMaintainer interface {
	MaintainWeb(context.Context, AdminWebSession) error
}

func adminWebAuthRequired() error {
	return model.WithOperation(model.NewError(model.ErrorAuthRequired,
		"fnOS management session expired; sign in again", false), "admin_web.recover")
}

// RecoverWeb uses only the Browser session's credentials, never the CLI tuple.
func (c NativeSessionClient) RecoverWeb(ctx context.Context, session AdminWebSession, deviceName string) (AdminWebSession, error) {
	return c.recoverWebSession(ctx, session, deviceName, false)
}

func (c NativeSessionClient) RecoverRejectedWebToken(ctx context.Context, session AdminWebSession, deviceName string) (AdminWebSession, error) {
	return c.recoverWebSession(ctx, session, deviceName, true)
}
func (c NativeSessionClient) recoverWebSession(ctx context.Context, session AdminWebSession, deviceName string, rejected bool) (AdminWebSession, error) {
	if err := validateAdminWebSession(session); err != nil || session.Secret == "" || session.LongToken == "" {
		return AdminWebSession{}, adminWebAuthRequired()
	}
	conn, err := c.dial(ctx, session.FNID)
	if err != nil {
		return AdminWebSession{}, err
	}
	defer conn.CloseNow()
	var response map[string]any
	if !rejected {
		response, err = recoverWebToken(ctx, conn, session, "user.authToken", deviceName)
		if err != nil {
			return AdminWebSession{}, err
		}
	}
	if rejected || !nativeSuccess(response) {
		response, err = recoverWebToken(ctx, conn, session, "user.tokenLogin", deviceName)
		if err != nil {
			return AdminWebSession{}, err
		}
		if !nativeSuccess(response) {
			return AdminWebSession{}, nativeAuthenticationError("fnOS management authorization was rejected; sign in again", response)
		}
		if nativeString(response, "token") == "" {
			return AdminWebSession{}, model.WithOperation(model.NewError(model.ErrorProtocol, "fnOS Web recovery did not return a token", false), "admin_web.recover")
		}
	}

	recovered := session
	recovered.Version = adminWebSessionVersion
	if token := nativeString(response, "token"); token != "" {
		recovered.Token = token
	}
	if token := nativeString(response, "longToken"); token != "" {
		recovered.LongToken = token
	}
	// Browser tokenLogin retains its existing secret; it does not treat a response
	// secret as plaintext (login secrets are AES encrypted).
	recovered.UpdatedAt = time.Now().UTC()
	return recovered, nil
}

func recoverWebToken(ctx context.Context, conn *websocket.Conn, session AdminWebSession, method, deviceName string) (map[string]any, error) {
	finish := beginSessionRecoveryLog(ctx, "web", session.FNID, method)
	response, err := authenticateWeb(ctx, conn, session, method, deviceName)
	failure := err
	if failure == nil && !nativeSuccess(response) {
		failure = nativeAuthenticationError("Web recovery authentication was rejected", response)
	}
	if failure == nil && method == "user.tokenLogin" && nativeString(response, "token") == "" {
		failure = model.NewError(model.ErrorProtocol, "Web recovery did not return a token", false)
	}
	token, longToken := nativeString(response, "token"), nativeString(response, "longToken")
	finish(token != "" && token != session.Token, longToken != "" && longToken != session.LongToken, failure)
	return response, err
}

func authenticateWeb(ctx context.Context, conn *websocket.Conn, session AdminWebSession, method, deviceName string) (map[string]any, error) {
	si, err := nativeRPC(ctx, conn, map[string]any{"req": "util.getSI", "reqid": nativeRequestID()}, "")
	if err != nil {
		return nil, err
	}
	if !nativeSuccess(si) || nativeString(si, "si") == "" {
		return nil, model.WithOperation(model.NewError(model.ErrorProtocol, "fnOS did not return a Web session integrity value", false), "admin_web.get_si")
	}
	request := map[string]any{"req": method, "reqid": nativeRequestID(), "si": nativeString(si, "si"), "token": session.Token}
	if method == "user.authToken" {
		request["main"] = true
	} else {
		request["token"] = session.LongToken
		request["deviceType"] = "Browser"
		request["deviceName"] = valueOrDefault(deviceName, "FnCPN")
		request["did"] = session.DeviceID
	}
	response, err := nativeRPC(ctx, conn, request, session.Secret)
	if err != nil {
		return nil, err
	}
	if nativeString(response, "token") == "" && (nativeString(response, "accessToken") != "" || nativeBool(response, "isTwofaEnforced")) {
		// Surface additional verification without discarding long-lived credentials.
		// No password or OTP is cached for unattended recovery.
		return submitTwofaCode(ctx, conn, response, nativeString(response, "accessToken"), NativeLogin{DeviceID: session.DeviceID}, "Browser")
	}
	return response, nil
}

// MaintainWeb lives only while the proxy is in use. Closing/idle cancellation
// interrupts both pending RPC reads and the connection; it never refreshes CLI auth.
func (c NativeSessionClient) MaintainWeb(ctx context.Context, session AdminWebSession) error {
	return c.maintainWeb(ctx, session, 15*time.Second, time.Minute)
}

func (c NativeSessionClient) maintainWeb(ctx context.Context, session AdminWebSession, heartbeatInterval, activityInterval time.Duration) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	conn, err := c.dial(ctx, session.FNID)
	if err != nil {
		return err
	}
	defer conn.CloseNow()
	response, err := authenticateWeb(ctx, conn, session, "user.authToken", "")
	if err != nil {
		return err
	}
	if !nativeSuccess(response) {
		return nativeAuthenticationError("fnOS management activity authentication failed", response)
	}
	// A single reader continuously handles pong/notifications and control frames.
	// RPC-based login above finishes before the read pump takes ownership.
	incoming := make(chan map[string]any)
	readError := make(chan error, 1)
	go func() {
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				readError <- err
				return
			}
			var frame map[string]any
			if json.Unmarshal(data, &frame) != nil {
				continue
			}
			select {
			case incoming <- frame:
			case <-ctx.Done():
				return
			}
		}
	}()
	send := func(request map[string]any, secret string) error {
		payload, err := nativeRequestPayload(request, secret)
		if err != nil {
			return err
		}
		writeCtx, cancel := context.WithTimeout(ctx, nativeRPCTimeout)
		defer cancel()
		return conn.Write(writeCtx, websocket.MessageText, payload)
	}
	active := func() error {
		return send(map[string]any{"req": "user.active", "reqid": "fncpn-active-" + nativeRequestID()}, session.Secret)
	}
	if err := active(); err != nil {
		return nativeTransportError("activity", err)
	}
	heartbeat := time.NewTicker(heartbeatInterval)
	defer heartbeat.Stop()
	activity := time.NewTicker(activityInterval)
	defer activity.Stop()
	lastPong := time.Now()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-readError:
			return nativeTransportError("activity", err)
		case frame := <-incoming:
			if nativeString(frame, "res") == "pong" {
				lastPong = time.Now()
			}
			if strings.HasPrefix(nativeString(frame, "reqid"), "fncpn-active-") && nativeString(frame, "result") == "fail" {
				return nativeAuthenticationError("fnOS management activity was rejected", frame)
			}
		case <-activity.C:
			if err := active(); err != nil {
				return nativeTransportError("activity", err)
			}
		case <-heartbeat.C:
			if time.Since(lastPong) > 3*heartbeatInterval {
				return model.NewError(model.ErrorUnavailable, "fnOS management heartbeat timed out", true)
			}
			if err := send(map[string]any{"req": "ping"}, ""); err != nil {
				return nativeTransportError("heartbeat", err)
			}
		}
	}
}

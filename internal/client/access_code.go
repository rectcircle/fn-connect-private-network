package client

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

// The access code is a fnOS v1.2.0203+ gateway-layer gate enforced by FN Connect
// before any HTTP or WebSocket request reaches the NAS, including the native
// login WebSocket. It is verified once and kept as the `os-access-code` session
// cookie; every later gateway request must carry it.
const (
	accessCodeCookieName    = "os-access-code"
	accessCodeChallengeHead = "x-trim-safe-code-challenge"
	accessCodeVerifyPath    = "/access_code_verify"
	accessCodeTimeout       = 12 * time.Second
	accessCodeBodyLimit     = 16 << 10
)

// detectAccessCode reports whether the FN Connect gateway of fnID currently
// intercepts unauthenticated requests with an access-code challenge. It probes
// the same stable FnCPN route used by the following version check, without
// credentials, and never touches /access_code_verify.
func detectAccessCode(ctx context.Context, fnID string) (bool, error) {
	return detectAccessCodeWithClient(ctx, newAccessCodeClient(), fnID)
}

func detectAccessCodeWithClient(
	ctx context.Context,
	client *http.Client,
	fnID string,
) (bool, error) {
	fnID, err := normalizeFNID(fnID)
	if err != nil {
		return false, model.WrapError(model.ErrorInvalidArgument, "invalid FN ID", false, err)
	}
	requestContext, cancel := context.WithTimeout(ctx, accessCodeTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(
		requestContext, http.MethodGet,
		gatewayURL(fnID, gatewayApplicationPath+"/version"), nil,
	)
	if err != nil {
		return false, model.WithOperation(err, "access_code.detect")
	}
	// This selects the NAS relay route; it is not an authentication credential.
	request.Header.Set("Cookie", nativeRelayCookie(fnID).requestValue())
	// The probe never follows redirects: an interception is always visible on
	// the first response, and following one could land on the verify endpoint.
	clientCopy := *client
	clientCopy.Jar = nil
	clientCopy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := clientCopy.Do(request)
	if err != nil {
		return false, model.WithOperation(model.NormalizeError(
			err, model.ErrorUnavailable, "fnOS gateway is unavailable", true,
		), "access_code.detect")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, accessCodeBodyLimit))
	if err != nil {
		return false, model.WithOperation(model.NormalizeError(
			err, model.ErrorUnavailable, "fnOS gateway is unavailable", true,
		), "access_code.detect")
	}
	return accessCodeChallengeDetected(response.Header, body), nil
}

func accessCodeChallenged(header http.Header) bool {
	return header.Get(accessCodeChallengeHead) == "1"
}

// verifyAccessCode exchanges the user-supplied access code for the gateway
// session cookie. The gateway answers 204 with the cookie on success and
// 401/403/429 on rejection or temporary rate limiting.
func verifyAccessCode(ctx context.Context, fnID string, code string) (Cookie, error) {
	return verifyAccessCodeWithClient(ctx, newAccessCodeClient(), fnID, code)
}

func verifyAccessCodeWithClient(
	ctx context.Context,
	client *http.Client,
	fnID string,
	code string,
) (Cookie, error) {
	fnID, err := normalizeFNID(fnID)
	if err != nil {
		return Cookie{}, model.WrapError(model.ErrorInvalidArgument, "invalid FN ID", false, err)
	}
	if err := validateAccessCode(code); err != nil {
		return Cookie{}, err
	}
	requestContext, cancel := context.WithTimeout(ctx, accessCodeTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(
		requestContext, http.MethodGet, gatewayURL(fnID, accessCodeVerifyPath), nil,
	)
	if err != nil {
		return Cookie{}, model.WithOperation(err, "access_code.verify")
	}
	request.Header.Set("x-access-code", base64.StdEncoding.EncodeToString([]byte(code)))
	request.Header.Set("x-access-source", "web")
	request.Header.Set("Cookie", nativeRelayCookie(fnID).requestValue())
	clientCopy := *client
	clientCopy.Jar = nil
	clientCopy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := clientCopy.Do(request)
	if err != nil {
		return Cookie{}, model.WithOperation(model.NormalizeError(
			err, model.ErrorUnavailable, "fnOS gateway is unavailable", true,
		), "access_code.verify")
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, accessCodeBodyLimit))
		response.Body.Close()
	}()
	switch response.StatusCode {
	case http.StatusNoContent:
		for _, cookie := range response.Cookies() {
			if cookie.Name != accessCodeCookieName || cookie.Value == "" {
				continue
			}
			// The gateway omits Secure. Persist it as secure to restrict replay
			// to HTTPS and WSS, which are the transports FnCPN uses for it.
			record := cookieFromHTTP(cookie, request.URL, time.Now().UTC())
			record.Secure = true
			return record, nil
		}
		return Cookie{}, model.WithOperation(model.NewError(
			model.ErrorProtocol, "fnOS access code verification returned no session", false,
		), "access_code.verify")
	case http.StatusUnauthorized, http.StatusForbidden:
		return Cookie{}, accessCodeFailure("fnOS access code is invalid", false, response)
	case http.StatusTooManyRequests:
		return Cookie{}, accessCodeFailure(
			"fnOS access code attempts are rate limited, try again later", true, response,
		)
	default:
		return Cookie{}, model.WithOperation(model.NewError(
			model.ErrorUnavailable, "fnOS access code verification failed", true,
		), "access_code.verify")
	}
}

func accessCodeFailure(message string, retryable bool, response *http.Response) error {
	failure := model.NewError(model.ErrorFailedPrecondition, message, retryable)
	failure.RemoteCode = "ACCESS_CODE_INVALID"
	failure.HTTPStatus = response.StatusCode
	if response.StatusCode == http.StatusTooManyRequests {
		failure.RemoteCode = "ACCESS_CODE_RATE_LIMITED"
	}
	return model.WithOperation(failure, "access_code.verify")
}

// accessCodeRequiredError reports that the gateway blocked a request because no
// access-code session has been established yet.
func accessCodeRequiredError() error {
	failure := model.NewError(
		model.ErrorFailedPrecondition,
		"this fnOS NAS requires an access code before signing in",
		false,
	)
	failure.RemoteCode = "ACCESS_CODE_REQUIRED"
	return model.WithOperation(failure, "access_code.challenge")
}

func validateAccessCode(code string) error {
	if code == "" {
		return nil
	}
	if len(code) > 128 || strings.IndexFunc(code, unicode.IsControl) >= 0 {
		return model.NewError(model.ErrorInvalidArgument, "invalid fnOS access code", false)
	}
	return nil
}

func validateTwofaCode(code string) error {
	if code == "" {
		return nil
	}
	if len(code) != 6 || strings.IndexFunc(code, func(value rune) bool {
		return value < '0' || value > '9'
	}) >= 0 {
		return model.NewError(model.ErrorInvalidArgument, "invalid two-factor code", false)
	}
	return nil
}

func gatewayURL(fnID, path string) string {
	return (&url.URL{
		Scheme: "https",
		Host:   fnID + ".fnos.net",
		Path:   path,
	}).String()
}

func newAccessCodeClient() *http.Client {
	return &http.Client{Transport: newFNOSTransport(nil), Timeout: accessCodeTimeout}
}

// accessCodeChallengeDetected reports whether a failed gateway response is the
// access-code interception page.
func accessCodeChallengeDetected(header http.Header, body []byte) bool {
	return accessCodeChallenged(header) ||
		strings.Contains(strings.ToLower(header.Get("Location")), accessCodeVerifyPath) ||
		(bytes.HasPrefix(bytes.TrimSpace(body), []byte("<")) &&
			bytes.Contains(bytes.ToLower(body), []byte(accessCodeVerifyPath)))
}

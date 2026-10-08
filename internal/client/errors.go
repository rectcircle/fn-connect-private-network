package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"github.com/rectcircle/fn-connect-private-network/internal/version"
)

// HTTPResponseError consumes a bounded error body for API, upgrade, and
// healthcheck requests. Full headers and HTML/unknown JSON are never exposed.
func HTTPResponseError(response *http.Response, operation string, cause error) *model.Error {
	code, retryable := model.ErrorProtocol, false
	switch response.StatusCode {
	case 400:
		code = model.ErrorInvalidArgument
	case 401:
		code = model.ErrorAuthRequired
	case 403:
		code = model.ErrorPermissionDenied
	case 404:
		code = model.ErrorNotFound
		// fnOS removes the application route while stopped. Retry an untyped
		// gateway 404; a recognized FnCPN error below overrides this fallback.
		if response.Request != nil && response.Request.URL != nil &&
			strings.HasPrefix(response.Request.URL.Path, gatewayApplicationPath+"/") {
			code, retryable = model.ErrorUnavailable, true
		}
	case 409:
		code = model.ErrorConflict
	case 410:
		code = model.ErrorDeviceRevoked
	case 412:
		code = model.ErrorFailedPrecondition
	case 408, 504:
		code, retryable = model.ErrorTimeout, true
	case 429:
		code, retryable = model.ErrorResourceExhausted, true
	default:
		if response.StatusCode >= 500 {
			code, retryable = model.ErrorUnavailable, true
		}
	}
	result := model.WrapError(code,
		fmt.Sprintf("remote request returned HTTP %d %s", response.StatusCode, http.StatusText(response.StatusCode)),
		retryable, cause)
	if response.Body != nil {
		data, readErr := io.ReadAll(io.LimitReader(response.Body, 8193))
		result.Cause = errors.Join(cause, readErr)
		var envelope struct {
			Error *model.Error `json:"error"`
		}
		if authErr := gatewayAuthenticationError(response, data); readErr == nil && len(data) <= 8192 && authErr != nil {
			result = authErr
			result.Cause = cause
		} else if len(data) <= 8192 && json.Unmarshal(data, &envelope) == nil &&
			envelope.Error != nil && model.IsErrorCode(envelope.Error.Code) {
			result = model.PublicError(envelope.Error)
			result.Cause = errors.Join(cause, readErr)
		} else {
			text := strings.TrimSpace(string(data))
			switch {
			case text == "":
				result.Detail = "empty response body"
			case len(data) > 8192:
				result.Detail = "response body exceeded diagnostic limit"
			case strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "html") ||
				strings.HasPrefix(text, "<"):
				result.Detail = "HTML response omitted (gateway or login page)"
			case strings.HasPrefix(text, "{") || strings.HasPrefix(text, "["):
				result.Detail = "unrecognized JSON error response"
			default:
				result.Detail = model.SafeText(text)
			}
		}
	}
	if result.Operation == "" {
		result.Operation = operation
	}
	result.HTTPStatus = response.StatusCode
	if result.RequestID == "" {
		result.RequestID = model.SafeRequestID(response.Header.Get("X-Request-ID"))
	}
	return result
}

func gatewayAuthenticationError(response *http.Response, data []byte) *model.Error {
	if response.StatusCode >= 300 && response.StatusCode < 400 &&
		response.Request != nil && response.Request.URL != nil &&
		strings.HasPrefix(response.Request.URL.Path, gatewayApplicationPath+"/") {
		location, err := response.Location()
		if err == nil && (location.Path == "/" || location.Path == "" ||
			location.Path == "/login" || strings.HasPrefix(location.Path, "/login/")) {
			return model.NewError(model.ErrorAuthRequired, "FN Connect redirected to login; authorize again", false)
		}
	}
	// fnOS rejects expired or missing sessions with plain text and HTTP 200,
	// before the request reaches the application. Match only this known response.
	if (response.StatusCode != http.StatusOK && response.StatusCode != http.StatusUnauthorized &&
		response.StatusCode != http.StatusForbidden) || strings.TrimSpace(string(data)) != "invalid token" {
		return nil
	}
	return &model.Error{
		Code:    model.ErrorAuthRequired,
		Message: "FN Connect session is invalid or expired; authorize again",
		Detail:  "fnOS gateway returned invalid token",
	}
}

func responseOperation(response *http.Response) string {
	if response.Request == nil || response.Request.URL == nil {
		return "http.response"
	}
	return response.Request.Method + " " + model.SafeURL(response.Request.URL.String())
}

func decodeResponse(response *http.Response, destination any) error {
	data, err := io.ReadAll(io.LimitReader(response.Body, maxLocalConfigSize+1))
	if err == nil && len(data) > maxLocalConfigSize {
		err = errors.New("JSON response exceeds size limit")
	}
	if err == nil {
		if authErr := gatewayAuthenticationError(response, data); authErr != nil {
			err = authErr
		} else {
			err = model.DecodeResponse(data, destination)
		}
	}
	if err == nil {
		return nil
	}
	failure := model.NormalizeError(err, model.ErrorProtocol, "invalid server JSON response", false)
	if strings.HasPrefix(strings.TrimSpace(string(data)), "<") {
		failure.Detail = "received HTML instead of JSON (gateway or login page)"
	}
	failure.Operation = responseOperation(response)
	failure.HTTPStatus = response.StatusCode
	failure.RequestID = model.SafeRequestID(response.Header.Get("X-Request-ID"))
	return failure
}

// isAdmissionFailure prevents stale configuration and automatic reconnect from
// bypassing a missing server or rejected product version.
func isAdmissionFailure(err error) bool {
	return version.IsFailure(err) || err != nil && model.AsError(err).Code == model.ErrorServerUnavailable
}

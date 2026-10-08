package model

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

const MaxErrorDetail = 2048

var (
	errorURL         = regexp.MustCompile(`(?i)(?:https?|wss?)://[^\s"'<>]+`)
	secretAssignment = regexp.MustCompile(`(?i)"?(?:set-cookie|cookie|authorization|authx|fn-sign|private_?key|access[_ -]?token|refresh[_ -]?token|token|password|passwd|secret)"?\s*[:=]`)
	secretMaterial   = regexp.MustCompile(`[A-Za-z0-9+/]{43}=|[0-9a-fA-F]{64}|[A-Za-z0-9_-]{16,}\.[A-Za-z0-9_-]{16,}\.[A-Za-z0-9_-]{16,}`)
	requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,128}$`)
)

func SafeRequestID(value string) string {
	if requestIDPattern.MatchString(value) {
		return value
	}
	return ""
}

// SafeText is for diagnostics, never for request/response payload logging.
// Keep harmless descriptions such as "authorization is required", but discard
// credential assignments, URL credentials/queries, and key-like strings.
func SafeText(value string) string {
	value = errorURL.ReplaceAllStringFunc(value, SafeURL)
	if secretAssignment.MatchString(value) {
		return "[redacted sensitive error]"
	}
	value = secretMaterial.ReplaceAllString(value, "[redacted]")
	value = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return ' '
		}
		return r
	}, value)
	if len(value) > MaxErrorDetail {
		value = strings.ToValidUTF8(value[:MaxErrorDetail], "") + " [truncated]"
	}
	return value
}

func SafeURL(value string) string {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" {
		return "[redacted URL]"
	}
	switch parsed.Scheme {
	case "http", "https", "ws", "wss":
	default:
		return "[redacted URL]"
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	parsed.RawFragment = ""
	return parsed.String()
}

func joinDetail(a, b string) string {
	if a == "" {
		return SafeText(b)
	}
	if b == "" || strings.Contains(a, b) {
		return SafeText(a)
	}
	return SafeText(a + "; " + b)
}

// ErrorDetail follows typed and joined causes; Error.Error intentionally remains
// the short message used by existing callers.
func ErrorDetail(err error) string {
	if err == nil {
		return ""
	}
	switch value := err.(type) {
	case *Error:
		if value == nil {
			return ""
		}
		return joinDetail(joinDetail(SafeText(value.Message), value.Detail), ErrorDetail(value.Cause))
	case *url.Error:
		return SafeText(fmt.Sprintf("%s %q: %s", value.Op, SafeURL(value.URL), ErrorDetail(value.Err)))
	case interface{ Unwrap() []error }:
		var result string
		for _, child := range value.Unwrap() {
			result = joinDetail(result, ErrorDetail(child))
		}
		return result
	default:
		return joinDetail(SafeText(err.Error()), ErrorDetail(errors.Unwrap(err)))
	}
}

func FormatError(err error) string {
	value := PublicError(err)
	if value == nil {
		return ""
	}
	text := string(value.Code) + ": " + value.Message
	if value.Code == ErrorVersionIncompatible {
		text += fmt.Sprintf(" (client %s, server %s; upgrade %s)", value.ClientVersion, value.ServerVersion, value.UpgradeTarget)
	}
	if value.Operation != "" {
		text += " [" + value.Operation + "]"
	}
	if value.HTTPStatus != 0 {
		text += fmt.Sprintf(" (HTTP %d)", value.HTTPStatus)
	}
	if value.RemoteCode != "" {
		text += " (remote " + value.RemoteCode + ")"
	}
	if value.Detail != "" {
		text += ": " + value.Detail
	}
	if value.RequestID != "" {
		text += " [request " + value.RequestID + "]"
	}
	return text
}

// WithOperation preserves the original classification and the most specific
// failure stage, without modifying an error shared with another goroutine.
func WithOperation(err error, operation string) *Error {
	typed := AsError(err)
	if typed == nil {
		return nil
	}
	result := *typed
	if result.Operation == "" {
		result.Operation = operation
	}
	return &result
}

func IsErrorCode(code ErrorCode) bool {
	switch code {
	case ErrorServerUnavailable, ErrorInvalidArgument, ErrorAuthRequired, ErrorPermissionDenied, ErrorNotFound,
		ErrorAlreadyExists, ErrorFailedPrecondition, ErrorDeviceRevoked, ErrorUnavailable,
		ErrorConflict, ErrorTimeout, ErrorCanceled, ErrorInternal, ErrorProtocol, ErrorVersionIncompatible,
		ErrorResourceExhausted, ErrorDiscoveryFailed:
		return true
	default:
		return false
	}
}

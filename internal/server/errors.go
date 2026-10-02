package server

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/rectcircle/fn-connect-private-network/internal/logging"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

type errorResponseWriter struct {
	http.ResponseWriter
	status int
	logged bool
}

func (w *errorResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *errorResponseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *errorResponseWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(data)
}

func writeHTTPError(logger *slog.Logger, writer http.ResponseWriter, request *http.Request, err error) {
	if tracked, ok := writer.(*errorResponseWriter); ok {
		tracked.logged = true
	}
	failure := model.WithOperation(err, request.Method+" "+request.URL.Path)
	httpRequestID := model.SafeRequestID(writer.Header().Get("X-Request-ID"))
	if failure.RequestID == "" {
		failure.RequestID = httpRequestID
	}
	status := http.StatusInternalServerError
	switch failure.Code {
	case model.ErrorInvalidArgument, model.ErrorProtocol:
		status = http.StatusBadRequest
	case model.ErrorAuthRequired:
		status = http.StatusUnauthorized
	case model.ErrorPermissionDenied:
		status = http.StatusForbidden
	case model.ErrorNotFound:
		status = http.StatusNotFound
	case model.ErrorAlreadyExists, model.ErrorConflict:
		status = http.StatusConflict
	case model.ErrorFailedPrecondition:
		status = http.StatusPreconditionFailed
	case model.ErrorDeviceRevoked:
		status = http.StatusGone
	case model.ErrorUnavailable, model.ErrorDiscoveryFailed:
		status = http.StatusServiceUnavailable
	case model.ErrorResourceExhausted:
		status = http.StatusTooManyRequests
	case model.ErrorTimeout:
		status = http.StatusGatewayTimeout
	case model.ErrorCanceled:
		status = 499
	}
	failure.HTTPStatus = status
	attrs := append(logging.ErrorAttrs(failure), "http_request_id", httpRequestID)
	if failure.Code == model.ErrorCanceled {
		logger.Debug("server request canceled", attrs...)
	} else {
		logger.Error("server request failed", attrs...)
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	if err := json.NewEncoder(writer).Encode(map[string]any{"error": model.PublicError(failure)}); err != nil {
		logger.Error("write HTTP error response", logging.ErrorAttrs(model.WithOperation(err, "http.write_error"))...)
	}
}

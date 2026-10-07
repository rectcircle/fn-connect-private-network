package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/logging"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"github.com/rectcircle/fn-connect-private-network/internal/notify"
)

const (
	maxRequestBody = 64 << 10
	watchTimeout   = 25 * time.Second
)

type serverSnapshot struct {
	state    model.ServerState
	network  model.ServerNetworkSnapshot
	activity map[string]deviceActivity
}

type adminSnapshot struct {
	ProtocolVersion int                         `json:"protocolVersion"`
	Settings        model.ServerSettings        `json:"settings"`
	ServerAddress   string                      `json:"serverAddress"`
	Devices         []adminDevice               `json:"devices"`
	Network         model.ServerNetworkSnapshot `json:"network"`
}

type HTTPServer struct {
	*Service
	probe   *LocalProbeService
	handler http.Handler
}

func NewHTTPHandler(service *Service, probe *LocalProbeService) *HTTPServer {
	server := &HTTPServer{
		Service: service,
		probe:   probe,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", serveIndex)
	mux.HandleFunc(
		"GET /api/v1/bootstrap",
		server.authenticated(false, server.bootstrap),
	)
	mux.HandleFunc(
		"GET /api/v1/admin/devices",
		server.authenticated(true, server.listDevices),
	)
	mux.HandleFunc(
		"DELETE /api/v1/admin/devices/{id}",
		server.authenticated(true, server.deleteDevice),
	)
	mux.HandleFunc(
		"GET /api/v1/admin/snapshot",
		server.authenticated(true, server.adminSnapshot),
	)
	mux.HandleFunc(
		"POST /api/v1/devices",
		server.authenticated(false, server.createDevice),
	)
	mux.HandleFunc(
		"GET /api/v1/devices/{id}/config",
		server.authenticated(false, server.deviceConfiguration),
	)
	mux.HandleFunc(
		"GET /api/v1/devices/{id}/local-probe",
		server.authenticated(false, server.localProbeConfiguration),
	)
	mux.HandleFunc(
		"PUT /api/v1/admin/networks",
		server.authenticated(true, server.updateNetworks),
	)
	mux.Handle(
		"GET /relay/v1/wireguard",
		server.authenticated(false, newRelayHandler(service, service.logger).ServeHTTP),
	)
	server.handler = stripGatewayPrefix(mux)
	return server
}

func (s *HTTPServer) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	requestID, err := newID()
	if err != nil {
		writeHTTPError(s.logger, writer, request, model.WithOperation(err, "http.request_id"))
		return
	}
	writer.Header().Set("X-Request-ID", requestID)
	request = request.WithContext(logging.WithLogger(request.Context(),
		s.logger.With("http_request_id", requestID)))
	tracked := &errorResponseWriter{ResponseWriter: writer}
	s.handler.ServeHTTP(tracked, request)
	if tracked.status >= 400 && !tracked.logged {
		failure := model.NewError(model.ErrorProtocol, http.StatusText(tracked.status), false)
		failure.HTTPStatus = tracked.status
		failure.RequestID = requestID
		failure.Operation = request.Method + " " + request.URL.Path
		s.logger.Error("HTTP request rejected", logging.ErrorAttrs(failure)...)
	}
}

func (s *HTTPServer) localProbeConfiguration(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if s.probe == nil {
		s.writeError(writer, request, model.NewError(
			model.ErrorUnavailable,
			"local probe is unavailable",
			true,
		))
		return
	}
	configuration, err := s.probe.Configuration(request.PathValue("id"))
	if err != nil {
		s.writeError(writer, request, err)
		return
	}
	s.writeJSON(writer, http.StatusOK, configuration)
}

func (s *HTTPServer) bootstrap(writer http.ResponseWriter, request *http.Request) {
	view := s.snapshot()
	state := view.state
	serverAddress, err := ServerAddress(state.Settings.OverlayCIDR)
	if err != nil {
		s.writeError(writer, request, err)
		return
	}
	response := map[string]any{
		"protocolVersion": model.ProtocolVersion,
		"administrator": strings.EqualFold(
			request.Header.Get("X-Trim-Isadmin"),
			"true",
		),
		"settings":      state.Settings,
		"serverAddress": serverAddress,
		"deviceCount":   len(state.Devices),
	}
	if s.network != nil {
		response["network"] = view.network
	}
	logging.FromContext(request.Context(), s.logger).Info("bootstrap authorization checked",
		"administrator", response["administrator"], "device_count", len(state.Devices))
	s.writeJSON(writer, http.StatusOK, response)
}

func (s *HTTPServer) listDevices(writer http.ResponseWriter, request *http.Request) {
	view := s.snapshot()
	s.writeJSON(writer, http.StatusOK, map[string]any{
		"devices": deviceViews(view, time.Now()),
		"network": view.network,
	})
}

func (s *HTTPServer) deleteDevice(writer http.ResponseWriter, request *http.Request) {
	if err := s.DeleteDevice(request.Context(), request.PathValue("id")); err != nil {
		s.writeError(writer, request, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (s *HTTPServer) adminSnapshot(
	writer http.ResponseWriter,
	request *http.Request,
) {
	value := func() (any, error) {
		view := s.snapshot()
		serverAddress, err := ServerAddress(view.state.Settings.OverlayCIDR)
		if err != nil {
			return nil, err
		}
		return adminSnapshot{
			ProtocolVersion: model.ProtocolVersion,
			Settings:        view.state.Settings,
			ServerAddress:   serverAddress,
			Devices:         deviceViews(view, time.Now()),
			Network:         view.network,
		}, nil
	}
	if request.URL.Query().Get("watch") != "1" {
		snapshot, err := value()
		if err != nil {
			s.writeError(writer, request, err)
			return
		}
		s.writeJSON(writer, http.StatusOK, snapshot)
		return
	}
	s.writeWatchJSON(
		writer,
		request,
		s.adminChanges,
		adminSnapshotCursor,
		value,
		func(changed bool, cursor string, current any) any {
			response := map[string]any{
				"changed": changed,
				"cursor":  cursor,
			}
			if changed {
				response["snapshot"] = current
			}
			return response
		},
	)
}

func (s *HTTPServer) createDevice(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		Name      string `json:"name"`
		PublicKey string `json:"publicKey"`
	}
	if err := decodeBody(request, &input); err != nil {
		s.writeError(writer, request, err)
		return
	}
	result, created, err := s.RegisterDevice(
		request.Context(),
		input.Name,
		input.PublicKey,
	)
	if err != nil {
		s.writeError(writer, request, err)
		return
	}
	statusCode := http.StatusOK
	if created {
		statusCode = http.StatusCreated
	}
	s.writeJSON(writer, statusCode, result)
}

func (s *HTTPServer) deviceConfiguration(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if s.network == nil {
		s.writeError(writer, request, model.NewError(
			model.ErrorUnavailable,
			"server network is unavailable",
			true,
		))
		return
	}
	deviceID := request.PathValue("id")
	value := func() (any, error) {
		return s.clientConfiguration(deviceID)
	}
	if request.URL.Query().Get("watch") != "1" {
		configuration, err := value()
		if err != nil {
			s.writeError(writer, request, err)
			return
		}
		s.writeJSON(writer, http.StatusOK, configuration)
		return
	}
	s.writeWatchJSON(
		writer,
		request,
		s.configChanges,
		valueCursor,
		value,
		func(changed bool, cursor string, current any) any {
			response := map[string]any{
				"changed": changed,
				"cursor":  cursor,
			}
			if changed {
				response["configuration"] = current
			}
			return response
		},
	)
}

func (s *HTTPServer) updateNetworks(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		OverlayCIDR *string   `json:"overlayCIDR"`
		ListenPort  *uint16   `json:"listenPort"`
		LANCIDRs    *[]string `json:"lanCIDRs"`
	}
	if err := decodeBody(request, &input); err != nil {
		s.writeError(writer, request, err)
		return
	}
	state, err := s.UpdateNetworks(request.Context(), NetworkUpdate{
		OverlayCIDR: input.OverlayCIDR,
		ListenPort:  input.ListenPort,
		LANCIDRs:    input.LANCIDRs,
	})
	if err != nil {
		s.writeError(writer, request, err)
		return
	}
	s.writeJSON(writer, http.StatusOK, state)
}

func (s *HTTPServer) writeError(writer http.ResponseWriter, request *http.Request, err error) {
	writeHTTPError(s.logger, writer, request, err)
}

func (s *HTTPServer) writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		s.logger.Error("encode HTTP response", "error", err)
	}
}

func (s *HTTPServer) writeWatchJSON(
	writer http.ResponseWriter,
	request *http.Request,
	changes *notify.Change,
	cursorFor func(any) string,
	value func() (any, error),
	response func(bool, string, any) any,
) {
	current, err := value()
	if err != nil {
		s.writeError(writer, request, err)
		return
	}
	after := strings.TrimSpace(request.URL.Query().Get("after"))
	cursor := cursorFor(current)
	if after != "" && after == cursor {
		generation := changes.Current()
		current, err = value()
		if err != nil {
			s.writeError(writer, request, err)
			return
		}
		cursor = cursorFor(current)
		if after == cursor {
			waitContext, cancel := context.WithTimeout(request.Context(), watchTimeout)
			changes.Wait(waitContext, generation)
			cancel()
			current, err = value()
			if err != nil {
				s.writeError(writer, request, err)
				return
			}
			cursor = cursorFor(current)
		}
	}
	writer.Header().Set("Cache-Control", "no-store")
	changed := after == "" || after != cursor
	s.writeJSON(writer, http.StatusOK, response(changed, cursor, current))
}

func adminSnapshotCursor(value any) string {
	snapshot, ok := value.(adminSnapshot)
	if !ok {
		return valueCursor(value)
	}
	snapshot.Network.RefreshedAt = time.Time{}
	return valueCursor(snapshot)
}

func semanticSnapshotCursor(view serverSnapshot) string {
	// Compare connection states at each observation time so keepalive expiry
	// notifies the admin watch even when peer counters have not changed.
	devices := deviceViews(view, view.network.RefreshedAt)
	view.network.RefreshedAt = time.Time{}
	return valueCursor(struct {
		Settings model.ServerSettings
		Devices  []adminDevice
		Network  model.ServerNetworkSnapshot
	}{
		Settings: view.state.Settings,
		Devices:  devices,
		Network:  view.network,
	})
}

func configurationSnapshotCursor(view serverSnapshot) string {
	return valueCursor(struct {
		State     model.ServerState
		Fresh     bool
		Active    bool
		PublicKey string
	}{
		State:     view.state,
		Fresh:     view.network.Fresh,
		Active:    view.network.Network.Active,
		PublicKey: view.network.Network.PublicKey,
	})
}

func valueCursor(value any) string {
	data, _ := json.Marshal(value)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (s *HTTPServer) authenticated(
	administrator bool,
	next http.HandlerFunc,
) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if strings.TrimSpace(request.Header.Get("X-Trim-Userid")) == "" {
			s.writeError(writer, request, model.NewError(
				model.ErrorAuthRequired,
				"authenticated user is required",
				false,
			))
			return
		}
		if administrator &&
			!strings.EqualFold(request.Header.Get("X-Trim-Isadmin"), "true") {
			s.writeError(writer, request, model.NewError(
				model.ErrorPermissionDenied,
				"administrator access is required",
				false,
			))
			return
		}
		next(writer, request)
	}
}

func decodeBody(request *http.Request, destination any) error {
	data, err := io.ReadAll(io.LimitReader(request.Body, maxRequestBody+1))
	if err != nil {
		return model.WrapError(
			model.ErrorInvalidArgument,
			"read request body",
			false,
			err,
		)
	}
	if len(data) > maxRequestBody {
		return model.NewError(
			model.ErrorInvalidArgument,
			"request body is too large",
			false,
		)
	}
	if len(data) == 0 {
		return model.NewError(
			model.ErrorInvalidArgument,
			"request body is required",
			false,
		)
	}
	if err := model.DecodeStrict(data, destination); err != nil {
		return model.WrapError(
			model.ErrorInvalidArgument,
			"invalid request body",
			false,
			err,
		)
	}
	return nil
}

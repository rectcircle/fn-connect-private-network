package ipc

import (
	"encoding/json"
	"errors"
	"io"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

// MaintenanceVersion is independent of ProtocolVersion and the product version.
// Keep the v1 envelope, framing, methods and authorization available across future
// business IPC upgrades. See docs/versioning-and-compatibility.md.
const MaintenanceVersion = 1

type maintenanceRequest struct {
	Version int    `json:"maintenanceVersion"`
	ID      string `json:"id"`
	Method  string `json:"method"`
}

type maintenanceResponse struct {
	Version int    `json:"maintenanceVersion"`
	ID      string `json:"id"`
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty"`
}

// Called only AFTER the server's normal pipe ACL and peer authorization checks.
// Maintenance has no route to the business handler or privileged operations.
func (s Server) handleMaintenance(writer io.Writer, payload []byte) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(payload, &fields) != nil {
		return false
	}
	if _, exists := fields["maintenanceVersion"]; !exists {
		return false
	}
	var request maintenanceRequest
	response := maintenanceResponse{Version: MaintenanceVersion}
	var shutdown func()
	err := model.DecodeStrict(payload, &request)
	response.ID = request.ID
	if err == nil && (request.Version != MaintenanceVersion || request.ID == "") {
		err = errors.New("unsupported or invalid maintenance request")
	}
	if err == nil && s.PrepareMaintenanceShutdown == nil {
		err = errors.New("maintenance is not available on this endpoint")
	}
	if err == nil {
		switch request.Method {
		case "identify": // No user status, configuration or credentials.
		case "shutdown":
			shutdown, err = s.PrepareMaintenanceShutdown()
			if err == nil && shutdown == nil {
				err = errors.New("daemon cannot be stopped")
			}
		default:
			err = errors.New("unsupported maintenance method")
		}
	}
	response.OK = err == nil
	if err != nil {
		response.Error = err.Error()
	}
	encoded, marshalErr := json.Marshal(response)
	if marshalErr == nil {
		if writeErr := WriteFrame(writer, encoded); writeErr == nil && response.OK && shutdown != nil {
			// Acknowledge before stopping the listener, avoiding an EOF/exit race.
			shutdown()
		}
	}
	return true
}

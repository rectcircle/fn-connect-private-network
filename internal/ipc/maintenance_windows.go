//go:build windows

package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"

	"golang.org/x/sys/windows"
)

// legacyMaintenanceResponse is frozen to the released pre-maintenance envelope.
// Do not replace it with Response: business IPC schema changes must not affect
// our ability to stop old installations. Result is ignored, never interpreted.
type legacyMaintenanceResponse struct {
	ProductVersion string          `json:"productVersion"`
	Version        int             `json:"version"`
	ID             string          `json:"id"`
	OK             bool            `json:"ok"`
	Result         json.RawMessage `json:"result"`
	Error          *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// StopDaemonForMaintenance is intentionally separate from Client.Call. It never
// uses ProtocolVersion, product version admission, or business status decoding.
func StopDaemonForMaintenance(ctx context.Context, socket string) error {
	id, err := requestID()
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(maintenanceRequest{Version: MaintenanceVersion, ID: id, Method: "identify"})
	reply, err := maintenanceExchange(ctx, socket, payload)
	if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("identify daemon for maintenance: %w", err)
	}
	legacy, err := identifyMaintenanceReply(reply, id)
	if err != nil {
		return err
	}
	id, err = requestID()
	if err != nil {
		return err
	}
	if legacy == 0 {
		payload, _ = json.Marshal(maintenanceRequest{Version: MaintenanceVersion, ID: id, Method: "shutdown"})
	} else {
		// Fixed compatibility adapters for released IPC 4 and the pre-maintenance
		// IPC 5 test builds. Future business IPC versions do not extend this list.
		payload, _ = json.Marshal(struct {
			Version int    `json:"version"`
			ID      string `json:"id"`
			Method  string `json:"method"`
		}{legacy, id, "shutdown-client-daemon"})
	}
	reply, err = maintenanceExchange(ctx, socket, payload)
	if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("request daemon maintenance shutdown: %w", err)
	}
	if legacy == 0 {
		err = validateMaintenanceReply(reply, id)
	} else {
		err = validateLegacyShutdownReply(reply, id, legacy)
	}
	if err != nil {
		return err
	}
	// Only an absent pipe proves it stopped accepting requests. Access denied,
	// timeout, malformed replies, and busy endpoints must never count as success.
	for {
		conn, err := dialIPC(ctx, socket, time.Second)
		if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("wait for daemon shutdown: %w", err)
		}
		conn.Close()
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for daemon shutdown: %w", ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func maintenanceExchange(ctx context.Context, socket string, payload []byte) ([]byte, error) {
	connection, err := dialIPC(ctx, socket, 2*time.Second)
	if err != nil {
		return nil, err
	}
	defer connection.Close()
	return exchangeMaintenanceFrame(ctx, connection, payload)
}

func exchangeMaintenanceFrame(ctx context.Context, connection net.Conn, payload []byte) ([]byte, error) {
	deadline := time.Now().Add(2 * time.Second)
	if parent, ok := ctx.Deadline(); ok && parent.Before(deadline) {
		deadline = parent
	}
	if err := connection.SetDeadline(deadline); err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { connection.SetDeadline(time.Now()) })
	defer stop()
	if err := WriteFrame(connection, payload); err != nil {
		return nil, err
	}
	return ReadFrame(connection)
}

func validateMaintenanceReply(payload []byte, id string) error {
	var response maintenanceResponse
	// Ignore additive fields, but validate the frozen required envelope.
	if err := json.Unmarshal(payload, &response); err != nil {
		return err
	}
	if response.Version != MaintenanceVersion || response.ID != id || response.OK == (response.Error != "") {
		return errors.New("invalid maintenance response")
	}
	if !response.OK {
		return fmt.Errorf("daemon refused maintenance: %s", response.Error)
	}
	return nil
}

func identifyMaintenanceReply(payload []byte, id string) (int, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		return 0, err
	}
	if _, exists := fields["maintenanceVersion"]; exists {
		return 0, validateMaintenanceReply(payload, id)
	}
	var response legacyMaintenanceResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		return 0, err
	}
	// Legacy servers explicitly reject the new envelope before dispatch. Never
	// retry after permission denial, a successful/malformed reply, or unknown IPC.
	if (response.Version != 4 && response.Version != 5) || response.ProductVersion == "" ||
		response.OK || response.Error == nil || response.Error.Code != "PROTOCOL_ERROR" ||
		(response.ID != "" && response.ID != id) || len(response.Result) != 0 {
		return 0, errors.New("daemon does not support a recognized maintenance protocol")
	}
	return response.Version, nil
}

func validateLegacyShutdownReply(payload []byte, id string, protocol int) error {
	var response legacyMaintenanceResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		return err
	}
	if response.Version != protocol || response.ProductVersion == "" ||
		(response.ID != id && (response.ID != "" || response.OK)) || response.OK == (response.Error != nil) ||
		(!response.OK && len(response.Result) != 0) {
		return errors.New("invalid legacy shutdown response")
	}
	if !response.OK {
		return fmt.Errorf("daemon refused shutdown: %s (%s)", response.Error.Message, response.Error.Code)
	}
	return nil
}

package ipc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
)

func TestMaintenanceServerIndependentOfBusinessProtocol(t *testing.T) {
	for _, method := range []string{"identify", "shutdown"} {
		t.Run(method, func(t *testing.T) {
			prepared, stopped := false, false
			server := Server{PrepareMaintenanceShutdown: func() (func(), error) {
				prepared = true
				return func() { stopped = true }, nil
			}, Handler: HandlerFunc(func(context.Context, Request) Response { t.Fatal("business handler called"); return Response{} })}
			// This frozen literal must stay usable when ProtocolVersion changes.
			payload := []byte(`{"maintenanceVersion":1,"id":"maintenance-v1","method":"` + method + `"}`)
			var output bytes.Buffer
			if !server.handleMaintenance(&output, payload) {
				t.Fatal("maintenance not recognized")
			}
			frame, err := ReadFrame(&output)
			if err != nil {
				t.Fatal(err)
			}
			var response maintenanceResponse
			if err := json.Unmarshal(frame, &response); err != nil {
				t.Fatal(err)
			}
			if !response.OK || response.Version != 1 || response.ID != "maintenance-v1" {
				t.Fatalf("%s", frame)
			}
			if prepared != (method == "shutdown") || stopped != prepared {
				t.Fatal("unexpected shutdown")
			}
		})
	}
}

func TestMaintenanceRejectsInvalidAndPrivilegedOperations(t *testing.T) {
	for _, payload := range []string{
		`{"maintenanceVersion":2,"id":"id","method":"shutdown"}`,
		`{"maintenanceVersion":1,"id":"","method":"shutdown"}`,
		`{"maintenanceVersion":1,"id":"id","method":"connect"}`,
		`{"maintenanceVersion":1,"id":"id","method":"status"}`,
		`{"maintenanceVersion":1,"id":"id","method":"shutdown","params":{"purge":true}}`,
	} {
		var output bytes.Buffer
		server := Server{PrepareMaintenanceShutdown: func() (func(), error) { t.Fatal("invalid request dispatched"); return nil, nil }}
		if !server.handleMaintenance(&output, []byte(payload)) {
			t.Fatal("not recognized")
		}
		frame, err := ReadFrame(&output)
		if err != nil {
			t.Fatal(err)
		}
		var response maintenanceResponse
		json.Unmarshal(frame, &response)
		if response.OK || response.Error == "" {
			t.Fatalf("accepted %s", payload)
		}
	}
}

type failingMaintenanceWriter struct{}

func (failingMaintenanceWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestMaintenanceAcknowledgesBeforeShutdown(t *testing.T) {
	stopped := false
	server := Server{PrepareMaintenanceShutdown: func() (func(), error) { return func() { stopped = true }, nil }}
	server.handleMaintenance(failingMaintenanceWriter{}, []byte(`{"maintenanceVersion":1,"id":"id","method":"shutdown"}`))
	if stopped {
		t.Fatal("shutdown after failed acknowledgment")
	}
	var output bytes.Buffer
	server.PrepareMaintenanceShutdown = func() (func(), error) { return nil, errors.New("refused") }
	server.handleMaintenance(&output, []byte(`{"maintenanceVersion":1,"id":"id","method":"shutdown"}`))
	if stopped {
		t.Fatal("shutdown despite refusal")
	}
}

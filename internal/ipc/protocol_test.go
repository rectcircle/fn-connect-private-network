package ipc

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

func TestNativeClientIPCVersionsMatchGoProtocol(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate IPC test source")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
	cases := []struct {
		name  string
		path  string
		regex *regexp.Regexp
	}{
		{
			name:  "Windows WPF",
			path:  filepath.Join(root, "platform", "windows", "FnCPN.Windows", "IPC.cs"),
			regex: regexp.MustCompile(`internal const int ProtocolVersion = ([0-9]+);`),
		},
		{
			name:  "macOS app",
			path:  filepath.Join(root, "platform", "macos", "FnCPNApp.swift"),
			regex: regexp.MustCompile(`private let ipcProtocolVersion = ([0-9]+)`),
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			contents, err := os.ReadFile(test.path)
			if err != nil {
				t.Fatalf("read native IPC implementation: %v", err)
			}
			match := test.regex.FindSubmatch(contents)
			if len(match) != 2 {
				t.Fatalf("protocol version declaration was not found in %s", test.path)
			}
			version, err := strconv.Atoi(string(match[1]))
			if err != nil {
				t.Fatalf("parse protocol version: %v", err)
			}
			if version != ProtocolVersion {
				t.Fatalf("native protocol version = %d, Go protocol version = %d", version, ProtocolVersion)
			}
		})
	}
}

func TestWindowsWPFErrorCodesMatchGoModel(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate IPC test source")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
	path := filepath.Join(root, "platform", "windows", "FnCPN.Windows", "IPC.cs")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read Windows IPC implementation: %v", err)
	}
	codeSwitch := regexp.MustCompile(`(?s)private static bool IsKnownErrorCode\(string code\) => code switch\s*\{(.*?)\n\s*\};`).FindSubmatch(contents)
	if len(codeSwitch) != 2 {
		t.Fatal("Windows IPC error-code validator was not found")
	}
	codes := []model.ErrorCode{
		model.ErrorInvalidArgument,
		model.ErrorAuthRequired,
		model.ErrorPermissionDenied,
		model.ErrorNotFound,
		model.ErrorAlreadyExists,
		model.ErrorFailedPrecondition,
		model.ErrorDeviceRevoked,
		model.ErrorUnavailable,
		model.ErrorConflict,
		model.ErrorTimeout,
		model.ErrorCanceled,
		model.ErrorInternal,
		model.ErrorProtocol,
		model.ErrorServerUnavailable,
		model.ErrorVersionIncompatible,
		model.ErrorResourceExhausted,
		model.ErrorDiscoveryFailed,
	}
	for _, code := range codes {
		if !strings.Contains(string(codeSwitch[1]), `"`+string(code)+`"`) {
			t.Errorf("Windows IPC error-code validator does not recognize Go model code %q", code)
		}
	}
}

func TestFrameRoundTrip(t *testing.T) {
	payload := []byte(`{"value":"test"}`)
	var buffer bytes.Buffer
	if err := WriteFrame(&buffer, payload); err != nil {
		t.Fatalf("write frame: %v", err)
	}
	result, err := ReadFrame(&buffer)
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	if !bytes.Equal(result, payload) {
		t.Fatalf("payload = %q, want %q", result, payload)
	}
}

func TestReadFrameRejectsOversizedPayload(t *testing.T) {
	var buffer bytes.Buffer
	if err := WriteFrame(
		&buffer,
		[]byte(strings.Repeat("x", MaxFrameSize+1)),
	); err == nil {
		t.Fatal("oversized frame unexpectedly accepted")
	}
}

func TestRequestAndResponseRoundTrip(t *testing.T) {
	request, err := NewRequest("request-id", "status", struct{}{})
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	var requestBuffer bytes.Buffer
	if err := WriteRequest(&requestBuffer, request); err != nil {
		t.Fatalf("write request: %v", err)
	}
	decodedRequest, err := ReadRequest(&requestBuffer)
	if err != nil {
		t.Fatalf("read request: %v", err)
	}
	if decodedRequest.ID != request.ID || decodedRequest.Method != request.Method {
		t.Fatalf("decoded request = %+v", decodedRequest)
	}

	response := Failure(
		request.ID,
		model.NewError(model.ErrorAuthRequired, "authorization required", false),
	)
	var responseBuffer bytes.Buffer
	if err := WriteResponse(&responseBuffer, response); err != nil {
		t.Fatalf("write response: %v", err)
	}
	decodedResponse, err := ReadResponse(&responseBuffer)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if decodedResponse.OK ||
		decodedResponse.Error == nil ||
		decodedResponse.Error.Code != model.ErrorAuthRequired {
		t.Fatalf("decoded response = %+v", decodedResponse)
	}
}

func TestReadResponseRejectsInvalidEnvelope(t *testing.T) {
	tests := []Response{
		{
			Version: ProtocolVersion,
			ID:      "request",
			OK:      true,
			Error:   model.NewError(model.ErrorInternal, "bad", false),
		},
		{
			Version: ProtocolVersion,
			ID:      "request",
			OK:      false,
			Result:  []byte(`{}`),
			Error:   model.NewError(model.ErrorInternal, "bad", false),
		},
		{
			Version: ProtocolVersion,
			ID:      "request",
			OK:      false,
		},
	}
	for _, response := range tests {
		var buffer bytes.Buffer
		if err := WriteResponse(&buffer, response); err != nil {
			t.Fatalf("write response: %v", err)
		}
		if _, err := ReadResponse(&buffer); err == nil {
			t.Fatalf("invalid response accepted: %+v", response)
		}
	}
}

func TestReadResponseRejectsDifferentLocalProduct(t *testing.T) {
	for _, product := range []string{"", "99.0.0"} {
		response := Success("id", nil)
		response.ProductVersion = product
		var buffer bytes.Buffer
		if err := WriteResponse(&buffer, response); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadResponse(&buffer); err == nil {
			t.Fatalf("accepted local product %q", product)
		}
	}
}

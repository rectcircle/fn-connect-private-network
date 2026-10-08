package ipc

import (
	"bytes"
	"strings"
	"testing"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

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

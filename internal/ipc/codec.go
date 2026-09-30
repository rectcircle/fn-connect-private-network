package ipc

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

func ReadRequest(reader io.Reader) (Request, error) {
	payload, err := ReadFrame(reader)
	if err != nil {
		return Request{}, err
	}
	var request Request
	if err := model.DecodeStrict(payload, &request); err != nil {
		return Request{}, fmt.Errorf("decode IPC request: %w", err)
	}
	if request.ID == "" {
		return Request{}, model.NewError(
			model.ErrorInvalidArgument,
			"request ID is required",
			false,
		)
	}
	if request.Method == "" {
		return Request{}, model.NewError(
			model.ErrorInvalidArgument,
			"request method is required",
			false,
		)
	}
	if err := model.ValidateProtocolVersion(request.Version); err != nil {
		return Request{}, err
	}
	return request, nil
}

func WriteRequest(writer io.Writer, request Request) error {
	payload, err := json.Marshal(request)
	if err != nil {
		return err
	}
	return WriteFrame(writer, payload)
}

func ReadResponse(reader io.Reader) (Response, error) {
	payload, err := ReadFrame(reader)
	if err != nil {
		return Response{}, err
	}
	var response Response
	if err := model.DecodeStrict(payload, &response); err != nil {
		return Response{}, fmt.Errorf("decode IPC response: %w", err)
	}
	if err := model.ValidateProtocolVersion(response.Version); err != nil {
		return Response{}, err
	}
	if response.OK && response.Error != nil {
		return Response{}, errors.New("successful response contains an error")
	}
	if response.OK && len(response.Result) > 0 && string(response.Result) == "null" {
		response.Result = nil
	}
	if !response.OK && response.Error == nil {
		return Response{}, errors.New("failed response does not contain an error")
	}
	if !response.OK && len(response.Result) != 0 {
		return Response{}, errors.New("failed response contains a result")
	}
	if response.Error != nil && response.Error.Code == "" {
		return Response{}, errors.New("failed response contains an invalid error")
	}
	return response, nil
}

func WriteResponse(writer io.Writer, response Response) error {
	payload, err := json.Marshal(response)
	if err != nil {
		return err
	}
	return WriteFrame(writer, payload)
}

package ipc

import (
	"encoding/json"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

const MaxFrameSize = 256 << 10

type Request struct {
	Version int             `json:"version"`
	ID      string          `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type Response struct {
	Version int             `json:"version"`
	ID      string          `json:"id,omitempty"`
	OK      bool            `json:"ok"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *model.Error    `json:"error,omitempty"`
}

func NewRequest(id, method string, params any) (Request, error) {
	request := Request{
		Version: model.ProtocolVersion,
		ID:      id,
		Method:  method,
	}
	if params == nil {
		return request, nil
	}
	data, err := json.Marshal(params)
	if err != nil {
		return Request{}, err
	}
	request.Params = data
	return request, nil
}

func Success(id string, result any) Response {
	response := Response{
		Version: model.ProtocolVersion,
		ID:      id,
		OK:      true,
	}
	if result != nil {
		data, err := json.Marshal(result)
		if err != nil {
			return Failure(
				id,
				model.WrapError(
					model.ErrorInternal,
					"encode IPC result",
					false,
					err,
				),
			)
		}
		response.Result = data
	}
	return response
}

func Failure(id string, err error) Response {
	failure := model.PublicError(err)
	// Keep the local cause until the server logs the failure. Cause is never
	// serialized; only the sanitized Detail crosses the process boundary.
	failure.Cause = err
	if failure.RequestID == "" {
		failure.RequestID = model.SafeRequestID(id)
	}
	return Response{
		Version: model.ProtocolVersion,
		ID:      id,
		OK:      false,
		Error:   failure,
	}
}

func DecodeParams[T any](request Request) (T, error) {
	var value T
	if len(request.Params) == 0 {
		request.Params = json.RawMessage(`{}`)
	}
	if err := model.DecodeStrict(request.Params, &value); err != nil {
		return value, model.WrapError(
			model.ErrorInvalidArgument,
			"invalid request parameters",
			false,
			err,
		)
	}
	return value, nil
}

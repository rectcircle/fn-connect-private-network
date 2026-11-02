package ipc

import "context"

// StreamHandler is optional. Ordinary and maintenance requests still return one frame.
// A streaming method defines its own terminal result and is canceled when its caller disconnects.
type StreamHandler interface {
	HandleStream(context.Context, Request, func(Response) error) bool
}

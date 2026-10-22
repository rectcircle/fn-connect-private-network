package main

import "context"

// daemonClient is the UI's entire dependency on the connection service.
// Both ipc.Client (desktop development) and clienthost.Host implement it.
// Lifecycle methods deliberately do not appear here: the system owns them.
type daemonClient interface {
	Call(context.Context, string, any, any) error
}

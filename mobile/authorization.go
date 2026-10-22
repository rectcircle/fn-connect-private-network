package main

import (
	"context"
	"sync"

	"github.com/rectcircle/fn-connect-private-network/internal/client"
)

// authorizationFlow is accessed by one operation at a time. Additional factors
// must reuse the daemon's pending request rather than start a second login.
type authorizationFlow struct {
	mu          sync.Mutex
	id          string
	fnID        string
	credentials client.NativeAuthorization
}

func (f *authorizationFlow) submit(ctx context.Context, backend daemonClient, input client.NativeAuthorization) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	fnID, err := client.NormalizeFNID(input.FNID)
	if err != nil {
		return err
	}
	if f.id != "" && f.fnID != fnID {
		if err := backend.Call(ctx, client.MethodAuthorizationCancel, client.AuthorizationCancelRequest{RequestID: f.id}, nil); err != nil {
			return err
		}
		f.id = ""
		f.credentials = client.NativeAuthorization{}
	}
	if f.id == "" {
		var pending client.AuthorizationResult
		if err := backend.Call(ctx, client.MethodAuthorizationBegin, client.AuthorizationRequest{FNID: fnID}, &pending); err != nil {
			return err
		}
		f.id, f.fnID = pending.RequestID, fnID
	}
	input.RequestID, input.FNID = f.id, fnID
	if input.Username == "" {
		input.Username = f.credentials.Username
	}
	if input.Password == "" {
		input.Password = f.credentials.Password
	}
	if input.AccessCode == "" {
		input.AccessCode = f.credentials.AccessCode
	}
	err = backend.Call(ctx, client.MethodAuthorizeNative, input, nil)
	if client.RequiresAdditionalCredential(err) {
		input.TwofaCode = ""
		f.credentials = input
	} else {
		f.id, f.fnID = "", ""
		f.credentials = client.NativeAuthorization{}
	}
	return err
}

func (f *authorizationFlow) cancel(ctx context.Context, backend daemonClient) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.id
	f.id, f.fnID = "", ""
	f.credentials = client.NativeAuthorization{}
	if id == "" {
		return nil
	}
	return backend.Call(ctx, client.MethodAuthorizationCancel, client.AuthorizationCancelRequest{RequestID: id}, nil)
}

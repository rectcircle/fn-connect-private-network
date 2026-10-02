package client

import (
	"context"

	"github.com/rectcircle/fn-connect-private-network/internal/ipc"
	"github.com/rectcircle/fn-connect-private-network/internal/privileged"
)

type IPCSecretStore struct {
	Context context.Context
	Client  ipc.Client
}

func (s IPCSecretStore) Get(service, account string) ([]byte, bool, error) {
	var result privileged.SecretResult
	err := s.Client.Call(s.Context, privileged.MethodGetSecret, privileged.SecretRequest{
		Service: service, Account: account,
	}, &result)
	return result.Value, result.Found, err
}

func (s IPCSecretStore) Put(service, account string, value []byte) error {
	return s.Client.Call(s.Context, privileged.MethodPutSecret, privileged.SecretRequest{
		Service: service, Account: account, Value: value,
	}, nil)
}

func (s IPCSecretStore) Delete(service, account string) error {
	return s.Client.Call(s.Context, privileged.MethodDeleteSecret, privileged.SecretRequest{
		Service: service, Account: account,
	}, nil)
}

//go:build !darwin

package platform

import "errors"

func NewClientSecretStore() SecretStore {
	return unavailableSecretStore{}
}

type unavailableSecretStore struct{}

func (unavailableSecretStore) Get(string, string) ([]byte, bool, error) {
	return nil, false, errors.New("client secret store is only available on macOS")
}

func (unavailableSecretStore) Put(string, string, []byte) error {
	return errors.New("client secret store is only available on macOS")
}

func (unavailableSecretStore) Delete(string, string) error {
	return errors.New("client secret store is only available on macOS")
}

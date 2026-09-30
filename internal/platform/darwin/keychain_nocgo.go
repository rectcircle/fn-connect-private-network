//go:build darwin && !cgo

package darwin

import "errors"

type KeychainStore struct{}

func NewKeychainStore() *KeychainStore {
	return &KeychainStore{}
}

func (*KeychainStore) Get(string, string) ([]byte, bool, error) {
	return nil, false, errors.New("macOS Keychain requires a CGO-enabled build")
}

func (*KeychainStore) Put(string, string, []byte) error {
	return errors.New("macOS Keychain requires a CGO-enabled build")
}

func (*KeychainStore) Delete(string, string) error {
	return errors.New("macOS Keychain requires a CGO-enabled build")
}

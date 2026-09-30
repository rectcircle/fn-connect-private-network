//go:build darwin

package platform

import darwinplatform "github.com/rectcircle/fn-connect-private-network/internal/platform/darwin"

func NewClientSecretStore() SecretStore {
	return darwinplatform.NewKeychainStore()
}

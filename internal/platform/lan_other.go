//go:build !linux

package platform

import "context"

func DetectLANCIDRs() ([]string, error) {
	return nil, nil
}

func DetectLANAddresses() ([]string, error) {
	return nil, nil
}

func LANAddressEvents(context.Context) (<-chan struct{}, error) {
	return nil, nil
}

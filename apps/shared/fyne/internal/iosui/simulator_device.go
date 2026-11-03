//go:build ios && !fncpn_ios_simulator

package iosui

import (
	"context"
	"encoding/json"
	"errors"
)

func simulatorCall(context.Context, string, any, any) error {
	return errors.New("simulator backend is not included in this build")
}
func NetworkProbe(context.Context, string) (string, error) {
	return "", errors.New("network probe is simulator-only")
}

func simulatorBrowserAllowed(string) error   { return nil }
func simulatorLogDirectory() (string, error) { return "", errors.New("simulator only") }

func simulatorStream(context.Context, string, any, func(json.RawMessage) (bool, error)) error {
	return errors.New("simulator backend unavailable")
}

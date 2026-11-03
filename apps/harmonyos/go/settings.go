package main

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"

	"github.com/rectcircle/fn-connect-private-network/internal/client"
	"github.com/rectcircle/fn-connect-private-network/internal/version"
)

// Reading and editing saved preferences never starts or authorizes a VPN.
func localSettingsCommand(ctx context.Context, directory, method string, params json.RawMessage) (any, error) {
	store := client.NewConfigStore(filepath.Join(directory, "client.json"), nil)
	switch method {
	case "build-info":
		return map[string]string{"version": version.Current, "commit": version.Revision}, nil
	case client.MethodLinkSettings:
		return store.LinkSettings(ctx)
	case client.MethodSetLinkSettings:
		var input client.LinkSettings
		if err := json.Unmarshal(params, &input); err != nil {
			return nil, err
		}
		return store.SetLinkSettings(ctx, input)
	default:
		return nil, errors.New("unsupported local settings command")
	}
}

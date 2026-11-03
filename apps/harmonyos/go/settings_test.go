package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/rectcircle/fn-connect-private-network/internal/client"
)

func TestNativeSettingsOfflineEditsPreserveConfigurationAndConnectionIntent(t *testing.T) {
	directory := t.TempDir()
	store := client.NewConfigStore(filepath.Join(directory, "client.json"), nil)
	if err := store.Save(client.LocalConfig{FNID: "home-nas", AutoConnect: true}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(directory, "client.json"))
	if err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`{"fnId":"home-nas","links":[{"link":"fn-connect","enabled":false},{"link":"ipv6","enabled":true}]}`)
	if _, err := localSettingsCommand(context.Background(), directory, client.MethodSetLinkSettings, raw); err != nil {
		t.Fatal(err)
	}
	result, err := localSettingsCommand(context.Background(), directory, client.MethodLinkSettings, nil)
	if err != nil {
		t.Fatal(err)
	}
	settings := result.(client.LinkSettings)
	if settings.FNID != "home-nas" || settings.Links[0].Link != "fn-connect" || settings.Links[0].Enabled {
		t.Fatal(settings)
	}
	after, err := os.ReadFile(filepath.Join(directory, "client.json"))
	if err != nil || string(before) != string(after) {
		t.Fatal("settings changed identity or connection intent", err)
	}
	if _, err := os.Stat(filepath.Join(directory, "control.sock")); !os.IsNotExist(err) {
		t.Fatal("offline settings unexpectedly started runtime")
	}
	if _, err := localSettingsCommand(context.Background(), directory, "build-info", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := localSettingsCommand(context.Background(), directory, client.MethodSetLinkSettings, json.RawMessage(`{"fnId":"other-nas","links":[]}`)); err == nil {
		t.Fatal("stale NAS accepted")
	}
}

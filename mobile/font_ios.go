//go:build ios

package main

import (
	"encoding/json"
	"os"
	"path/filepath"

	"fyne.io/fyne/v2"
	"github.com/rectcircle/fn-connect-private-network/mobile/internal/appletext"
)

func init() {
	fyne.SetSystemTextRenderer(appletext.Renderer{})
	if os.Getenv("FNCPN_TEXT_SELFTEST") == "1" {
		report := appletext.Probe()
		if data, err := json.MarshalIndent(report, "", "  "); err == nil {
			_ = os.WriteFile(filepath.Join(os.TempDir(), "FnCPN-text-selftest.json"), data, 0600)
		}
	}
}

// Normal text uses Core Text; Fyne keeps its small bundled symbol-font path.
func platformFont(fyne.TextStyle) fyne.Resource { return nil }

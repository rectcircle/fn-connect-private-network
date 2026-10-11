//go:build windows

package platform

import (
	"context"
	"fmt"

	"golang.org/x/sys/windows"
)

func OpenAuthorization(ctx context.Context, fnID, requestID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	verb, _ := windows.UTF16PtrFromString("open")
	uri, err := windows.UTF16PtrFromString("fncpn://authorize/" + fnID + "?request=" + requestID)
	if err != nil {
		return fmt.Errorf("encode authorization URI: %w", err)
	}
	if err := windows.ShellExecute(0, verb, uri, nil, nil, windows.SW_SHOWNORMAL); err != nil {
		return fmt.Errorf("open FnCPN authorization window: %w", err)
	}
	return nil
}

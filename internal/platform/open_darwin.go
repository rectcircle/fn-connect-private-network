//go:build darwin

package platform

import (
	"context"
	"fmt"
	"os/exec"
)

func OpenAuthorization(ctx context.Context, fnID, requestID string) error {
	if err := exec.CommandContext(
		ctx,
		"/usr/bin/open",
		"fncpn://authorize/"+fnID+"?request="+requestID,
	).Run(); err != nil {
		return fmt.Errorf("open FnCPN authorization window: %w", err)
	}
	return nil
}

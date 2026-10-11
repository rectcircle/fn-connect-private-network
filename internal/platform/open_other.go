//go:build !darwin && !windows

package platform

import (
	"context"
	"errors"
)

func OpenAuthorization(context.Context, string, string) error {
	return errors.New("authorization UI is only available on macOS")
}

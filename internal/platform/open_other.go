//go:build !darwin

package platform

import (
	"context"
	"errors"
)

func OpenAuthorization(context.Context, string, string) error {
	return errors.New("authorization UI is only available on macOS")
}

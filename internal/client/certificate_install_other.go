//go:build !windows && !darwin

package client

import (
	"context"
	"errors"
)

func installISRGRoot(context.Context, []byte) error {
	return errors.New("automatic certificate installation is supported only on Windows and macOS")
}

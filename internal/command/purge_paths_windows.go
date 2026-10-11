//go:build windows

package command

import (
	"path/filepath"
	"strings"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

func validatePurgeUserPaths(dataDirectory, configPath, logDirectory string) error {
	expected := []string{
		defaultClientDataDirectory(),
		defaultClientConfig(),
		filepath.Dir(defaultClientLog()),
	}
	provided := []string{dataDirectory, configPath, logDirectory}
	for index, path := range provided {
		if !filepath.IsAbs(expected[index]) || !filepath.IsAbs(path) {
			return model.NewError(model.ErrorInvalidArgument, "user data purge paths must be absolute Windows profile paths", false)
		}
		actualFull, err := filepath.Abs(filepath.Clean(path))
		if err != nil {
			return model.NewError(model.ErrorInvalidArgument, "invalid user data purge path", false)
		}
		expectedFull, err := filepath.Abs(filepath.Clean(expected[index]))
		if err != nil || !strings.EqualFold(actualFull, expectedFull) {
			return model.NewError(model.ErrorInvalidArgument, "user data purge is limited to the current user's FnCPN profile", false)
		}
	}
	return nil
}

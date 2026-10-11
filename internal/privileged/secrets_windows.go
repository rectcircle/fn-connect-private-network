//go:build windows

package privileged

import (
	"errors"
	"os"
)

// The Windows product stores user secrets with DPAPI CurrentUser and does not
// use this Unix owner/mode based store. Fail closed if an accidental caller
// tries to use the legacy file secret store on Windows.
func checkSecretMetadata(_ os.FileInfo, _ bool, _ os.FileMode) error {
	return errors.New("file based privileged credential storage is unsupported on Windows")
}

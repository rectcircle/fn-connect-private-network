//go:build windows

package client

import "os"

// Windows profiles are protected by their profile directory ACL; POSIX mode
// bits returned by os.FileInfo do not represent the effective Windows ACL.
func isPrivateClientConfig(info os.FileInfo) bool {
	return info.Mode().IsRegular()
}

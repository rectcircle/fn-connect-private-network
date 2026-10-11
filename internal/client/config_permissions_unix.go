//go:build !windows

package client

import "os"

func isPrivateClientConfig(info os.FileInfo) bool {
	return info.Mode().IsRegular() && info.Mode().Perm()&0o077 == 0
}

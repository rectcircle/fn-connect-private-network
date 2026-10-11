//go:build windows

package command

import "golang.org/x/sys/windows"

func isPrivilegedProcess() bool {
	token := windows.GetCurrentProcessToken()
	user, err := token.GetTokenUser()
	return err == nil && user.User.Sid.String() == "S-1-5-18"
}

//go:build darwin

package command

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func readTerminalPassword() ([]byte, error) {
	fd := int(os.Stdin.Fd())
	state, err := unix.IoctlGetTermios(fd, unix.TIOCGETA)
	if err != nil {
		return nil, fmt.Errorf("authorize requires an interactive terminal: %w", err)
	}
	updated := *state
	updated.Lflag &^= unix.ECHO
	if err := unix.IoctlSetTermios(fd, unix.TIOCSETA, &updated); err != nil {
		return nil, fmt.Errorf("disable terminal password echo: %w", err)
	}
	defer func() { _ = unix.IoctlSetTermios(fd, unix.TIOCSETA, state) }()

	password := make([]byte, 0, 64)
	var one [1]byte
	for {
		count, err := os.Stdin.Read(one[:])
		if err != nil {
			clear(password)
			return nil, err
		}
		if count == 0 {
			clear(password)
			return nil, fmt.Errorf("end of input while reading fnOS password")
		}
		if one[0] == '\n' || one[0] == '\r' {
			return password, nil
		}
		if len(password) == 4096 {
			clear(password)
			return nil, fmt.Errorf("fnOS password exceeds 4096 bytes")
		}
		password = append(password, one[0])
	}
}

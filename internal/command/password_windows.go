//go:build windows

package command

import (
	"fmt"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

const enableEchoInput = 0x0004

func readTerminalPassword() ([]byte, error) {
	input, err := windows.GetStdHandle(windows.STD_INPUT_HANDLE)
	if err != nil {
		return nil, fmt.Errorf("authorize requires an interactive terminal: %w", err)
	}
	var mode uint32
	if err := windows.GetConsoleMode(input, &mode); err != nil {
		return nil, fmt.Errorf("authorize requires an interactive terminal: %w", err)
	}
	if err := windows.SetConsoleMode(input, mode&^enableEchoInput); err != nil {
		return nil, fmt.Errorf("disable terminal password echo: %w", err)
	}
	defer func() { _ = windows.SetConsoleMode(input, mode) }()

	buffer := make([]uint16, 4097)
	var count uint32
	if err := windows.ReadConsole(input, &buffer[0], uint32(len(buffer)), &count, nil); err != nil {
		return nil, err
	}
	text := string(utf16.Decode(buffer[:count]))
	for len(text) > 0 && (text[len(text)-1] == '\r' || text[len(text)-1] == '\n') {
		text = text[:len(text)-1]
	}
	if len(text) > 4096 || count == uint32(len(buffer)) {
		return nil, fmt.Errorf("fnOS password exceeds 4096 bytes")
	}
	return []byte(text), nil
}

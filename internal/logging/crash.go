package logging

import (
	"fmt"
	"runtime/debug"
)

// EnableCrashOutput duplicates runtime panic/fatal output to a private file.
// Runtime writes bypass slog and cannot be redacted or rotated while crashing.
// Rotate nonempty output at the next start, retaining DefaultBackups captures.
func EnableCrashOutput(path string) error {
	writer, err := Open(path, DefaultMaxSize, DefaultBackups)
	if err != nil {
		return err
	}
	defer writer.Close()
	if writer.size > 0 {
		if err := writer.rotate(); err != nil {
			return fmt.Errorf("rotate crash output: %w", err)
		}
	}
	// SetCrashOutput duplicates the descriptor, so closing writer is safe and
	// crash capture remains available even after normal log cleanup during panic.
	return debug.SetCrashOutput(writer.file, debug.CrashOptions{})
}

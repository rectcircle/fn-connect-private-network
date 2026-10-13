//go:build windows

package main

import (
	"context"
	"fmt"
	"github.com/rectcircle/fn-connect-private-network/internal/version"
	"github.com/rectcircle/fn-connect-private-network/internal/windowsinstall"
	"io"
	"os"
	"path/filepath"
	"time"
)

func run(ctx context.Context, args []string, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "expected exactly one installer operation")
		return 2
	}
	var err error
	switch args[0] {
	case "version":
		fmt.Fprintln(stderr, version.Current)
		return 0
	case "stop-daemons":
		err = windowsinstall.StopAllClientDaemons()
	case "stop-current-daemon":
		err = windowsinstall.StopCurrentClientDaemon()
	case "cleanup-network":
		err = windowsinstall.CleanupNetwork(ctx)
	default:
		fmt.Fprintln(stderr, "unknown installer operation")
		return 2
	}
	if err != nil {
		fmt.Fprintln(stderr, "fncpn installer:", err)
		return 1
	}
	return 0
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var output io.Writer = os.Stderr
	var logFile *os.File
	if windowsinstall.IsLocalSystem() {
		root := os.Getenv("ProgramData")
		if root == "" {
			root = `C:\ProgramData`
		}
		state := filepath.Join(root, "FnCPN", "Service")
		if windowsinstall.PrepareDataDirectories(state, filepath.Join(state, "Logs", "service.log")) == nil {
			logFile, _ = os.OpenFile(filepath.Join(state, "Logs", fmt.Sprintf("installer-%s-%d.log", time.Now().UTC().Format("20060102T150405.000000000"), os.Getpid())), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if logFile != nil {
				output = io.MultiWriter(os.Stderr, logFile)
			}
		}
	}
	started := time.Now()
	fmt.Fprintf(output, "%s operation=%q start\n", started.UTC().Format(time.RFC3339Nano), os.Args[1:])
	code := run(ctx, os.Args[1:], output)
	fmt.Fprintf(output, "%s exit=%d elapsed=%s\n", time.Now().UTC().Format(time.RFC3339Nano), code, time.Since(started))
	if logFile != nil {
		_ = logFile.Close()
	}
	os.Exit(code)
}

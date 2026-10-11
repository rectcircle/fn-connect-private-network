//go:build darwin

package client

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

func installISRGRoot(ctx context.Context, der []byte) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	keychain, err := exec.CommandContext(ctx, "/usr/bin/security", "default-keychain", "-d", "user").Output()
	if err != nil {
		return fmt.Errorf("locate user keychain: %w", err)
	}
	path := strings.Trim(strings.TrimSpace(string(keychain)), "\"")
	if path == "" {
		return fmt.Errorf("user keychain is unavailable")
	}
	file, err := os.CreateTemp("", "fncpn-isrg-root-*.der")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(der); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	// Without -d, security modifies the current user's trust domain. Restrict
	// the added trust setting to SSL server verification.
	output, err := exec.CommandContext(ctx, "/usr/bin/security", "add-trusted-cert", "-r", "trustRoot",
		"-p", "ssl", "-k", path, file.Name()).CombinedOutput()
	if err != nil {
		return fmt.Errorf("install user root certificate: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

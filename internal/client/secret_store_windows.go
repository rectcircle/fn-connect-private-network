//go:build windows

package client

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"unsafe"

	"github.com/rectcircle/fn-connect-private-network/internal/ipc"
	"github.com/rectcircle/fn-connect-private-network/internal/windowsenv"
	"golang.org/x/sys/windows"
)

type dpapiSecretStore struct {
	directory string
	pathError error
}

const maxDPAPISecretSize = 64 * 1024

func newPlatformSecretStore(_ context.Context, _ ipc.Client) SecretStore {
	root, err := windowsenv.LocalAppDataDirectory()
	if err != nil {
		return dpapiSecretStore{pathError: err}
	}
	return dpapiSecretStore{directory: filepath.Join(root, "FnCPN", "credentials")}
}

func (s dpapiSecretStore) Get(service, account string) ([]byte, bool, error) {
	if s.pathError != nil {
		return nil, false, fmt.Errorf("resolve credential directory: %w", s.pathError)
	}
	if s.directory == "" {
		return nil, false, errors.New("credential directory is unavailable")
	}
	path := s.path(service, account)
	sealed, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if len(sealed) == 0 || len(sealed) > maxDPAPISecretSize+4096 {
		return nil, false, errors.New("DPAPI credential record has an invalid size")
	}
	plain, err := dpapiUnprotect(sealed, secretEntropy(service, account))
	if err != nil {
		return nil, false, fmt.Errorf("decrypt credential with CurrentUser DPAPI: %w", err)
	}
	if len(plain) > maxDPAPISecretSize {
		return nil, false, errors.New("credential exceeds size limit")
	}
	return plain, true, nil
}

func (s dpapiSecretStore) Put(service, account string, value []byte) error {
	if s.pathError != nil {
		return fmt.Errorf("resolve credential directory: %w", s.pathError)
	}
	if s.directory == "" {
		return errors.New("credential directory is unavailable")
	}
	if service == "" || account == "" || len(service) > 128 || len(account) > 256 || len(value) == 0 || len(value) > maxDPAPISecretSize {
		return errors.New("invalid DPAPI credential parameters")
	}
	sealed, err := dpapiProtect(value, secretEntropy(service, account))
	if err != nil {
		return fmt.Errorf("encrypt credential with CurrentUser DPAPI: %w", err)
	}
	if err := os.MkdirAll(s.directory, 0o700); err != nil {
		return fmt.Errorf("create credential directory: %w", err)
	}
	return writeClientFileAtomic(s.path(service, account), sealed)
}

func (s dpapiSecretStore) Delete(service, account string) error {
	if s.pathError != nil {
		return fmt.Errorf("resolve credential directory: %w", s.pathError)
	}
	if s.directory == "" {
		return errors.New("credential directory is unavailable")
	}
	err := os.Remove(s.path(service, account))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (s dpapiSecretStore) path(service, account string) string {
	digest := sha256.Sum256([]byte(service + "\x00" + account))
	return filepath.Join(s.directory, hex.EncodeToString(digest[:])+".dpapi")
}

func secretEntropy(service, account string) []byte {
	digest := sha256.Sum256([]byte("FnCPN credential v1\x00" + service + "\x00" + account))
	return digest[:]
}

func dpapiProtect(plain, entropy []byte) ([]byte, error) {
	input := windows.DataBlob{Size: uint32(len(plain)), Data: &plain[0]}
	optional := windows.DataBlob{Size: uint32(len(entropy)), Data: &entropy[0]}
	var output windows.DataBlob
	if err := windows.CryptProtectData(&input, nil, &optional, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &output); err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(output.Data))))
	sealed := append([]byte(nil), unsafe.Slice(output.Data, output.Size)...)
	runtime.KeepAlive(plain)
	runtime.KeepAlive(entropy)
	return sealed, nil
}

func dpapiUnprotect(sealed, entropy []byte) ([]byte, error) {
	input := windows.DataBlob{Size: uint32(len(sealed)), Data: &sealed[0]}
	optional := windows.DataBlob{Size: uint32(len(entropy)), Data: &entropy[0]}
	var output windows.DataBlob
	if err := windows.CryptUnprotectData(&input, nil, &optional, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &output); err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(output.Data))))
	plain := append([]byte(nil), unsafe.Slice(output.Data, output.Size)...)
	runtime.KeepAlive(sealed)
	runtime.KeepAlive(entropy)
	return plain, nil
}

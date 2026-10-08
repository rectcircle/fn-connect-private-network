package linux

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

const (
	serverStateVersion = 2
	maxServerStateSize = 256 * 1024
)

type fileKeyStore struct {
	path string
}

func (s fileKeyStore) LoadOrCreate() (wgtypes.Key, error) {
	if err := ensurePrivateDirectory(filepath.Dir(s.path)); err != nil {
		return wgtypes.Key{}, err
	}
	data, err := os.ReadFile(s.path)
	if err == nil {
		info, statErr := os.Stat(s.path)
		if statErr != nil {
			return wgtypes.Key{}, fmt.Errorf("stat server private key: %w", statErr)
		}
		if info.Mode().Perm()&0o077 != 0 {
			return wgtypes.Key{}, errors.New(
				"server private key permissions must not allow group or other access",
			)
		}
		key, parseErr := wgtypes.ParseKey(strings.TrimSpace(string(data)))
		if parseErr != nil {
			return wgtypes.Key{}, fmt.Errorf("parse server private key: %w", parseErr)
		}
		return key, nil
	}
	if !os.IsNotExist(err) {
		return wgtypes.Key{}, fmt.Errorf("read server private key: %w", err)
	}
	key, err := wgtypes.GeneratePrivateKey()
	if err != nil {
		return wgtypes.Key{}, fmt.Errorf("generate server private key: %w", err)
	}
	if err := writePrivateFileAtomic(s.path, []byte(key.String()+"\n")); err != nil {
		return wgtypes.Key{}, fmt.Errorf("persist server private key: %w", err)
	}
	return key, nil
}

type fileServerStateStore struct {
	path string
}

func (s fileServerStateStore) Load() (*persistedServerState, error) {
	file, err := os.Open(s.path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open server network state: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat server network state: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("server network state permissions are invalid")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxServerStateSize+1))
	if err != nil {
		return nil, fmt.Errorf("read server network state: %w", err)
	}
	if len(data) > maxServerStateSize {
		return nil, errors.New("server network state exceeds size limit")
	}
	var state persistedServerState
	if err := model.DecodeStrict(data, &state); err != nil {
		return nil, fmt.Errorf("decode server network state: %w", err)
	}
	if err := validatePersistedServerState(state); err != nil {
		return nil, err
	}
	return &state, nil
}

func (s fileServerStateStore) Save(state persistedServerState) error {
	if err := validatePersistedServerState(state); err != nil {
		return err
	}
	if err := ensurePrivateDirectory(filepath.Dir(s.path)); err != nil {
		return err
	}
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode server network state: %w", err)
	}
	return writePrivateFileAtomic(s.path, append(data, '\n'))
}

func (s fileServerStateStore) Clear() error {
	if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove server network state: %w", err)
	}
	return syncPrivateDirectory(filepath.Dir(s.path))
}

func validatePersistedServerState(state persistedServerState) error {
	if state.Version != serverStateVersion {
		return fmt.Errorf("unsupported server network state version %d", state.Version)
	}
	if state.OwnerToken == "" ||
		state.InterfaceName == "" ||
		state.FirewallTable == "" {
		return errors.New("server network cleanup journal is incomplete")
	}
	return nil
}

func ensurePrivateDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("create private directory: %w", err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return fmt.Errorf("set private directory mode: %w", err)
	}
	return nil
}

func writePrivateFileAtomic(path string, data []byte) error {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	return syncPrivateDirectory(directory)
}

func syncPrivateDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

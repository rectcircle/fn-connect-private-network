package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

const stateFileName = "state.json"
const stateVersion = 1

type stateDocument struct {
	Version int               `json:"version"`
	State   model.ServerState `json:"state"`
}

type Store struct {
	dir        string
	syncDir    func(string) error
	renameFile func(string, string) error
}

type NetworkUpdate struct {
	OverlayCIDR *string
	ListenPort  *uint16
	LANCIDRs    *[]string
}

func openStore(dir string) (*Store, model.ServerState, error) {
	if dir == "" {
		return nil, model.ServerState{}, errors.New("server state directory is required")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, model.ServerState{}, fmt.Errorf("create server state directory: %w", err)
	}
	store := &Store{dir: dir, syncDir: syncDirectory, renameFile: os.Rename}
	data, found, err := readFile(filepath.Join(dir, stateFileName))
	if err != nil {
		return nil, model.ServerState{}, err
	}
	var state model.ServerState
	if found {
		var document stateDocument
		if err := model.DecodeStrict(data, &document); err != nil {
			return nil, state, fmt.Errorf("decode server state: %w", err)
		}
		if document.Version != stateVersion {
			return nil, state, fmt.Errorf("unsupported state schema version %d", document.Version)
		}
		state = document.State
	} else {
		for _, name := range []string{"state.transaction.json", "settings.json", "devices.json"} {
			if _, statErr := os.Stat(filepath.Join(dir, name)); statErr == nil {
				return nil, state, fmt.Errorf("unsupported development data %s; back up and reset before initializing", name)
			} else if !os.IsNotExist(statErr) {
				return nil, state, statErr
			}
		}
		state = model.ServerState{Settings: model.ServerSettings{OverlayCIDR: DefaultOverlayCIDR, ListenPort: DefaultListenPort}, Devices: []model.Device{}}
	}
	if err := validateState(state); err != nil {
		return nil, state, err
	}
	if !found {
		if _, err := store.save(state); err != nil {
			return nil, state, err
		}
	}
	return store, state, nil
}

// Renaming is the commit point, even when the subsequent directory sync fails.
func (s *Store) save(state model.ServerState) (bool, error) {
	if err := validateState(state); err != nil {
		return false, err
	}
	return writeJSONAtomicWith(
		filepath.Join(s.dir, stateFileName),
		stateDocument{Version: stateVersion, State: state},
		s.renameFile, s.syncDir,
	)
}

func validateState(state model.ServerState) error {
	if err := ValidateSettings(state.Settings); err != nil {
		return err
	}
	overlay, _ := netip.ParsePrefix(state.Settings.OverlayCIDR)
	serverAddress, _ := ServerAddress(state.Settings.OverlayCIDR)
	serverPrefix, _ := netip.ParsePrefix(serverAddress)
	ids := make(map[string]struct{}, len(state.Devices))
	keys := make(map[string]struct{}, len(state.Devices))
	addresses := make(map[netip.Addr]struct{}, len(state.Devices))
	for _, device := range state.Devices {
		if device.ID == "" {
			return errors.New("device ID is required")
		}
		if _, exists := ids[device.ID]; exists {
			return fmt.Errorf("duplicate device ID %q", device.ID)
		}
		ids[device.ID] = struct{}{}
		if strings.TrimSpace(device.Name) == "" || len(device.Name) > 64 {
			return fmt.Errorf("invalid device name for %q", device.ID)
		}
		if err := ValidatePublicKey(device.PublicKey); err != nil {
			return fmt.Errorf("device %q: %w", device.ID, err)
		}
		if _, exists := keys[device.PublicKey]; exists {
			return fmt.Errorf("duplicate public key for device %q", device.ID)
		}
		keys[device.PublicKey] = struct{}{}
		address, err := netip.ParsePrefix(device.OverlayAddress)
		if err != nil || address.Bits() != 32 || !overlay.Contains(address.Addr()) {
			return fmt.Errorf("invalid overlay address for device %q", device.ID)
		}
		if address.Addr() == overlay.Addr() || address.Addr() == serverPrefix.Addr() ||
			!overlay.Contains(address.Addr().Next()) {
			return fmt.Errorf("device %q uses a reserved overlay address", device.ID)
		}
		if _, exists := addresses[address.Addr()]; exists {
			return fmt.Errorf("duplicate overlay address for device %q", device.ID)
		}
		addresses[address.Addr()] = struct{}{}
	}
	return nil
}

func allocateLowestAddress(overlayCIDR string, devices []model.Device) (string, error) {
	overlay, err := parseOverlay(overlayCIDR)
	if err != nil {
		return "", err
	}
	used := make(map[netip.Addr]struct{}, len(devices))
	for _, device := range devices {
		address, err := netip.ParsePrefix(device.OverlayAddress)
		if err != nil {
			return "", fmt.Errorf("parse device address: %w", err)
		}
		used[address.Addr()] = struct{}{}
	}
	for address := overlay.Addr().Next().Next(); overlay.Contains(address.Next()); address = address.Next() {
		if _, exists := used[address]; !exists {
			return netip.PrefixFrom(address, 32).String(), nil
		}
	}
	return "", errors.New("overlay address pool is exhausted")
}

func cloneState(state model.ServerState) model.ServerState {
	cloned := state
	cloned.Settings.LANCIDRs = append([]string(nil), state.Settings.LANCIDRs...)
	cloned.Devices = append([]model.Device(nil), state.Devices...)
	for index := range cloned.Devices {
		if cloned.Devices[index].LastHandshake != nil {
			value := *cloned.Devices[index].LastHandshake
			cloned.Devices[index].LastHandshake = &value
		}
	}
	return cloned
}

func newID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func readFile(path string) ([]byte, bool, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}
	return data, true, nil
}

func writeJSONAtomic(path string, value any) error {
	_, err := writeJSONAtomicWith(path, value, os.Rename, syncDirectory)
	return err
}

func writeJSONAtomicWith(
	path string, value any,
	rename func(string, string) error, syncDir func(string) error,
) (bool, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return false, err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".state-*.tmp")
	if err != nil {
		return false, err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, err := file.Write(append(data, '\n')); err != nil {
		return false, err
	}
	if err := file.Sync(); err != nil {
		return false, err
	}
	if err := file.Close(); err != nil {
		return false, err
	}
	if err := rename(file.Name(), path); err != nil {
		return false, err
	}
	return true, syncDir(filepath.Dir(path))
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

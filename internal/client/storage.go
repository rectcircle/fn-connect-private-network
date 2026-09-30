package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
	wgconfig "github.com/rectcircle/fn-connect-private-network/internal/wireguard"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

const (
	localConfigVersion = 2
	maxLocalConfigSize = 256 * 1024
	wireGuardSecret    = "com.rectcircle.fncpn.wireguard"
	cookieSecret       = "com.rectcircle.fncpn.cookies"
)

type Cookie struct {
	Name     string    `json:"name"`
	Value    string    `json:"value"`
	Domain   string    `json:"domain"`
	Path     string    `json:"path"`
	Secure   bool      `json:"secure"`
	HTTPOnly bool      `json:"httpOnly"`
	HostOnly bool      `json:"hostOnly"`
	SameSite string    `json:"sameSite,omitempty"`
	Expires  time.Time `json:"expires,omitempty"`
}

type LocalConfig struct {
	Version       int                       `json:"version"`
	FNID          string                    `json:"fnId"`
	DeviceID      string                    `json:"deviceId,omitempty"`
	PublicKey     string                    `json:"publicKey,omitempty"`
	Configuration model.ClientConfiguration `json:"configuration"`
	AutoConnect   bool                      `json:"autoConnect"`
}

type SecretStore interface {
	Get(service, account string) ([]byte, bool, error)
	Put(service, account string, value []byte) error
	Delete(service, account string) error
}

type ConfigStore struct {
	path    string
	secrets SecretStore
}

func NewConfigStore(path string, secrets SecretStore) *ConfigStore {
	return &ConfigStore{path: path, secrets: secrets}
}

func (s *ConfigStore) Load() (*LocalConfig, error) {
	file, err := os.Open(s.path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open client config: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat client config: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("client config permissions are invalid")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxLocalConfigSize+1))
	if err != nil {
		return nil, fmt.Errorf("read client config: %w", err)
	}
	if len(data) > maxLocalConfigSize {
		return nil, errors.New("client config exceeds size limit")
	}
	var config LocalConfig
	if err := model.DecodeStrict(data, &config); err != nil {
		return nil, fmt.Errorf("decode client config: %w", err)
	}
	if err := validateLocalConfig(config); err != nil {
		return nil, err
	}
	return &config, nil
}

func (s *ConfigStore) Save(config LocalConfig) error {
	if config.Version == 0 {
		config.Version = localConfigVersion
	}
	if err := validateLocalConfig(config); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("create client config directory: %w", err)
	}
	if err := os.Chmod(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("set client config directory mode: %w", err)
	}
	data, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("encode client config: %w", err)
	}
	return writeClientFileAtomic(s.path, append(data, '\n'))
}

func (s *ConfigStore) Clear() error {
	if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove client config: %w", err)
	}
	return nil
}

func (s *ConfigStore) EnsureWireGuardKey(fnID string) (wgtypes.Key, error) {
	if s.secrets == nil {
		return wgtypes.Key{}, errors.New("client secret store is unavailable")
	}
	account, err := normalizeFNID(fnID)
	if err != nil {
		return wgtypes.Key{}, err
	}
	data, found, err := s.secrets.Get(wireGuardSecret, account)
	if err != nil {
		return wgtypes.Key{}, fmt.Errorf("read WireGuard private key: %w", err)
	}
	if found {
		key, err := wgtypes.ParseKey(strings.TrimSpace(string(data)))
		if err != nil {
			return wgtypes.Key{}, fmt.Errorf("parse WireGuard private key: %w", err)
		}
		return key, nil
	}
	key, err := wgtypes.GeneratePrivateKey()
	if err != nil {
		return wgtypes.Key{}, fmt.Errorf("generate WireGuard private key: %w", err)
	}
	if err := s.secrets.Put(
		wireGuardSecret,
		account,
		[]byte(key.String()),
	); err != nil {
		return wgtypes.Key{}, fmt.Errorf("save WireGuard private key: %w", err)
	}
	return key, nil
}

func (s *ConfigStore) SaveCookies(fnID string, cookies []Cookie) error {
	if s.secrets == nil {
		return errors.New("client secret store is unavailable")
	}
	account, err := normalizeFNID(fnID)
	if err != nil {
		return err
	}
	data, err := json.Marshal(cookies)
	if err != nil {
		return fmt.Errorf("encode cookies: %w", err)
	}
	return s.secrets.Put(cookieSecret, account, data)
}

func (s *ConfigStore) LoadCookies(fnID string) ([]Cookie, error) {
	if s.secrets == nil {
		return nil, errors.New("client secret store is unavailable")
	}
	account, err := normalizeFNID(fnID)
	if err != nil {
		return nil, err
	}
	data, found, err := s.secrets.Get(cookieSecret, account)
	if err != nil {
		return nil, fmt.Errorf("read cookies: %w", err)
	}
	if !found {
		return nil, nil
	}
	var cookies []Cookie
	if err := model.DecodeStrict(data, &cookies); err != nil {
		return nil, fmt.Errorf("decode cookies: %w", err)
	}
	return cookies, nil
}

func (s *ConfigStore) ClearCookies(fnID string) error {
	if s.secrets == nil {
		return errors.New("client secret store is unavailable")
	}
	account, err := normalizeFNID(fnID)
	if err != nil {
		return err
	}
	return s.secrets.Delete(cookieSecret, account)
}

func (s *ConfigStore) Forget(fnID string) error {
	account, err := normalizeFNID(fnID)
	if err != nil {
		return err
	}
	var cleanupErrors []error
	if s.secrets != nil {
		cleanupErrors = append(
			cleanupErrors,
			s.secrets.Delete(wireGuardSecret, account),
			s.secrets.Delete(cookieSecret, account),
		)
	}
	cleanupErrors = append(cleanupErrors, s.Clear())
	return errors.Join(cleanupErrors...)
}

func validateLocalConfig(config LocalConfig) error {
	if config.Version != localConfigVersion {
		return fmt.Errorf("unsupported client config version %d", config.Version)
	}
	if _, err := normalizeFNID(config.FNID); err != nil {
		return err
	}
	if config.DeviceID == "" {
		return nil
	}
	if config.PublicKey == "" {
		return errors.New("client public key is required")
	}
	if err := wgconfig.ValidateKey(config.PublicKey); err != nil {
		return fmt.Errorf("invalid client public key: %w", err)
	}
	if config.Configuration.DeviceID != config.DeviceID {
		return errors.New("client configuration device ID mismatch")
	}
	if _, err := wgconfig.NormalizeClientConfiguration(config.Configuration); err != nil {
		return fmt.Errorf("invalid client configuration: %w", err)
	}
	return nil
}

func normalizeFNID(value string) (string, error) {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.TrimPrefix(value, "https://")
	value = strings.TrimPrefix(value, "http://")
	value = strings.TrimSuffix(value, "/")
	if strings.HasSuffix(value, ".fnos.net") {
		value = strings.TrimSuffix(value, ".fnos.net")
	}
	if value == "" || len(value) > 63 {
		return "", errors.New("invalid FN ID")
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') &&
			(character < '0' || character > '9') &&
			character != '-' {
			return "", errors.New("invalid FN ID")
		}
	}
	if value[0] == '-' || value[len(value)-1] == '-' {
		return "", errors.New("invalid FN ID")
	}
	return value, nil
}

func NormalizeFNID(value string) (string, error) {
	return normalizeFNID(value)
}

func writeClientFileAtomic(path string, data []byte) error {
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
	directoryHandle, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer directoryHandle.Close()
	return directoryHandle.Sync()
}

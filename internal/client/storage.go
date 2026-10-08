package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"github.com/rectcircle/fn-connect-private-network/internal/privileged"
	wgconfig "github.com/rectcircle/fn-connect-private-network/internal/wireguard"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

const (
	localConfigVersion  = 2
	maxLocalConfigSize  = 256 * 1024
	wireGuardSecret     = privileged.WireGuardSecret
	cookieSecret        = privileged.CookieSecret
	nativeSessionSecret = privileged.NativeSessionSecret
	adminWebSecret      = privileged.AdminWebSecret
	localProbeSecret    = privileged.LocalProbeSecret
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
	// Administrator records whether the account that authorized this device has
	// fnOS management privileges. It is refreshed on every AuthorizeNative and
	// gates access to the management webview (admin-only).
	Administrator bool `json:"administrator,omitempty"`
}

type SecretStore interface {
	Get(service, account string) ([]byte, bool, error)
	Put(service, account string, value []byte) error
	Delete(service, account string) error
}

type ConfigStore struct {
	path         string
	secrets      SecretStore
	credentialMu sync.Mutex
}

func NewConfigStore(path string, secrets SecretStore) *ConfigStore {
	return &ConfigStore{path: path, secrets: secrets}
}

func (s *ConfigStore) Load() (_ *LocalConfig, failure error) {
	defer func() {
		if failure != nil {
			failure = model.WithOperation(failure, "configuration.load")
		}
	}()
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
		return nil, model.NewError(model.ErrorFailedPrecondition, "client config permissions are invalid", false)
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
		return nil, model.WrapError(model.ErrorFailedPrecondition, "invalid client configuration JSON", false, err)
	}
	if err := validateLocalConfig(config); err != nil {
		return nil, err
	}
	return &config, nil
}

func (s *ConfigStore) Save(config LocalConfig) (failure error) {
	defer func() {
		if failure != nil {
			failure = model.WithOperation(failure, "configuration.save")
		}
	}()
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

func (s *ConfigStore) Clear() (failure error) {
	defer func() {
		if failure != nil {
			failure = model.WithOperation(failure, "configuration.clear")
		}
	}()
	if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove client config: %w", err)
	}
	return nil
}

func (s *ConfigStore) EnsureWireGuardKey(fnID string) (_ wgtypes.Key, failure error) {
	defer func() {
		if failure != nil {
			failure = model.WithOperation(failure, "credentials.wireguard")
		}
	}()
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

func (s *ConfigStore) SaveCookies(fnID string, cookies []Cookie) (failure error) {
	s.credentialMu.Lock()
	defer s.credentialMu.Unlock()
	return s.saveCookies(fnID, cookies)
}

func (s *ConfigStore) saveCookies(fnID string, cookies []Cookie) (failure error) {
	defer func() {
		if failure != nil {
			failure = model.WithOperation(failure, "credentials.save_cookies")
		}
	}()
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

func (s *ConfigStore) LoadCookies(fnID string) (_ []Cookie, failure error) {
	s.credentialMu.Lock()
	defer s.credentialMu.Unlock()
	return s.loadCookies(fnID)
}

func (s *ConfigStore) loadCookies(fnID string) (_ []Cookie, failure error) {
	defer func() {
		if failure != nil {
			failure = model.WithOperation(failure, "credentials.load_cookies")
		}
	}()
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

func (s *ConfigStore) ClearCookies(fnID string) (failure error) {
	s.credentialMu.Lock()
	defer s.credentialMu.Unlock()
	defer func() {
		if failure != nil {
			failure = model.WithOperation(failure, "credentials.clear_cookies")
		}
	}()
	if s.secrets == nil {
		return errors.New("client secret store is unavailable")
	}
	account, err := normalizeFNID(fnID)
	if err != nil {
		return err
	}
	return s.secrets.Delete(cookieSecret, account)
}

// Merge only the response's changes, and never overwrite a newer session value.
func (s *ConfigStore) MergeCookies(fnID string, before, after []Cookie) error {
	s.credentialMu.Lock()
	defer s.credentialMu.Unlock()
	current, err := s.loadCookies(fnID)
	if err != nil {
		return err
	}
	merged := mergeCookies(current, before, after)
	return s.saveCookies(fnID, merged)
}

func (s *ConfigStore) SaveNativeSession(session NativeSession) (failure error) {
	s.credentialMu.Lock()
	defer s.credentialMu.Unlock()
	defer func() {
		if failure != nil {
			failure = model.WithOperation(failure, "credentials.save_native_session")
		}
	}()
	if s.secrets == nil {
		return errors.New("client secret store is unavailable")
	}
	if err := validateNativeSession(session); err != nil {
		return err
	}
	data, err := json.Marshal(session)
	if err != nil {
		return fmt.Errorf("encode native session: %w", err)
	}
	return s.secrets.Put(nativeSessionSecret, session.FNID, data)
}

func (s *ConfigStore) LoadNativeSession(fnID string) (_ NativeSession, found bool, failure error) {
	s.credentialMu.Lock()
	defer s.credentialMu.Unlock()
	defer func() {
		if failure != nil {
			failure = model.WithOperation(failure, "credentials.load_native_session")
		}
	}()
	if s.secrets == nil {
		return NativeSession{}, false, errors.New("client secret store is unavailable")
	}
	account, err := normalizeFNID(fnID)
	if err != nil {
		return NativeSession{}, false, err
	}
	data, found, err := s.secrets.Get(nativeSessionSecret, account)
	if err != nil {
		return NativeSession{}, false, fmt.Errorf("read native session: %w", err)
	}
	if !found {
		return NativeSession{}, false, nil
	}
	var session NativeSession
	if err := model.DecodeStrict(data, &session); err != nil {
		return NativeSession{}, false, fmt.Errorf("decode native session: %w", err)
	}
	if err := validateNativeSession(session); err != nil {
		return NativeSession{}, false, err
	}
	if session.FNID != account {
		return NativeSession{}, false, errors.New("native session account mismatch")
	}
	return session, true, nil
}

func (s *ConfigStore) ClearNativeSession(fnID string) (failure error) {
	s.credentialMu.Lock()
	defer s.credentialMu.Unlock()
	defer func() {
		if failure != nil {
			failure = model.WithOperation(failure, "credentials.clear_native_session")
		}
	}()
	if s.secrets == nil {
		return errors.New("client secret store is unavailable")
	}
	account, err := normalizeFNID(fnID)
	if err != nil {
		return err
	}
	return s.secrets.Delete(nativeSessionSecret, account)
}

func (s *ConfigStore) SaveAdminWebSession(session AdminWebSession) (failure error) {
	s.credentialMu.Lock()
	defer s.credentialMu.Unlock()
	defer func() {
		if failure != nil {
			failure = model.WithOperation(failure, "credentials.save_admin_web_session")
		}
	}()
	if s.secrets == nil {
		return errors.New("client secret store is unavailable")
	}
	if err := validateAdminWebSession(session); err != nil {
		return err
	}
	data, err := json.Marshal(session)
	if err != nil {
		return fmt.Errorf("encode admin Web session: %w", err)
	}
	return s.secrets.Put(adminWebSecret, session.FNID, data)
}

func (s *ConfigStore) LoadAdminWebSession(
	fnID string,
) (_ AdminWebSession, found bool, failure error) {
	s.credentialMu.Lock()
	defer s.credentialMu.Unlock()
	defer func() {
		if failure != nil {
			failure = model.WithOperation(failure, "credentials.load_admin_web_session")
		}
	}()
	if s.secrets == nil {
		return AdminWebSession{}, false, errors.New("client secret store is unavailable")
	}
	account, err := normalizeFNID(fnID)
	if err != nil {
		return AdminWebSession{}, false, err
	}
	data, found, err := s.secrets.Get(adminWebSecret, account)
	if err != nil {
		return AdminWebSession{}, false, fmt.Errorf("read admin Web session: %w", err)
	}
	if !found {
		return AdminWebSession{}, false, nil
	}
	var session AdminWebSession
	if err := model.DecodeStrict(data, &session); err != nil {
		return AdminWebSession{}, false, fmt.Errorf("decode admin Web session: %w", err)
	}
	if err := validateAdminWebSession(session); err != nil {
		return AdminWebSession{}, false, err
	}
	if session.FNID != account {
		return AdminWebSession{}, false, errors.New("admin Web session account mismatch")
	}
	return session, true, nil
}

func (s *ConfigStore) ClearAdminWebSession(fnID string) (failure error) {
	s.credentialMu.Lock()
	defer s.credentialMu.Unlock()
	defer func() {
		if failure != nil {
			failure = model.WithOperation(failure, "credentials.clear_admin_web_session")
		}
	}()
	if s.secrets == nil {
		return errors.New("client secret store is unavailable")
	}
	account, err := normalizeFNID(fnID)
	if err != nil {
		return err
	}
	return s.secrets.Delete(adminWebSecret, account)
}

// persistedLocalProbe wraps the cached LAN probe configuration together with the
// time it was last validated. It lets the manager reuse the LAN endpoints without
// hitting the public gateway first, while still knowing when the cache is stale.
type persistedLocalProbe struct {
	Configuration model.LocalProbeConfiguration `json:"configuration"`
	CheckedAt     time.Time                     `json:"checkedAt,omitempty"`
}

func (s *ConfigStore) SaveLocalProbeConfig(fnID string, cfg model.LocalProbeConfiguration) (failure error) {
	s.credentialMu.Lock()
	defer s.credentialMu.Unlock()
	defer func() {
		if failure != nil {
			failure = model.WithOperation(failure, "credentials.save_local_probe")
		}
	}()
	if s.secrets == nil {
		return errors.New("client secret store is unavailable")
	}
	if !validLocalProbeConfig(cfg) {
		return errors.New("invalid local probe configuration")
	}
	account, err := normalizeFNID(fnID)
	if err != nil {
		return err
	}
	data, err := json.Marshal(persistedLocalProbe{Configuration: cfg, CheckedAt: time.Now().UTC()})
	if err != nil {
		return fmt.Errorf("encode local probe configuration: %w", err)
	}
	return s.secrets.Put(localProbeSecret, account, data)
}

func (s *ConfigStore) LoadLocalProbeConfig(
	fnID string,
) (_ model.LocalProbeConfiguration, found bool, checkedAt time.Time, failure error) {
	s.credentialMu.Lock()
	defer s.credentialMu.Unlock()
	defer func() {
		if failure != nil {
			failure = model.WithOperation(failure, "credentials.load_local_probe")
		}
	}()
	if s.secrets == nil {
		return model.LocalProbeConfiguration{}, false, time.Time{}, errors.New("client secret store is unavailable")
	}
	account, err := normalizeFNID(fnID)
	if err != nil {
		return model.LocalProbeConfiguration{}, false, time.Time{}, err
	}
	data, found, err := s.secrets.Get(localProbeSecret, account)
	if err != nil {
		return model.LocalProbeConfiguration{}, false, time.Time{}, fmt.Errorf("read local probe configuration: %w", err)
	}
	if !found {
		return model.LocalProbeConfiguration{}, false, time.Time{}, nil
	}
	var persisted persistedLocalProbe
	if err := model.DecodeStrict(data, &persisted); err != nil {
		return model.LocalProbeConfiguration{}, false, time.Time{}, fmt.Errorf("decode local probe configuration: %w", err)
	}
	if !validLocalProbeConfig(persisted.Configuration) {
		// Silently treat an invalid cache as absent so the caller refetches.
		return model.LocalProbeConfiguration{}, false, time.Time{}, nil
	}
	return persisted.Configuration, true, persisted.CheckedAt, nil
}

func (s *ConfigStore) ClearLocalProbeConfig(fnID string) (failure error) {
	s.credentialMu.Lock()
	defer s.credentialMu.Unlock()
	defer func() {
		if failure != nil {
			failure = model.WithOperation(failure, "credentials.clear_local_probe")
		}
	}()
	if s.secrets == nil {
		return errors.New("client secret store is unavailable")
	}
	account, err := normalizeFNID(fnID)
	if err != nil {
		return err
	}
	return s.secrets.Delete(localProbeSecret, account)
}

func validLocalProbeConfig(cfg model.LocalProbeConfiguration) bool {
	if len(cfg.Endpoints) == 0 || cfg.Key == "" {
		return false
	}
	for _, endpoint := range cfg.Endpoints {
		if _, _, err := net.SplitHostPort(endpoint); err != nil {
			return false
		}
	}
	return true
}

func (s *ConfigStore) SaveNativeGatewayCookies(fnID, token string) error {
	s.credentialMu.Lock()
	defer s.credentialMu.Unlock()
	current, err := s.loadCookies(fnID)
	if err != nil {
		return err
	}
	current = slices.DeleteFunc(current, func(cookie Cookie) bool {
		return cookie.Name == "mode" || cookie.Name == "fnos-token"
	})
	for _, cookie := range nativeGatewayCookies(fnID, token) {
		current = append(current, cookie)
	}
	return s.saveCookies(fnID, current)
}

func (s *ConfigStore) Forget(fnID string) (failure error) {
	defer func() {
		if failure != nil {
			failure = model.WithOperation(failure, "credentials.forget")
		}
	}()
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
			s.secrets.Delete(nativeSessionSecret, account),
			s.secrets.Delete(adminWebSecret, account),
			s.secrets.Delete(localProbeSecret, account),
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

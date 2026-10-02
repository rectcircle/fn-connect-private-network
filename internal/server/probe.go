package server

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

const (
	DefaultLocalProbePort  uint16 = 54790
	probeMasterKeyFileName        = "probe.key"
	probeKeySize                  = 32
	probeNonceSize                = 32
	maxProbeRequestSize           = 4 * 1024
)

type LocalProbeService struct {
	mu        sync.RWMutex
	store     *Service
	masterKey []byte
	port      uint16
	listeners map[string]localProbeListener
	endpoints []string
	errors    chan error
	closed    bool
	listen    func(string, string) (net.Listener, error)
}

type localProbeListener struct {
	listener net.Listener
	server   *http.Server
}

func OpenLocalProbeService(
	stateDirectory string,
	store *Service,
	port uint16,
) (*LocalProbeService, error) {
	if store == nil {
		return nil, errors.New("local probe store is required")
	}
	if port == 0 {
		port = DefaultLocalProbePort
	}
	masterKey, err := loadOrCreateProbeMasterKey(stateDirectory)
	if err != nil {
		return nil, err
	}
	service := &LocalProbeService{
		store:     store,
		masterKey: masterKey,
		port:      port,
		listeners: make(map[string]localProbeListener),
		errors:    make(chan error, 1),
		listen:    net.Listen,
	}
	return service, nil
}

func (s *LocalProbeService) Run(
	ctx context.Context,
	detect func() ([]string, error),
	events <-chan struct{},
	logger *slog.Logger,
) {
	reconcile := func() {
		addresses, err := detect()
		if err == nil {
			err = s.Reconcile(addresses)
		}
		if err != nil {
			logger.Error("reconcile local probe listeners", "error", err)
		}
	}
	reconcile()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			reconcile()
		case <-ticker.C:
			reconcile()
		case err := <-s.errors:
			logger.Error("serve local probes", "error", err)
			reconcile()
		}
	}
}

func (s *LocalProbeService) Reconcile(addresses []string) error {
	seen := make(map[netip.Addr]struct{}, len(addresses))
	for _, value := range addresses {
		address, parseErr := netip.ParseAddr(value)
		if parseErr != nil || !address.Is4() || !address.IsPrivate() {
			return fmt.Errorf("invalid local probe address %q", value)
		}
		seen[address] = struct{}{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("local probe service is closed")
	}
	next := make(map[string]localProbeListener, len(seen))
	opened := make(map[string]localProbeListener, len(seen))
	for address := range seen {
		endpoint := net.JoinHostPort(address.String(), fmt.Sprint(s.port))
		if existing, ok := s.listeners[endpoint]; ok {
			next[endpoint] = existing
			continue
		}
		listener, listenErr := s.listen("tcp4", endpoint)
		if listenErr != nil {
			for _, entry := range opened {
				_ = entry.listener.Close()
			}
			return fmt.Errorf("listen for local probes on %s: %w", endpoint, listenErr)
		}
		entry := localProbeListener{
			listener: listener,
			server: &http.Server{
				Handler:           s,
				ReadHeaderTimeout: 2 * time.Second,
				ReadTimeout:       2 * time.Second,
				WriteTimeout:      2 * time.Second,
				IdleTimeout:       5 * time.Second,
				MaxHeaderBytes:    4 * 1024,
			},
		}
		next[endpoint] = entry
		opened[endpoint] = entry
	}
	var removed []localProbeListener
	for endpoint, entry := range s.listeners {
		if _, keep := next[endpoint]; !keep {
			removed = append(removed, entry)
		}
	}
	s.listeners = next
	s.refreshEndpointsLocked()
	for endpoint, entry := range opened {
		go s.serve(endpoint, entry)
	}
	for _, entry := range removed {
		_ = entry.server.Close()
	}
	return nil
}

func (s *LocalProbeService) Configuration(
	deviceID string,
) (model.LocalProbeConfiguration, error) {
	s.mu.RLock()
	endpoints := slices.Clone(s.endpoints)
	closed := s.closed
	s.mu.RUnlock()
	if closed || len(endpoints) == 0 {
		return model.LocalProbeConfiguration{}, model.NewError(
			model.ErrorUnavailable,
			"local probe is unavailable",
			true,
		)
	}
	device, err := FindDevice(s.store.Snapshot(), deviceID)
	if err != nil || !device.Enabled {
		return model.LocalProbeConfiguration{}, model.NewError(
			model.ErrorDeviceRevoked,
			"device was not found",
			false,
		)
	}
	return model.LocalProbeConfiguration{
		Endpoints: endpoints,
		Key: base64.RawURLEncoding.EncodeToString(
			s.deviceKey(device),
		),
	}, nil
}

func (s *LocalProbeService) serve(
	endpoint string,
	entry localProbeListener,
) {
	err := entry.server.Serve(entry.listener)
	if err == nil || errors.Is(err, http.ErrServerClosed) {
		return
	}
	s.mu.Lock()
	current, exists := s.listeners[endpoint]
	if exists && current.server == entry.server {
		delete(s.listeners, endpoint)
		s.refreshEndpointsLocked()
	}
	s.mu.Unlock()
	if !exists || current.server != entry.server {
		return
	}
	select {
	case s.errors <- fmt.Errorf("serve local probes on %s: %w", endpoint, err):
	default:
	}
}

func (s *LocalProbeService) refreshEndpointsLocked() {
	s.endpoints = s.endpoints[:0]
	for endpoint := range s.listeners {
		s.endpoints = append(s.endpoints, endpoint)
	}
	slices.Sort(s.endpoints)
}

func (s *LocalProbeService) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	listeners := make([]localProbeListener, 0, len(s.listeners))
	for _, entry := range s.listeners {
		listeners = append(listeners, entry)
	}
	s.listeners = nil
	s.endpoints = nil
	s.mu.Unlock()
	for _, entry := range listeners {
		_ = entry.server.Close()
	}
}

func (s *LocalProbeService) ServeHTTP(
	writer http.ResponseWriter,
	request *http.Request,
) {
	requestID, err := newID()
	if err != nil {
		writeHTTPError(s.store.logger, writer, request, model.WithOperation(err, "local_probe.request_id"))
		return
	}
	writer.Header().Set("X-Request-ID", requestID)
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	if request.Method != http.MethodPost || request.URL.Path != "/probe" {
		writeHTTPError(s.store.logger, writer, request, model.NewError(model.ErrorNotFound, "local probe endpoint was not found", false))
		return
	}
	if request.Header.Get("Cookie") != "" {
		writeHTTPError(s.store.logger, writer, request, model.NewError(model.ErrorInvalidArgument, "cookies are not accepted", false))
		return
	}
	data, err := io.ReadAll(io.LimitReader(request.Body, maxProbeRequestSize+1))
	if err != nil || len(data) > maxProbeRequestSize {
		writeHTTPError(s.store.logger, writer, request, model.WrapError(model.ErrorInvalidArgument, "local probe request is unreadable or too large", false, err))
		return
	}
	var input model.LocalProbeRequest
	if err := model.DecodeStrict(data, &input); err != nil {
		writeHTTPError(s.store.logger, writer, request, model.WrapError(model.ErrorInvalidArgument, "invalid local probe JSON", false, err))
		return
	}
	nonce, err := base64.RawURLEncoding.DecodeString(input.Nonce)
	if err != nil || len(nonce) != probeNonceSize {
		writeHTTPError(s.store.logger, writer, request, model.NewError(model.ErrorInvalidArgument, "invalid local probe nonce", false))
		return
	}
	device, err := FindDevice(s.store.Snapshot(), input.DeviceID)
	if err != nil || !device.Enabled {
		writeHTTPError(s.store.logger, writer, request, model.NewError(model.ErrorNotFound, "local probe device is unavailable", false))
		return
	}
	mac := hmac.New(sha256.New, s.deviceKey(device))
	_, _ = mac.Write([]byte(model.LocalProbeDomain))
	_, _ = mac.Write(nonce)
	writer.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(writer).Encode(model.LocalProbeResponse{
		Proof: base64.RawURLEncoding.EncodeToString(mac.Sum(nil)),
	}); err != nil {
		s.store.logger.Error("write local probe response", "request_id", requestID, "error", err)
	}
}

func (s *LocalProbeService) deviceKey(device model.Device) []byte {
	mac := hmac.New(sha256.New, s.masterKey)
	_, _ = mac.Write([]byte(device.ID))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(device.PublicKey))
	return mac.Sum(nil)
}

func loadOrCreateProbeMasterKey(stateDirectory string) ([]byte, error) {
	path := filepath.Join(stateDirectory, probeMasterKeyFileName)
	data, err := os.ReadFile(path)
	if err == nil {
		if len(data) != probeKeySize {
			return nil, errors.New("local probe master key has invalid length")
		}
		return data, nil
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read local probe master key: %w", err)
	}
	key := make([]byte, probeKeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate local probe master key: %w", err)
	}
	temporary, err := os.CreateTemp(stateDirectory, ".probe-key-*")
	if err != nil {
		return nil, fmt.Errorf("create local probe master key: %w", err)
	}
	temporaryPath := temporary.Name()
	cleanup := true
	defer func() {
		_ = temporary.Close()
		if cleanup {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return nil, err
	}
	if _, err := temporary.Write(key); err != nil {
		return nil, err
	}
	if err := temporary.Sync(); err != nil {
		return nil, err
	}
	if err := temporary.Close(); err != nil {
		return nil, err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return nil, err
	}
	cleanup = false
	if err := syncDirectory(stateDirectory); err != nil {
		return nil, err
	}
	return key, nil
}

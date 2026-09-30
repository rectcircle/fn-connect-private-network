package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

func TestServerAPI(t *testing.T) {
	store, err := OpenService(t.TempDir(), testNetwork{}, nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	handler := NewHTTPHandler(store, nil)

	index := httptest.NewRecorder()
	handler.ServeHTTP(
		index,
		httptest.NewRequest(http.MethodGet, "/app/fncpn", nil),
	)
	if index.Code != http.StatusOK ||
		!bytes.Contains(index.Body.Bytes(), []byte("<title>FnCPN</title>")) ||
		!bytes.Contains(index.Body.Bytes(), []byte(`id="networkState"`)) ||
		!bytes.Contains(index.Body.Bytes(), []byte("正在应用")) {
		t.Fatalf("index status = %d", index.Code)
	}

	bootstrap := httptest.NewRecorder()
	bootstrapRequest := httptest.NewRequest(http.MethodGet, "/api/v1/bootstrap", nil)
	bootstrapRequest.Header.Set("X-Trim-Userid", "user")
	handler.ServeHTTP(
		bootstrap,
		bootstrapRequest,
	)
	if bootstrap.Code != http.StatusOK {
		t.Fatalf("bootstrap status = %d", bootstrap.Code)
	}

	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(
		unauthorized,
		httptest.NewRequest(http.MethodGet, "/api/v1/admin/devices", nil),
	)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", unauthorized.Code)
	}

	createBody, _ := json.Marshal(map[string]string{
		"name":      "MacBook",
		"publicKey": apiTestPublicKey(),
	})
	createRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/devices",
		bytes.NewReader(createBody),
	)
	createRequest.Header.Set("X-Trim-Isadmin", "true")
	createRequest.Header.Set("X-Trim-Userid", "admin")
	create := httptest.NewRecorder()
	handler.ServeHTTP(create, createRequest)
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", create.Code, create.Body.String())
	}
	retryRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/devices",
		bytes.NewReader(createBody),
	)
	retryRequest.Header = createRequest.Header.Clone()
	retry := httptest.NewRecorder()
	handler.ServeHTTP(retry, retryRequest)
	if retry.Code != http.StatusOK {
		t.Fatalf("idempotent create status = %d body=%s", retry.Code, retry.Body.String())
	}
	var device model.DeviceRegistration
	if err := json.NewDecoder(create.Body).Decode(&device); err != nil {
		t.Fatalf("decode device: %v", err)
	}
	if device.Device.OverlayAddress != "10.253.203.2/32" {
		t.Fatalf("device address = %q", device.Device.OverlayAddress)
	}

	updateRequest := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/admin/networks",
		bytes.NewBufferString(
			`{"overlayCIDR":"172.20.0.0/24"}`,
		),
	)
	updateRequest.Header.Set("X-Trim-Isadmin", "true")
	updateRequest.Header.Set("X-Trim-Userid", "admin")
	update := httptest.NewRecorder()
	handler.ServeHTTP(update, updateRequest)
	if update.Code != http.StatusOK {
		t.Fatalf("update status = %d body=%s", update.Code, update.Body.String())
	}
	state := store.Snapshot()
	if state.Settings.OverlayCIDR != "172.20.0.0/24" ||
		state.Devices[0].OverlayAddress != "172.20.0.2/32" {
		t.Fatalf("unexpected updated state: %+v", state)
	}
}

func TestServerAPIRequiresUserAndAdministrator(t *testing.T) {
	store, err := OpenService(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	handler := NewHTTPHandler(store, nil)

	request := httptest.NewRequest(http.MethodGet, "/api/v1/bootstrap", nil)
	request.Header.Set("X-Trim-Isadmin", "true")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("bootstrap status = %d", response.Code)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/devices", nil)
	request.Header.Set("X-Trim-Userid", "user")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("non-admin status = %d", response.Code)
	}
}

func TestConfigurationQueryWatch(t *testing.T) {
	network := &rebuildingNetworkStatus{
		snapshot: model.ServerNetworkSnapshot{
			Network: model.ServerPrivilegedStatus{
				Active:     true,
				PublicKey:  apiTestPublicKey(),
				ListenPort: DefaultListenPort,
			},
			Fresh: true,
		},
	}
	store, err := OpenService(t.TempDir(), network, nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	device, _, err := createTestDevice(store, "MacBook", apiTestPublicKey())
	if err != nil {
		t.Fatalf("create device: %v", err)
	}
	handler := NewHTTPHandler(store, nil)
	path := "/api/v1/devices/" + device.ID + "/config"
	initial := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, path+"?watch=1", nil)
	request.Header.Set("X-Trim-Userid", "user")
	handler.ServeHTTP(initial, request)
	var initialResult struct {
		Changed       bool                      `json:"changed"`
		Cursor        string                    `json:"cursor"`
		Configuration model.ClientConfiguration `json:"configuration"`
	}
	if err := json.NewDecoder(initial.Body).Decode(&initialResult); err != nil {
		t.Fatalf("decode initial watch: %v", err)
	}
	if initial.Code != http.StatusOK ||
		!initialResult.Changed ||
		initialResult.Cursor == "" ||
		initialResult.Configuration.DeviceID != device.ID {
		t.Fatalf("initial watch = %+v", initialResult)
	}

	watched := make(chan *httptest.ResponseRecorder, 1)
	watchContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(
			http.MethodGet,
			path+"?watch=1&after="+initialResult.Cursor,
			nil,
		).
			WithContext(watchContext)
		request.Header.Set("X-Trim-Userid", "user")
		handler.ServeHTTP(response, request)
		watched <- response
	}()

	listenPort := uint16(51821)
	if _, err := store.UpdateNetworks(context.Background(), NetworkUpdate{ListenPort: &listenPort}); err != nil {
		t.Fatalf("update network: %v", err)
	}
	select {
	case response := <-watched:
		var result struct {
			Changed bool   `json:"changed"`
			Cursor  string `json:"cursor"`
		}
		if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
			t.Fatalf("decode changed watch: %v", err)
		}
		if response.Code != http.StatusOK ||
			!result.Changed ||
			result.Cursor == initialResult.Cursor {
			t.Fatalf("changed watch = %+v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("configuration watch was not notified")
	}
}

func TestAdminSnapshotQueryWatchReturnsUnchanged(t *testing.T) {
	store, err := OpenService(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	handler := NewHTTPHandler(store, nil)
	initial := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/admin/snapshot?watch=1",
		nil,
	)
	request.Header.Set("X-Trim-Userid", "admin")
	request.Header.Set("X-Trim-Isadmin", "true")
	handler.ServeHTTP(initial, request)
	var initialResult struct {
		Changed bool   `json:"changed"`
		Cursor  string `json:"cursor"`
	}
	if err := json.NewDecoder(initial.Body).Decode(&initialResult); err != nil {
		t.Fatalf("decode initial watch: %v", err)
	}
	if initial.Code != http.StatusOK ||
		!initialResult.Changed ||
		initialResult.Cursor == "" {
		t.Fatalf("initial watch = %+v", initialResult)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	response := httptest.NewRecorder()
	request = httptest.NewRequest(
		http.MethodGet,
		"/api/v1/admin/snapshot?watch=1&after="+initialResult.Cursor,
		nil,
	).WithContext(ctx)
	request.Header.Set("X-Trim-Userid", "admin")
	request.Header.Set("X-Trim-Isadmin", "true")
	handler.ServeHTTP(response, request)
	var result struct {
		Changed bool   `json:"changed"`
		Cursor  string `json:"cursor"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatalf("decode unchanged watch: %v", err)
	}
	if response.Code != http.StatusOK ||
		result.Changed ||
		result.Cursor != initialResult.Cursor {
		t.Fatalf("unchanged watch = %+v", result)
	}
}

func TestBootstrapMarksPrivilegedStatusStale(t *testing.T) {
	store, err := OpenService(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	network := &failingNetworkStatus{
		snapshot: model.ServerNetworkSnapshot{
			Network: model.ServerPrivilegedStatus{Active: true},
			Fresh:   true,
		},
		err: errors.New("privileged socket unavailable"),
	}
	store.network = network
	handler := NewHTTPHandler(store, nil)
	handler.refreshNetwork(context.Background())
	request := httptest.NewRequest(http.MethodGet, "/api/v1/bootstrap", nil)
	request.Header.Set("X-Trim-Userid", "user")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("bootstrap status = %d", response.Code)
	}
	var body struct {
		Network model.ServerNetworkSnapshot `json:"network"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Network.Fresh || body.Network.LastError == nil {
		t.Fatalf("network snapshot = %+v", body.Network)
	}
}

func TestBootstrapDoesNotWaitForNetworkRefresh(t *testing.T) {
	store, err := OpenService(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	network := &blockingNetworkStatus{
		started: make(chan struct{}),
		release: make(chan struct{}),
		snapshot: model.ServerNetworkSnapshot{
			Network: model.ServerPrivilegedStatus{
				Active:    true,
				Interface: "fncpn-old",
			},
			Fresh: true,
		},
	}
	store.network = network
	store.publish(serverSnapshot{state: store.Snapshot(), network: network.snapshot})
	handler := NewHTTPHandler(store, nil)
	refreshed := make(chan struct{})
	go func() {
		handler.refreshNetwork(context.Background())
		close(refreshed)
	}()
	<-network.started

	result := make(chan model.ServerNetworkSnapshot, 1)
	go func() {
		input := httptest.NewRequest(http.MethodGet, "/api/v1/bootstrap", nil)
		input.Header.Set("X-Trim-Userid", "user")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, input)
		if response.Code != http.StatusOK {
			t.Errorf(
				"bootstrap status=%d body=%s",
				response.Code,
				response.Body.String(),
			)
			result <- model.ServerNetworkSnapshot{}
			return
		}
		var body struct {
			Network model.ServerNetworkSnapshot `json:"network"`
		}
		if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
			t.Errorf("decode response: %v", err)
		}
		result <- body.Network
	}()
	select {
	case snapshot := <-result:
		if snapshot.Network.Interface != "fncpn-old" {
			t.Fatalf("snapshot = %+v", snapshot)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("bootstrap blocked behind network refresh")
	}
	close(network.release)
	<-refreshed
	input := httptest.NewRequest(http.MethodGet, "/api/v1/bootstrap", nil)
	input.Header.Set("X-Trim-Userid", "user")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, input)
	var body struct {
		Network model.ServerNetworkSnapshot `json:"network"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode refreshed response: %v", err)
	}
	if body.Network.Network.Interface != "fncpn-new" {
		t.Fatalf("refreshed snapshot = %+v", body.Network)
	}
}

func TestRefreshRebuildsEmptyPrivilegedNetwork(t *testing.T) {
	network := &rebuildingNetworkStatus{
		snapshot: model.ServerNetworkSnapshot{Fresh: true},
	}
	store, err := OpenService(t.TempDir(), network, nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	handler := NewHTTPHandler(store, nil)

	handler.refreshNetwork(context.Background())

	view := handler.snapshot()
	if network.applied != 1 ||
		!view.network.Fresh ||
		!view.network.Network.Active {
		t.Fatalf(
			"rebuild applied=%d snapshot=%+v",
			network.applied,
			view.network,
		)
	}
}

func TestBootstrapDoesNotWaitForDeviceRegistration(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	store, err := OpenService(t.TempDir(), testNetwork{apply: func(model.ServerState) error {
		once.Do(func() { close(started) })
		<-release
		return nil
	}}, nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	handler := NewHTTPHandler(store, nil)
	registrationDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		body, _ := json.Marshal(map[string]string{
			"name":      "MacBook",
			"publicKey": testPublicKey(8),
		})
		request := httptest.NewRequest(
			http.MethodPost,
			"/api/v1/devices",
			bytes.NewReader(body),
		)
		request.Header.Set("X-Trim-Isadmin", "true")
		request.Header.Set("X-Trim-Userid", "admin")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		registrationDone <- response
	}()
	<-started

	bootstrapDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/bootstrap", nil)
		request.Header.Set("X-Trim-Userid", "user")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		bootstrapDone <- response
	}()
	select {
	case response := <-bootstrapDone:
		if response.Code != http.StatusOK {
			t.Fatalf("bootstrap status=%d body=%s", response.Code, response.Body.String())
		}
		var body struct {
			DeviceCount int `json:"deviceCount"`
		}
		if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
			t.Fatalf("decode bootstrap: %v", err)
		}
		if body.DeviceCount != 0 {
			t.Fatalf("bootstrap exposed uncommitted device count %d", body.DeviceCount)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("bootstrap blocked behind device registration")
	}

	updateDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		request := httptest.NewRequest(
			http.MethodPut,
			"/api/v1/admin/networks",
			bytes.NewBufferString(`{"overlayCIDR":"172.20.0.0/24"}`),
		)
		request.Header.Set("X-Trim-Isadmin", "true")
		request.Header.Set("X-Trim-Userid", "admin")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		updateDone <- response
	}()
	select {
	case response := <-updateDone:
		t.Fatalf(
			"network update bypassed registration: status=%d",
			response.Code,
		)
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	response := <-registrationDone
	if response.Code != http.StatusCreated {
		t.Fatalf(
			"registration status=%d body=%s",
			response.Code,
			response.Body.String(),
		)
	}
	updateResponse := <-updateDone
	if updateResponse.Code != http.StatusOK {
		t.Fatalf(
			"network update status=%d body=%s",
			updateResponse.Code,
			updateResponse.Body.String(),
		)
	}
	if store.Snapshot().Settings.OverlayCIDR != "172.20.0.0/24" {
		t.Fatalf("network update did not run after registration")
	}
}

func TestLocalProbeConfigurationRequiresAuthentication(t *testing.T) {
	store, err := OpenService(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	device, _, err := createTestDevice(store, "MacBook", apiTestPublicKey())
	if err != nil {
		t.Fatalf("create device: %v", err)
	}
	probe := &LocalProbeService{
		store:     store,
		masterKey: bytes.Repeat([]byte{7}, probeKeySize),
		endpoints: []string{"192.168.1.10:54790"},
	}
	handler := NewHTTPHandler(store, probe)
	path := "/app/fncpn/api/v1/devices/" + device.ID + "/local-probe"
	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(
		unauthorized,
		httptest.NewRequest(http.MethodGet, path, nil),
	)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized probe configuration status = %d", unauthorized.Code)
	}
	request := httptest.NewRequest(
		http.MethodGet,
		path,
		nil,
	)
	request.Header.Set("X-Trim-Userid", "user")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf(
			"probe configuration status = %d body=%s",
			response.Code,
			response.Body.String(),
		)
	}
	var configuration model.LocalProbeConfiguration
	if err := json.NewDecoder(response.Body).Decode(&configuration); err != nil {
		t.Fatalf("decode probe configuration: %v", err)
	}
	if len(configuration.Endpoints) != 1 ||
		configuration.Endpoints[0] != "192.168.1.10:54790" ||
		configuration.Key == "" {
		t.Fatalf("probe configuration = %+v", configuration)
	}
}

func TestDeviceRegistrationAndConfiguration(t *testing.T) {
	store, err := OpenService(t.TempDir(), testNetwork{}, nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	handler := NewHTTPHandler(store, nil)
	createBody, _ := json.Marshal(map[string]string{
		"name":      "MacBook",
		"publicKey": testPublicKey(8),
	})
	createRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/devices",
		bytes.NewReader(createBody),
	)
	createRequest.Header.Set("X-Trim-Isadmin", "true")
	createRequest.Header.Set("X-Trim-Userid", "admin")
	create := httptest.NewRecorder()
	handler.ServeHTTP(create, createRequest)
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", create.Code, create.Body.String())
	}
	var registration model.DeviceRegistration
	if err := json.NewDecoder(create.Body).Decode(&registration); err != nil {
		t.Fatalf("decode registration: %v", err)
	}
	if registration.Configuration.DeviceID != registration.Device.ID ||
		registration.Configuration.ClientAddress != registration.Device.OverlayAddress {
		t.Fatalf("registration = %+v", registration)
	}

	request := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/devices/"+registration.Device.ID+"/config",
		nil,
	)
	request.Header.Set("X-Trim-Userid", "user")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("config status = %d body=%s", response.Code, response.Body.String())
	}
	var configuration model.ClientConfiguration
	if err := json.NewDecoder(response.Body).Decode(&configuration); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if configuration.DeviceID != registration.Device.ID ||
		configuration.ServerPublicKey == "" ||
		configuration.ServerAddress != "10.253.203.1/24" ||
		configuration.ListenPort != DefaultListenPort {
		t.Fatalf("configuration = %+v", configuration)
	}
}

type staticNetworkStatus struct {
	status model.ServerPrivilegedStatus
}

func (s staticNetworkStatus) NetworkSnapshot() model.ServerNetworkSnapshot {
	return model.ServerNetworkSnapshot{Network: s.status, Fresh: true}
}

type failingNetworkStatus struct {
	testNetwork
	snapshot model.ServerNetworkSnapshot
	err      error
}

func (s *failingNetworkStatus) Status(
	context.Context,
) (model.ServerPrivilegedStatus, error) {
	return s.snapshot.Network, s.err
}

type blockingNetworkStatus struct {
	testNetwork
	started  chan struct{}
	release  chan struct{}
	mu       sync.RWMutex
	snapshot model.ServerNetworkSnapshot
}

type rebuildingNetworkStatus struct {
	mu       sync.RWMutex
	snapshot model.ServerNetworkSnapshot
	applied  int
}

func (s *rebuildingNetworkStatus) Status(
	context.Context,
) (model.ServerPrivilegedStatus, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneServerNetworkSnapshot(s.snapshot).Network, nil
}

func (s *rebuildingNetworkStatus) Apply(
	ctx context.Context, state model.ServerState,
) (model.ServerPrivilegedStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.applied++
	status, err := (testNetwork{}).Apply(ctx, state)
	s.snapshot = model.ServerNetworkSnapshot{Network: status, Fresh: true}
	return status, err
}

func (s *blockingNetworkStatus) Status(
	context.Context,
) (model.ServerPrivilegedStatus, error) {
	close(s.started)
	<-s.release
	snapshot := model.ServerNetworkSnapshot{
		Network: model.ServerPrivilegedStatus{
			Active:    true,
			Interface: "fncpn-new",
		},
		Fresh: true,
	}
	s.mu.Lock()
	s.snapshot = snapshot
	s.mu.Unlock()
	return snapshot.Network, nil
}

func apiTestPublicKey() string {
	return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
}

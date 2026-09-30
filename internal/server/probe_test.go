package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

func TestLocalProbeUsesPerDeviceHMACWithoutCookies(t *testing.T) {
	store, err := OpenService(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	device, _, err := createTestDevice(store, "MacBook", testPublicKey(7))
	if err != nil {
		t.Fatalf("create device: %v", err)
	}
	service := &LocalProbeService{
		store:     store,
		masterKey: bytes.Repeat([]byte{3}, probeKeySize),
		endpoints: []string{"192.168.1.10:54790"},
	}
	configuration, err := service.Configuration(device.ID)
	if err != nil {
		t.Fatalf("get probe configuration: %v", err)
	}
	repeated, err := service.Configuration(device.ID)
	if err != nil {
		t.Fatalf("repeat probe configuration: %v", err)
	}
	if repeated.Key != configuration.Key {
		t.Fatal("device probe key changed")
	}
	other, _, err := createTestDevice(store, "Desktop", testPublicKey(8))
	if err != nil {
		t.Fatalf("create second device: %v", err)
	}
	otherConfiguration, err := service.Configuration(other.ID)
	if err != nil {
		t.Fatalf("get second probe configuration: %v", err)
	}
	if otherConfiguration.Key == configuration.Key {
		t.Fatal("different devices received the same probe key")
	}
	key, err := base64.RawURLEncoding.DecodeString(configuration.Key)
	if err != nil {
		t.Fatalf("decode probe key: %v", err)
	}
	nonce := bytes.Repeat([]byte{5}, probeNonceSize)
	body, err := json.Marshal(model.LocalProbeRequest{
		DeviceID: device.ID,
		Nonce:    base64.RawURLEncoding.EncodeToString(nonce),
	})
	if err != nil {
		t.Fatalf("encode probe request: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/probe", bytes.NewReader(body))
	response := httptest.NewRecorder()
	service.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("probe status = %d body=%s", response.Code, response.Body.String())
	}
	var result model.LocalProbeResponse
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatalf("decode probe response: %v", err)
	}
	proof, err := base64.RawURLEncoding.DecodeString(result.Proof)
	if err != nil {
		t.Fatalf("decode proof: %v", err)
	}
	expected := hmac.New(sha256.New, key)
	_, _ = expected.Write([]byte(model.LocalProbeDomain))
	_, _ = expected.Write(nonce)
	if !hmac.Equal(proof, expected.Sum(nil)) {
		t.Fatal("probe proof does not match device key")
	}

	request = httptest.NewRequest(http.MethodPost, "/probe", bytes.NewReader(body))
	request.Header.Set("Cookie", "session=secret")
	response = httptest.NewRecorder()
	service.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("probe accepted cookies: status=%d", response.Code)
	}
}

func TestProbeMasterKeyPersists(t *testing.T) {
	directory := t.TempDir()
	first, err := loadOrCreateProbeMasterKey(directory)
	if err != nil {
		t.Fatalf("create probe master key: %v", err)
	}
	second, err := loadOrCreateProbeMasterKey(directory)
	if err != nil {
		t.Fatalf("load probe master key: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("probe master key changed")
	}
}

func TestProbeRetriesInitialFailureOnNetworkEvent(t *testing.T) {
	for _, failure := range []string{"detect", "listen"} {
		t.Run(failure, func(t *testing.T) {
			dir := t.TempDir()
			store, err := OpenService(dir, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			device, _, err := createTestDevice(store, "Mac", testPublicKey(1))
			if err != nil {
				t.Fatal(err)
			}
			probe, err := OpenLocalProbeService(dir, store, 54790)
			if err != nil {
				t.Fatal(err)
			}
			first := make(chan struct{})
			listenCalls := 0
			probe.listen = func(_, _ string) (net.Listener, error) {
				listenCalls++
				if failure == "listen" && listenCalls == 1 {
					close(first)
					return nil, errors.New("port busy")
				}
				return net.Listen("tcp4", "127.0.0.1:0")
			}
			detectCalls := 0
			detect := func() ([]string, error) {
				detectCalls++
				if failure == "detect" && detectCalls == 1 {
					close(first)
					return nil, errors.New("no primary network")
				}
				return []string{"192.168.71.1"}, nil
			}
			ctx, cancel := context.WithCancel(context.Background())
			events := make(chan struct{}, 1)
			done := make(chan struct{})
			go func() {
				defer close(done)
				probe.Run(ctx, detect, events, slog.Default())
			}()
			defer func() { cancel(); <-done; probe.Close() }()
			<-first
			if _, err := probe.Configuration(device.ID); model.AsError(err).Code != model.ErrorUnavailable {
				t.Fatalf("uninitialized probe error=%v", err)
			}
			events <- struct{}{}
			deadline := time.Now().Add(time.Second)
			for {
				if config, err := probe.Configuration(device.ID); err == nil && len(config.Endpoints) == 1 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("probe did not recover")
				}
				time.Sleep(time.Millisecond)
			}
		})
	}
}

func TestNilProbeHTTPReturnsUnavailable(t *testing.T) {
	store, err := OpenService(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var probe *LocalProbeService
	handler := NewHTTPHandler(store, probe)
	request := httptest.NewRequest("GET", "/api/v1/devices/device-1/local-probe", nil)
	request.Header.Set("X-Trim-Userid", "user")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

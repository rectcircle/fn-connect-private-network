//go:build !windows

package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"github.com/rectcircle/fn-connect-private-network/internal/version"
)

func speedFixture(t *testing.T) (*PrivateAPIService, model.Device) {
	t.Helper()
	store, err := OpenService(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	device, _, err := createTestDevice(store, "Mac", testPublicKey(7))
	if err != nil {
		t.Fatal(err)
	}
	return &PrivateAPIService{store: store, masterKey: bytes.Repeat([]byte{3}, 32), endpoints: []string{"192.168.71.2:54790"}}, device
}
func authorizeSpeed(r *http.Request, s *PrivateAPIService, d model.Device) {
	now := time.Now().Unix()
	r.Header.Set("X-FnCPN-Client-Version", version.Current)
	r.Header.Set("X-FnCPN-Device", d.ID)
	r.Header.Set("X-FnCPN-Time", strconv.FormatInt(now, 10))
	r.Header.Set("X-FnCPN-Proof", model.SpeedTestProof(s.speedTestKey(d), d.ID, r.Method, r.URL.Path, now))
}
func testSpeedServer(s *PrivateAPIService, d model.Device) *httptest.Server {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.RemoteAddr = netip.MustParsePrefix(d.OverlayAddress).Addr().String() + ":12345"
		s.ServeHTTP(w, r)
	}))
	server.Config.ReadTimeout = 200 * time.Millisecond
	server.Config.WriteTimeout = 200 * time.Millisecond
	server.Start()
	return server
}
func TestSpeedTestAuthorizationAndIndependentRequests(t *testing.T) {
	s, d := speedFixture(t)
	config, err := s.Configuration(d.ID)
	if err != nil || config.Key == config.SpeedTestKey {
		t.Fatal("independent test key missing", err)
	}
	request := httptest.NewRequest("POST", model.SpeedTestPath+"/upload", nil)
	authorizeSpeed(request, s, d)
	request.RemoteAddr = "192.168.71.9:12345"
	response := httptest.NewRecorder()
	s.ServeHTTP(response, request)
	if response.Code != 401 {
		t.Fatal("LAN bypass accepted", response.Code)
	}
	request = httptest.NewRequest("GET", model.SpeedTestPath+"/download", nil)
	authorizeSpeed(request, s, d)
	request.RemoteAddr = netip.MustParsePrefix(d.OverlayAddress).Addr().String() + ":12345"
	request.Header.Set("X-FnCPN-Proof", "wrong")
	response = httptest.NewRecorder()
	s.ServeHTTP(response, request)
	if response.Code != 401 {
		t.Fatal("invalid proof accepted")
	}
	server := testSpeedServer(s, d)
	defer server.Close()
	// An upload with an open request body does not prevent a second test on the NAS.
	reader, writer := io.Pipe()
	defer writer.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	upload, _ := http.NewRequestWithContext(ctx, "POST", server.URL+model.SpeedTestPath+"/upload", reader)
	authorizeSpeed(upload, s, d)
	first, err := server.Client().Do(upload)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Body.Close()
	latency, _ := http.NewRequest("GET", server.URL+model.SpeedTestPath+"/latency", nil)
	authorizeSpeed(latency, s, d)
	connection, _, err := websocket.Dial(t.Context(), strings.Replace(latency.URL.String(), "http:", "ws:", 1), &websocket.DialOptions{HTTPClient: server.Client(), HTTPHeader: latency.Header})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()
	readCtx := connection.CloseRead(t.Context())
	pingCtx, stop := context.WithTimeout(readCtx, 2*time.Second)
	defer stop()
	if err := connection.Ping(pingCtx); err != nil {
		t.Fatal(err)
	}
	cancel()
	first.Body.Close()
	// A new upload succeeds immediately after abort; there is no session lease to expire.
	second, _ := http.NewRequest("POST", server.URL+model.SpeedTestPath+"/upload", strings.NewReader("again"))
	authorizeSpeed(second, s, d)
	result, err := server.Client().Do(second)
	if err != nil {
		t.Fatal(err)
	}
	defer result.Body.Close()
	body, _ := io.ReadAll(result.Body)
	if result.StatusCode != 200 || !strings.Contains(string(body), "event: complete") {
		t.Fatal(result.StatusCode, string(body))
	}
}
func TestUploadSSEProgressArrivesBeforeRequestEOF(t *testing.T) {
	s, d := speedFixture(t)
	server := testSpeedServer(s, d)
	defer server.Close()
	reader, writer := io.Pipe()
	defer writer.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "POST", server.URL+model.SpeedTestPath+"/upload", reader)
	authorizeSpeed(request, s, d)
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatal(response.Header)
	}
	if _, err := writer.Write(make([]byte, 1024)); err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(response.Body)
	var event string
	progress := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event: ") {
			event = strings.TrimPrefix(line, "event: ")
		}
		if strings.HasPrefix(line, "data: ") {
			var stats model.SpeedTestStats
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &stats); err != nil {
				t.Fatal(err)
			}
			if event == "progress" && stats.TotalBytes == 1024 {
				progress = true
				break
			}
		}
	}
	if !progress {
		t.Fatal("no progress before EOF", scanner.Err())
	}
	// Leave enough time for a real measured chunk after the one-second warmup.
	time.Sleep(1100 * time.Millisecond)
	if _, err := writer.Write(make([]byte, 2048)); err != nil {
		t.Fatal(err)
	}
	writer.Close()
	complete := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event: ") {
			event = strings.TrimPrefix(line, "event: ")
		}
		if strings.HasPrefix(line, "data: ") && event == "complete" {
			var stats model.SpeedTestStats
			_ = json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &stats)
			if stats.TotalBytes != 3072 || stats.Bytes != 2048 {
				t.Fatal("receiver count includes warmup or loses bytes", stats)
			}
			complete = true
		}
	}
	if !complete {
		t.Fatal("missing final completion", scanner.Err())
	}
}
func TestDownloadDisconnectDoesNotBlockAnotherRequest(t *testing.T) {
	s, d := speedFixture(t)
	server := testSpeedServer(s, d)
	defer server.Close()
	for i := 0; i < 2; i++ {
		request, _ := http.NewRequest("GET", server.URL+model.SpeedTestPath+"/download", nil)
		authorizeSpeed(request, s, d)
		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != 200 {
			t.Fatal(response.StatusCode)
		}
		if _, err := io.CopyN(io.Discard, response.Body, 1024); err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
	}
}

func TestUploadIdleConfirmationDoesNotExtendMeasurement(t *testing.T) {
	s, d := speedFixture(t)
	server := testSpeedServer(s, d)
	defer server.Close()
	reader, writer := io.Pipe()
	defer writer.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "POST", server.URL+model.SpeedTestPath+"/upload", reader)
	authorizeSpeed(request, s, d)
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if _, err := writer.Write(make([]byte, 1024)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
	if _, err := writer.Write(make([]byte, 2048)); err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(response.Body)
	event := ""
	var measured model.SpeedTestStats
	seen := 0
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event: ") {
			event = strings.TrimPrefix(line, "event: ")
		}
		if strings.HasPrefix(line, "data: ") && event == "progress" {
			var stats model.SpeedTestStats
			_ = json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &stats)
			if stats.Bytes == 2048 {
				if seen == 0 {
					measured = stats
				} else if stats.ElapsedMS != measured.ElapsedMS {
					t.Fatal("idle heartbeat extended payload measurement", measured, stats)
				}
				seen++
				if seen == 3 {
					break
				}
			}
		}
	}
	if seen != 3 {
		t.Fatal("no idle progress samples", scanner.Err())
	}
	writer.Close()
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event: ") {
			event = strings.TrimPrefix(line, "event: ")
		}
		if strings.HasPrefix(line, "data: ") && event == "complete" {
			var final model.SpeedTestStats
			_ = json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &final)
			if final.ElapsedMS != measured.ElapsedMS || final.Bytes != measured.Bytes {
				t.Fatal("EOF changed the measurement", measured, final)
			}
			return
		}
	}
	t.Fatal("no complete confirmation", scanner.Err())
}

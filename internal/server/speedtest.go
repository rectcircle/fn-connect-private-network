package server

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"github.com/rectcircle/fn-connect-private-network/internal/version"
)

func (s *PrivateAPIService) speedTestKey(d model.Device) []byte {
	mac := hmac.New(sha256.New, s.masterKey)
	mac.Write([]byte("speedtest-v1\x00" + d.ID + "\x00" + d.PublicKey))
	return mac.Sum(nil)
}

// All accounting belongs to this request. There is no registry, admission lock,
// session token, polling endpoint, or delayed cleanup lease.
func (s *PrivateAPIService) serveSpeedTest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-FnCPN-Version", version.Current)
	reject := func(code model.ErrorCode, message string) {
		writeHTTPError(s.store.logger, w, r, model.NewError(code, message, false))
	}
	if err := version.Check(r.Header.Get("X-FnCPN-Client-Version"), version.Current); err != nil {
		writeHTTPError(s.store.logger, w, r, err)
		return
	}
	valid := (r.Method == http.MethodGet && (r.URL.Path == model.SpeedTestPath+"/latency" || r.URL.Path == model.SpeedTestPath+"/download")) || (r.Method == http.MethodPost && r.URL.Path == model.SpeedTestPath+"/upload")
	if !valid {
		reject(model.ErrorNotFound, "speed test endpoint not found")
		return
	}
	if r.Header.Get("Cookie") != "" {
		reject(model.ErrorInvalidArgument, "cookies are not accepted")
		return
	}
	device, err := FindDevice(s.store.Snapshot(), r.Header.Get("X-FnCPN-Device"))
	if err != nil || !device.Enabled {
		reject(model.ErrorDeviceRevoked, "device is unavailable")
		return
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	source, parseErr := netip.ParseAddr(host)
	prefix, prefixErr := netip.ParsePrefix(device.OverlayAddress)
	if err != nil || parseErr != nil || prefixErr != nil || source.Unmap() != prefix.Addr().Unmap() {
		reject(model.ErrorAuthRequired, "speed test requires the device tunnel source address")
		return
	}
	timestamp, err := strconv.ParseInt(r.Header.Get("X-FnCPN-Time"), 10, 64)
	if err != nil || timestamp < time.Now().Add(-30*time.Second).Unix() || timestamp > time.Now().Add(30*time.Second).Unix() {
		reject(model.ErrorAuthRequired, "expired speed test authorization")
		return
	}
	expected := model.SpeedTestProof(s.speedTestKey(device), device.ID, r.Method, r.URL.Path, timestamp)
	if !hmac.Equal([]byte(expected), []byte(r.Header.Get("X-FnCPN-Proof"))) {
		reject(model.ErrorAuthRequired, "invalid speed test authorization")
		return
	}
	controller := http.NewResponseController(w)
	// These deadlines are per request and override only the private API's short probe deadlines.
	deadline := time.Now().Add(45 * time.Second)
	_ = controller.SetReadDeadline(deadline)
	_ = controller.SetWriteDeadline(deadline)
	ctx, cancel := context.WithDeadline(r.Context(), deadline)
	defer cancel()
	if r.URL.Path == model.SpeedTestPath+"/latency" {
		connection, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled, InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer connection.CloseNow()
		connection.SetReadLimit(128)
		// Read processes RFC 6455 Ping/Pong control frames automatically.
		for {
			_, _, err := connection.Read(ctx)
			if err != nil {
				return
			}
		}
	}
	abort := context.AfterFunc(ctx, func() { _ = controller.SetReadDeadline(time.Now()); _ = controller.SetWriteDeadline(time.Now()) })
	defer abort()
	if r.URL.Path == model.SpeedTestPath+"/download" {
		w.Header().Set("Content-Type", "application/octet-stream")
		buf := make([]byte, 64<<10)
		if _, err := rand.Read(buf); err != nil {
			reject(model.ErrorInternal, "cannot generate test data")
			return
		}
		end := time.Now().Add(9 * time.Second)
		for remaining := model.SpeedTestMaxBytes; remaining > 0 && time.Now().Before(end) && ctx.Err() == nil; {
			n, err := w.Write(buf[:min(int64(len(buf)), remaining)])
			remaining -= int64(n)
			if err != nil {
				return
			}
		}
		return
	}
	if err := controller.EnableFullDuplex(); err != nil {
		reject(model.ErrorUnavailable, "HTTP full duplex upload is unavailable")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	if err := controller.Flush(); err != nil {
		return
	}
	s.receiveSpeedUpload(ctx, cancel, w, r, controller)
}

func (s *PrivateAPIService) receiveSpeedUpload(ctx context.Context, cancel context.CancelFunc, w http.ResponseWriter, r *http.Request, controller *http.ResponseController) {
	var mu sync.Mutex
	var stats model.SpeedTestStats
	var started time.Time
	// One goroutine owns the request body, one owns the SSE response writer.
	done := make(chan error, 1)
	go func() {
		buf := make([]byte, 64<<10)
		for {
			n, err := r.Body.Read(buf)
			mu.Lock()
			if n > 0 && started.IsZero() {
				started = time.Now()
			}
			stats.TotalBytes += int64(n)
			if n > 0 && !started.IsZero() {
				elapsed := time.Since(started) - time.Second
				if elapsed > 0 {
					stats.Bytes += int64(n)
					stats.ElapsedMS = elapsed.Milliseconds()
				}
			}
			exceeded := stats.TotalBytes > model.SpeedTestMaxBytes
			mu.Unlock()
			if exceeded {
				done <- fmt.Errorf("upload exceeds byte limit")
				return
			}
			if err != nil {
				if err == io.EOF {
					done <- nil
				} else {
					done <- err
				}
				return
			}
			if ctx.Err() != nil {
				done <- ctx.Err()
				return
			}
		}
	}()
	snapshot := func() model.SpeedTestStats { mu.Lock(); defer mu.Unlock(); return stats }

	send := func(event string, value any) error {
		payload, err := json.Marshal(value)
		if err != nil {
			return err
		}
		if _, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, payload); err != nil {
			return err
		}
		return controller.Flush()
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	join := func() { cancel(); _ = controller.SetReadDeadline(time.Now()); <-done }
	for {
		select {
		case <-ctx.Done():
			join()
			return
		case err := <-done:
			if err != nil {
				_ = send("error", map[string]string{"message": "upload interrupted"})
				return
			}
			_ = send("complete", snapshot())
			return
		case <-ticker.C:
			if err := send("progress", snapshot()); err != nil {
				join()
				return
			}
		}
	}
}

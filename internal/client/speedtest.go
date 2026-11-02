package client

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"github.com/rectcircle/fn-connect-private-network/internal/version"
)

type SpeedTestResult struct {
	Link            string    `json:"link"`
	LatencyMS       *float64  `json:"latencyMs,omitempty"`
	TimeoutPercent  *float64  `json:"timeoutPercent,omitempty"`
	DownloadMbps    *float64  `json:"downloadMbps,omitempty"`
	UploadMbps      *float64  `json:"uploadMbps,omitempty"`
	DownloadLimited bool      `json:"downloadLimited,omitempty"`
	UploadLimited   bool      `json:"uploadLimited,omitempty"`
	CompletedAt     time.Time `json:"completedAt"`
	Error           string    `json:"error,omitempty"`
}
type SpeedTestStatus struct {
	FNID         string            `json:"fnId"`
	Active       bool              `json:"active"`
	Stage        string            `json:"stage"`
	Link         string            `json:"link"`
	Sequence     uint64            `json:"sequence"`
	Mbps         float64           `json:"mbps"`
	RTTMS        float64           `json:"rttMs"`
	EchoSent     uint64            `json:"echoSent"`
	EchoReceived uint64            `json:"echoReceived"`
	Samples      int               `json:"samples"`
	Progress     float64           `json:"progress"`
	Results      []SpeedTestResult `json:"results"`
	Error        string            `json:"error,omitempty"`
	RestoreError string            `json:"restoreError,omitempty"`
}
type SpeedTestRequest struct {
	Link string `json:"link"`
}

// A measurement lives on the caller's stack. It is not a daemon task and has no
// task ID, status lookup, admission lease or persisted error state.
type speedMeasurement struct {
	*Manager
	status SpeedTestStatus
	report func(SpeedTestStatus) error
}

func (m *speedMeasurement) updateSpeed(update func(*SpeedTestStatus)) {
	update(&m.status)
	m.status.Sequence++
	out := m.status
	out.Results = slices.Clone(out.Results)
	_ = m.report(out)
}

func (m *Manager) RunSpeedTest(parent context.Context, input SpeedTestRequest, report func(SpeedTestStatus) error) error {
	if input.Link != "ipv6" && input.Link != "fn-connect" && input.Link != "all" {
		return model.NewError(model.ErrorInvalidArgument, "请选择 IPv6、FN Connect 或两条链路", false)
	}
	if err := parent.Err(); err != nil {
		return err
	}
	coordinator := m.connectionCoordinator()
	if coordinator == nil {
		return model.NewError(model.ErrorUnavailable, "连接服务尚未就绪", true)
	}
	coordinator.mu.Lock()
	if coordinator.cancel != nil {
		coordinator.cancel()
	}
	coordinator.mu.Unlock()
	m.operation.Lock()
	defer m.operation.Unlock()
	if err := parent.Err(); err != nil {
		return err
	}
	cfg, err := m.store.Load()
	if err != nil {
		return err
	}
	if cfg == nil || cfg.DeviceID == "" {
		return model.NewError(model.ErrorAuthRequired, "请先登录 NAS", false)
	}
	coordinator.mu.Lock()
	previousSuspended := coordinator.suspended
	coordinator.waiters = append(coordinator.waiters, coordinator.activeWaiters...)
	coordinator.activeWaiters = nil
	if coordinator.active&workManual != 0 {
		coordinator.pending |= workManual
	}
	coordinator.invalidateLocked()
	generation := coordinator.epoch
	invalidated := coordinator.epochDone
	coordinator.suspended = true
	if coordinator.cancel != nil {
		coordinator.cancel()
	}
	coordinator.mu.Unlock()
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	ctx = context.WithValue(ctx, connectionAttemptKey{}, connectionAttemptIdentity{coordinator, generation})
	watchDone := make(chan struct{})
	defer close(watchDone)
	go func() {
		select {
		case <-watchDone:
		case <-ctx.Done():
		case <-invalidated:
			cancel()
		}
	}()

	measurement := &speedMeasurement{Manager: m, status: SpeedTestStatus{Active: true, FNID: cfg.FNID, Stage: "preparing"}, report: func(s SpeedTestStatus) error {
		err := report(s)
		if err != nil {
			cancel()
		}
		return err
	}}
	links := []string{input.Link}
	if input.Link == "all" {
		links = []string{"ipv6", "fn-connect"}
	}
	for _, link := range links {
		measurement.status.Results = append(measurement.status.Results, SpeedTestResult{Link: link})
	}
	networkTouched := false
	for index, link := range links {
		if ctx.Err() != nil {
			break
		}
		measurement.updateSpeed(func(s *SpeedTestStatus) {
			s.Link = link
			s.Stage = "checking"
			s.Mbps = 0
			s.Progress = 0
			s.Samples = 0
			s.RTTMS = 0
			s.EchoSent = 0
			s.EchoReceived = 0
		})
		// Forced tests use the same discovery and destination route check as
		// ordinary connections; interface address classification is advisory.
		err = nil
		if err == nil {
			measurement.updateSpeed(func(s *SpeedTestStatus) { s.Stage = "connecting" })
			m.directRetryAfter = time.Time{}
			networkTouched = true
			err = m.connectWithConfig(context.WithValue(ctx, forcedLinkKey{}, link), *cfg)
			if err == nil {
				current, loadErr := m.store.Load()
				if loadErr != nil {
					err = loadErr
				} else if current == nil || current.FNID != cfg.FNID {
					err = errors.New("NAS 已变更")
				} else {
					err = measurement.measureLink(ctx, *current, index)
				}
			}
		}
		if err != nil {
			message := err.Error()
			measurement.updateSpeed(func(s *SpeedTestStatus) {
				s.Results[index].Error = message
				s.Results[index].CompletedAt = time.Now().UTC()
			})
			if isAdmissionFailure(err) {
				break
			}
		}
	}
	canceled := ctx.Err() != nil
	measurement.updateSpeed(func(s *SpeedTestStatus) { s.Stage = "restoring"; s.Mbps = 0 })
	coordinator.mu.Lock()
	obsolete := coordinator.epoch != generation || coordinator.stopped
	coordinator.mu.Unlock()
	if !obsolete && networkTouched {
		// Cancellation ends I/O immediately, but restoration has its own bounded lifetime.
		restoreCtx, stop := context.WithTimeout(context.Background(), connectionAttemptTimeout)
		restoreCtx = context.WithValue(restoreCtx, connectionAttemptKey{}, connectionAttemptIdentity{coordinator, generation})
		current, restoreErr := m.store.Load()
		if restoreErr == nil && current != nil && current.AutoConnect && !previousSuspended {
			m.resetReconnectCooldown()
			restoreErr = m.connectWithConfig(restoreCtx, *current)
		} else if restoreErr == nil {
			restoreErr = m.disconnect(restoreCtx)
		}
		stop()
		if restoreErr != nil {
			measurement.updateSpeed(func(s *SpeedTestStatus) { s.RestoreError = restoreErr.Error() })
		}
	}
	coordinator.mu.Lock()
	if coordinator.epoch == generation {
		coordinator.suspended = previousSuspended
	}
	coordinator.mu.Unlock()
	coordinator.signal()
	measurement.updateSpeed(func(s *SpeedTestStatus) {
		s.Active = false
		s.Stage = "completed"
		if canceled {
			s.Stage = "canceled"
		}
	})
	return nil
}

type speedHTTP struct {
	client       *http.Client
	base, device string
	key          []byte
}

func (h *speedHTTP) headers(method, path string) http.Header {
	now := time.Now().Unix()
	headers := http.Header{}
	headers.Set("X-FnCPN-Client-Version", version.Current)
	headers.Set("X-FnCPN-Device", h.device)
	headers.Set("X-FnCPN-Time", strconv.FormatInt(now, 10))
	headers.Set("X-FnCPN-Proof", model.SpeedTestProof(h.key, h.device, method, path, now))
	return headers
}
func (h *speedHTTP) request(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, method, h.base+path, body)
	if err != nil {
		return nil, err
	}
	request.Header = h.headers(method, path)
	request.Header.Set("Content-Type", "application/octet-stream")
	response, err := h.client.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer response.Body.Close()
		return nil, HTTPResponseError(response, "speedtest", nil)
	}
	if err := version.Check(version.Current, response.Header.Get("X-FnCPN-Version")); err != nil {
		response.Body.Close()
		return nil, err
	}
	return response, nil
}
func (m *speedMeasurement) measureLink(ctx context.Context, cfg LocalConfig, index int) error {
	if m.lastPlan == nil {
		return errors.New("尚未建立隧道")
	}
	snap, err := m.probe.Snapshot()
	if err != nil {
		return err
	}
	fingerprint := snap.Fingerprint()
	m.updateSpeed(func(s *SpeedTestStatus) {
		s.Stage = "verifying"
	})
	// Refresh the authenticated private API configuration, including the independent test key.
	cookies, err := m.store.LoadCookies(cfg.FNID)
	if err != nil {
		return err
	}
	remote, err := m.remote(cfg.FNID, cookies, func(updated []Cookie) error { return m.store.MergeCookies(cfg.FNID, cookies, updated) })
	if err != nil {
		return err
	}
	pc, err := remote.PrivateAPIConfiguration(ctx, cfg.DeviceID)
	if err != nil {
		return err
	}
	key, err := base64.RawURLEncoding.DecodeString(pc.SpeedTestKey)
	if err != nil || len(key) != 32 {
		return errors.New("invalid speed test configuration")
	}
	var target string
	for _, endpoint := range pc.Endpoints {
		host, _, e := net.SplitHostPort(endpoint)
		if e != nil {
			continue
		}
		addr, e := netip.ParseAddr(host)
		if e != nil {
			continue
		}
		for _, route := range m.lastPlan.AllowedIPs {
			prefix, e := netip.ParsePrefix(route)
			if e == nil && prefix.Contains(addr) {
				target = endpoint
				break
			}
		}
		if target != "" {
			break
		}
	}
	if target == "" {
		return errors.New("NAS IP is not routed through the selected tunnel (check overlapping LAN subnets)")
	}
	source, err := netip.ParsePrefix(cfg.Configuration.ClientAddress)
	if err != nil {
		return err
	}
	transport := &http.Transport{Proxy: nil, DisableCompression: true, MaxConnsPerHost: 4, DialContext: (&net.Dialer{LocalAddr: &net.TCPAddr{IP: net.IP(source.Addr().AsSlice())}, Timeout: 3 * time.Second}).DialContext}
	// Only platforms that explicitly need a userspace dialer override the
	// source-bound system route; normal HarmonyOS VPNs use the latter too.
	if m.speedTestDialContext != nil {
		transport.DialContext = m.speedTestDialContext
	}
	defer transport.CloseIdleConnections()
	h := &speedHTTP{client: &http.Client{Transport: transport, Timeout: 45 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, base: "http://" + target}
	h.device = cfg.DeviceID
	h.key = key
	path := model.SpeedTestPath
	// Verify the existing HMAC-bound NAS identity through this exact transport before trusting measurements.
	pc.Endpoints = []string{target}
	probe := SystemLocalProbe{HTTPClient: h.client}
	ok, err := probe.Reachable(ctx, pc, cfg.DeviceID, NetworkSnapshot{})
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("NAS 身份验证失败")
	}
	if err := m.measureLatency(ctx, h, path, index); err != nil {
		return err
	}
	if err := m.checkSpeedPath(ctx, fingerprint); err != nil {
		return err
	}
	if err := m.measureDownload(ctx, h, path, index, fingerprint); err != nil {
		return err
	}
	if err := m.measureUpload(ctx, h, path, index, fingerprint); err != nil {
		return err
	}
	m.updateSpeed(func(s *SpeedTestStatus) { s.Results[index].CompletedAt = time.Now().UTC() })
	return nil
}
func (m *speedMeasurement) checkSpeedPath(ctx context.Context, fingerprint string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	snap, err := m.probe.Snapshot()
	if err != nil {
		return err
	}
	if snap.Fingerprint() != fingerprint {
		return errors.New("测试期间网络发生变化，请重试")
	}
	status, err := m.networkStatus(ctx)
	if err != nil {
		return err
	}
	return clientNetworkHealthError(m.Status().State, status, nil, m.bridge.Connected())
}
func (m *speedMeasurement) measureDownload(ctx context.Context, h *speedHTTP, path string, index int, fingerprint string) error {
	m.updateSpeed(func(s *SpeedTestStatus) { s.Stage = "download"; s.Mbps = 0; s.Progress = 0 })
	response, err := h.request(ctx, http.MethodGet, path+"/download", nil)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	started := time.Now()
	measure := started.Add(time.Second)
	last := measure
	var count, total, lastCount int64
	buf := make([]byte, 64<<10)
	for {
		n, readErr := response.Body.Read(buf)
		now := time.Now()
		total += int64(n)
		if now.After(measure) {
			count += int64(n)
		}
		if now.Sub(last) >= 250*time.Millisecond {
			rate := float64(count-lastCount) * 8 / now.Sub(last).Seconds() / 1e6
			last = now
			lastCount = count
			m.updateSpeed(func(s *SpeedTestStatus) { s.Mbps = rate; s.Progress = min(1, now.Sub(measure).Seconds()/8) })
			if err := m.checkSpeedPath(ctx, fingerprint); err != nil {
				return err
			}
		}
		if total > model.SpeedTestMaxBytes {
			return errors.New("下载流量超出允许范围")
		}
		if readErr != nil {
			if readErr != io.EOF {
				return readErr
			}
			break
		}
	}
	if err := m.checkSpeedPath(ctx, fingerprint); err != nil {
		return err
	}
	elapsed := time.Since(measure).Seconds()
	if elapsed < 0.5 {
		return errors.New("下载样本不足，请重试")
	}
	rate := float64(count) * 8 / elapsed / 1e6
	m.updateSpeed(func(s *SpeedTestStatus) {
		s.Results[index].DownloadMbps = &rate
		s.Results[index].DownloadLimited = total >= model.SpeedTestMaxBytes
	})
	return nil
}

type speedUploadReader struct {
	ctx       context.Context
	buf       []byte
	remaining int64
	deadline  time.Time
	sent      atomic.Int64
	finished  atomic.Bool
}

func (r *speedUploadReader) Read(p []byte) (int, error) {
	if r.ctx.Err() != nil {
		return 0, r.ctx.Err()
	}
	if r.remaining <= 0 || time.Now().After(r.deadline) {
		r.finished.Store(true)
		return 0, io.EOF
	}
	n := min(len(p), len(r.buf), int(r.remaining))
	copy(p, r.buf[:n])
	r.remaining -= int64(n)
	r.sent.Add(int64(n))
	return n, nil
}
func (m *speedMeasurement) measureLatency(ctx context.Context, h *speedHTTP, path string, index int) error {
	m.updateSpeed(func(s *SpeedTestStatus) { s.Stage = "latency" })
	endpoint := strings.Replace(h.base, "http://", "ws://", 1) + path + "/latency"
	connection, response, err := websocket.Dial(ctx, endpoint, &websocket.DialOptions{HTTPClient: h.client, HTTPHeader: h.headers(http.MethodGet, path+"/latency"), CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		if response != nil && response.Body != nil {
			response.Body.Close()
		}
		return fmt.Errorf("延迟测试连接失败：%w", err)
	}
	defer connection.CloseNow()
	if err := version.Check(version.Current, response.Header.Get("X-FnCPN-Version")); err != nil {
		return err
	}
	readCtx := connection.CloseRead(ctx) // Process control Pong frames while Ping waits.
	var samples []float64
	for i := 0; i < 11; i++ {
		probeCtx, cancel := context.WithTimeout(readCtx, 2*time.Second)
		m.updateSpeed(func(s *SpeedTestStatus) { s.EchoSent++ })
		started := time.Now()
		err := connection.Ping(probeCtx)
		elapsed := float64(time.Since(started).Microseconds()) / 1000
		cancel()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err == nil {
			if i > 0 {
				samples = append(samples, elapsed)
			}
			m.updateSpeed(func(s *SpeedTestStatus) { s.EchoReceived = s.EchoSent; s.RTTMS = elapsed })
		}
		m.updateSpeed(func(s *SpeedTestStatus) { s.Samples = i; s.Progress = float64(i) / 10 })
		timer := time.NewTimer(900 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	if len(samples) == 0 {
		return errors.New("延迟测试未收到 NAS 响应")
	}
	slices.Sort(samples)
	median := samples[len(samples)/2]
	if len(samples)%2 == 0 {
		median = (median + samples[len(samples)/2-1]) / 2
	}
	timeouts := float64(10-len(samples)) * 10
	m.updateSpeed(func(s *SpeedTestStatus) {
		s.Results[index].LatencyMS = &median
		s.Results[index].TimeoutPercent = &timeouts
	})
	return nil
}

func (m *speedMeasurement) measureUpload(ctx context.Context, h *speedHTTP, path string, index int, fingerprint string) error {
	m.updateSpeed(func(s *SpeedTestStatus) { s.Stage = "upload"; s.Mbps = 0; s.Progress = 0 })
	payload := make([]byte, 64<<10)
	if _, err := rand.Read(payload); err != nil {
		return err
	}
	reader := &speedUploadReader{ctx: ctx, buf: payload, remaining: model.SpeedTestMaxBytes, deadline: time.Now().Add(9 * time.Second)}
	response, err := h.request(ctx, http.MethodPost, path+"/upload", reader)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		return errors.New("上传接口未返回 SSE 进度")
	}
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 4096), 16<<10)
	var previous model.SpeedTestStats
	meter := uploadProgressMeter{}
	event := ""
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event: ") {
			event = strings.TrimPrefix(line, "event: ")
			continue
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		if event == "error" {
			return errors.New("NAS 上传接收中断，请重试")
		}
		if event != "progress" && event != "complete" {
			continue
		}
		var stats model.SpeedTestStats
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &stats); err != nil {
			return errors.New("上传统计响应无效")
		}
		if stats.Bytes < previous.Bytes || stats.ElapsedMS < previous.ElapsedMS || stats.TotalBytes < stats.Bytes || stats.TotalBytes < previous.TotalBytes || stats.TotalBytes > model.SpeedTestMaxBytes || stats.ElapsedMS > 45000 {
			return errors.New("上传统计超出有效范围")
		}
		if err := m.checkSpeedPath(ctx, fingerprint); err != nil {
			return err
		}
		if event == "complete" {
			if stats.TotalBytes > reader.sent.Load() || stats.ElapsedMS < 500 {
				return errors.New("上传样本不足，请重试")
			}
			rate := float64(stats.Bytes) * 8 / float64(stats.ElapsedMS) / 1000
			m.updateSpeed(func(s *SpeedTestStatus) {
				s.Stage = "upload-complete"
				s.Mbps = rate
				s.Progress = 1
				s.Results[index].UploadMbps = &rate
				s.Results[index].UploadLimited = stats.TotalBytes >= model.SpeedTestMaxBytes
			})
			return nil
		}
		rate, stage := meter.observe(stats, time.Now(), reader.finished.Load())
		m.updateSpeed(func(s *SpeedTestStatus) {
			s.Stage = stage
			s.Mbps = rate
			s.Progress = min(1, float64(stats.ElapsedMS)/8000)
		})
		previous = stats

	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("上传中断：%w", err)
	}
	return errors.New("上传中断：未收到 NAS 完成确认")
}

// Heartbeats and the EOF confirmation are not zero-throughput samples. Smooth
// actual receiver increments across roughly one second, and distinguish a stall
// or a completed sender waiting for acknowledgment from payload transmission.
type uploadProgressMeter struct {
	samples  []model.SpeedTestStats
	previous model.SpeedTestStats
	lastData time.Time
	rate     float64
}

func (m *uploadProgressMeter) observe(stats model.SpeedTestStats, now time.Time, senderFinished bool) (float64, string) {
	if stats.TotalBytes > m.previous.TotalBytes {
		m.lastData = now
	}
	stage := "upload"
	if stats.ElapsedMS == 0 {
		stage = "upload-warmup"
	} else if stats.Bytes > m.previous.Bytes {
		if len(m.samples) == 0 {
			m.samples = append(m.samples, model.SpeedTestStats{})
		}
		for len(m.samples) > 1 && stats.ElapsedMS-m.samples[1].ElapsedMS >= 1000 {
			m.samples = m.samples[1:]
		}
		base := m.samples[0]
		if stats.ElapsedMS > base.ElapsedMS {
			m.rate = float64(stats.Bytes-base.Bytes) * 8 / float64(stats.ElapsedMS-base.ElapsedMS) / 1000
		}
		m.samples = append(m.samples, stats)
	} else if senderFinished {
		stage = "upload-confirming"
	} else if !m.lastData.IsZero() && now.Sub(m.lastData) >= time.Second {
		stage = "upload-waiting"
	}
	m.previous = stats
	return m.rate, stage
}

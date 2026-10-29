//go:build android

// Package uiperf records opt-in local UI diagnostics. Nothing is recorded by default.
package uiperf

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime/pprof"
	"sync"
	"sync/atomic"
	"time"
)

var active atomic.Bool
var session struct {
	sync.Mutex
	file    *os.File
	started time.Time
}

func Enabled() bool { return active.Load() }

// Capture is called only by the debug Android intent bridge, off the UI thread.
func Capture(directory string, duration time.Duration) {
	if !active.CompareAndSwap(false, true) {
		return
	}
	defer active.Store(false)
	cpu, err := os.Create(filepath.Join(directory, "FnCPN-menu.cpu.pprof"))
	if err != nil {
		return
	}
	defer cpu.Close()
	events, err := os.Create(filepath.Join(directory, "FnCPN-menu.events.jsonl"))
	if err != nil {
		return
	}
	defer events.Close()
	session.Lock()
	session.file = events
	session.started = time.Now()
	session.Unlock()
	defer func() { session.Lock(); session.file = nil; session.Unlock() }()
	if err = pprof.StartCPUProfile(cpu); err != nil {
		Mark("capture.failed", 0, 0)
		return
	}
	Mark("capture.started", 0, 0)
	time.Sleep(duration)
	pprof.StopCPUProfile()
	Mark("capture.finished", 0, 0)
}

// Mark contains only fixed stage names, durations and primitive counts.
func Mark(stage string, elapsed time.Duration, count int) {
	if !active.Load() {
		return
	}
	session.Lock()
	defer session.Unlock()
	if session.file == nil {
		return
	}
	_ = json.NewEncoder(session.file).Encode(struct {
		Stage      string `json:"stage"`
		SinceMS    int64  `json:"since_ms"`
		DurationUS int64  `json:"duration_us"`
		Count      int    `json:"count,omitempty"`
	}{stage, time.Since(session.started).Milliseconds(), elapsed.Microseconds(), count})
}

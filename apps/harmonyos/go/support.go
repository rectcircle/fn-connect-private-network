package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/support"
	"github.com/rectcircle/fn-connect-private-network/internal/version"
)

type supportArchive struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// Return a private file descriptor by name instead of fitting an entire ZIP into
// a control frame. Only the shared allowlist filter may populate this archive.
func exportSupport(directory string, diagnostics []byte) (supportArchive, error) {
	names := []string{"client.log.2", "client.log.1", "client.log", "privileged.log.2", "privileged.log.1", "privileged.log"}
	logs := map[string]string{}
	files := []map[string]any{}
	for _, name := range names {
		path := filepath.Join(directory, "logs", name)
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return supportArchive{}, err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		file, err := os.Open(path)
		if err != nil {
			return supportArchive{}, err
		}
		if info.Size() > 2<<20 {
			_, err = file.Seek(info.Size()-(2<<20), io.SeekStart)
		}
		var data []byte
		if err == nil {
			data, err = io.ReadAll(io.LimitReader(file, 2<<20))
		}
		file.Close()
		if err != nil {
			return supportArchive{}, err
		}
		logs[name] = string(data)
		files = append(files, map[string]any{"name": name, "size": len(data)})
	}
	dir := filepath.Join(directory, "exports")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return supportArchive{}, err
	}
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "fncpn-support-") && strings.HasSuffix(entry.Name(), ".zip") {
			info, err := entry.Info()
			if err == nil && time.Since(info.ModTime()) > 24*time.Hour {
				_ = os.Remove(filepath.Join(dir, entry.Name()))
			}
		}
	}
	name := fmt.Sprintf("fncpn-support-%d.zip", time.Now().UnixNano())
	path := filepath.Join(dir, name)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return supportArchive{}, err
	}
	metadata, _ := json.MarshalIndent(map[string]any{"format": 1, "capturedAt": time.Now().UTC(), "platform": "harmonyos", "productVersion": version.Current, "files": files}, "", "  ")
	err = support.Archive(f, diagnostics, logs, metadata,
		"FnCPN diagnostics and log snapshots / FnCPN 诊断与日志快照\n\nCapture time (UTC), version and file inventory: snapshot.json.\nclient.log: shared client; privileged.log: HarmonyOS platform adapter.\nExported diagnostics and logs are filtered to remove identities and credentials.\n采集时间、版本和文件列表见 snapshot.json。client.log 为共享客户端，privileged.log 为鸿蒙适配层。诊断和日志已按共享白名单脱敏。\n", names)
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(path)
		return supportArchive{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return supportArchive{}, err
	}
	return supportArchive{Name: name, Size: info.Size()}, nil
}

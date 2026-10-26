//go:build ios

package iosui

/*
#cgo LDFLAGS: -framework QuickLook -framework UniformTypeIdentifiers
#include <stdlib.h>
char *fncpn_ui_files_directory(int caches);
void fncpn_ui_logs(const char *directory);
void fncpn_ui_share_file(const char *path);
*/
import "C"
import (
	"context"
	"encoding/json"
	"errors"
	"github.com/rectcircle/fn-connect-private-network/mobile/internal/ioslogs"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
	"unsafe"
)

var logSync sync.Mutex
var lastLogSync time.Time

func fileDirectory(caches bool) (string, error) {
	var flag C.int
	if caches {
		flag = 1
	}
	p := C.fncpn_ui_files_directory(flag)
	if p == nil {
		return "", errors.New("cannot access iOS documents")
	}
	defer C.free(unsafe.Pointer(p))
	return C.GoString(p), nil
}
func logsDirectory() (string, error) {
	base, err := fileDirectory(false)
	return filepath.Join(base, "Logs"), err
}
func syncLogsLocked(ctx context.Context) (bool, error) {
	dir, err := logsDirectory()
	if err != nil {
		return false, err
	}
	var local *ioslogs.Store
	if Simulator() {
		base, err := simulatorLogDirectory()
		if err != nil {
			return false, err
		}
		local = ioslogs.NewStore(filepath.Join(base, "logs"))
		defer local.Close()
	}
	return ioslogs.Sync(ctx, dir, func(ctx context.Context, r ioslogs.Request) (ioslogs.Reply, error) {
		if local != nil {
			return local.Handle(r)
		}
		var result ioslogs.Reply
		err := Call(ctx, "ios-log-snapshot", r, &result)
		return result, err
	})
}
func scheduleLogSync() {
	go func() {
		if !logSync.TryLock() {
			return
		}
		defer logSync.Unlock()
		if time.Since(lastLogSync) < time.Minute {
			return
		}
		lastLogSync = time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = syncLogsLocked(ctx)
	}()
}
func prepareLogs() (string, error) {
	logSync.Lock()
	defer logSync.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	_, syncErr := syncLogsLocked(ctx)
	dir, err := logsDirectory()
	if err != nil {
		return "", err
	}
	logs, err := ioslogs.Cached(dir)
	if err != nil {
		return "", err
	}
	if syncErr != nil && len(logs) == 0 {
		return "", syncErr
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	readme := ioslogs.SnapshotReadme
	if len(logs) == 0 {
		readme = ioslogs.NoLogsReadme
	}
	if err = os.WriteFile(filepath.Join(dir, "README.txt"), []byte(readme), 0600); err != nil {
		return "", err
	}
	return dir, nil
}
func OpenLogs() error {
	dir, err := prepareLogs()
	if err != nil {
		return err
	}
	p := C.CString(dir)
	defer C.free(unsafe.Pointer(p))
	C.fncpn_ui_logs(p)
	return nil
}
func ExportSupport(diagnostics string) error {
	dir, err := prepareLogs()
	if err != nil {
		return err
	}
	logSync.Lock()
	defer logSync.Unlock()
	logs, err := ioslogs.Cached(dir)
	if err != nil {
		return err
	}
	// Re-encode only known metadata fields from the user-accessible directory.
	var metadata []byte
	metaFile, metaErr := os.Open(filepath.Join(dir, "snapshot.json"))
	var metaData []byte
	if metaErr == nil {
		metaData, _ = io.ReadAll(io.LimitReader(metaFile, 16384))
		metaFile.Close()
	}
	if data := metaData; len(data) > 0 && len(data) < 16384 {
		var m ioslogs.Metadata
		if json.Unmarshal(data, &m) == nil && m.Format == 1 {
			valid := len(m.Files) <= len(ioslogs.Names)
			for _, f := range m.Files {
				valid = valid && ioslogs.Allowed(f.Name) && f.Size >= 0 && f.Size <= ioslogs.MaxFileSize
			}
			if valid {
				metadata, _ = json.Marshal(m)
			}
		}
	}
	cache, err := fileDirectory(true)
	if err != nil {
		return err
	}
	exports := filepath.Join(cache, "Support")
	if err = os.MkdirAll(exports, 0700); err != nil {
		return err
	}
	entries, _ := os.ReadDir(exports)
	for _, e := range entries {
		info, err := e.Info()
		if err == nil && time.Since(info.ModTime()) > 24*time.Hour {
			_ = os.Remove(filepath.Join(exports, e.Name()))
		}
	}
	f, err := os.CreateTemp(exports, "FnCPN-support-*.zip")
	if err != nil {
		return err
	}
	err = ioslogs.Archive(f, []byte(diagnostics), logs, metadata)
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		os.Remove(f.Name())
		return errors.Join(err, closeErr)
	}
	p := C.CString(f.Name())
	defer C.free(unsafe.Pointer(p))
	C.fncpn_ui_share_file(p)
	return nil
}

package ioslogs

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/rectcircle/fn-connect-private-network/internal/support"
	"io"
	"os"
	"path/filepath"
	"time"
)

type Call func(context.Context, Request) (Reply, error)

const SnapshotReadme = `FnCPN log snapshots / FnCPN 日志快照

These files are the most recently synchronized snapshots, not live logs.
See snapshot.json for the capture time (UTC). Open logs again to refresh.
client.log: shared client daemon. privileged.log: iOS tunnel and network adapter.
An inactive extension cannot supply new logs; the previous snapshot is retained.
Logs in an exported support ZIP are filtered to remove identities and credentials.

这些文件是最近同步的快照，不是实时日志。同步时间（UTC）见 snapshot.json。
重新打开“打开日志”可刷新。client.log 是共享客户端日志；privileged.log 是 iOS
隧道与网络适配日志。扩展停止后无法读取新日志，保留上次快照。
导出的诊断 ZIP 中，日志经过筛选以去除身份和凭据。
`

const NoLogsReadme = "No log snapshot is available yet. Viewing logs does not start the VPN.\n尚无日志快照。查看日志不会启动 VPN。\n\n" + SnapshotReadme

type Metadata struct {
	Format     int       `json:"format"`
	CapturedAt time.Time `json:"capturedAt"`
	Files      []File    `json:"files"`
}

// Sync publishes only complete snapshots. Offline calls preserve cached files.
func Sync(ctx context.Context, directory string, call Call) (bool, error) {
	r, err := call(ctx, Request{Action: "create"})
	if err != nil {
		return false, err
	}
	if !r.Available {
		return false, nil
	}
	defer call(context.Background(), Request{Action: "close", Token: r.Token})
	if r.Token == "" || len(r.Files) > len(Names) {
		return false, errors.New("invalid log snapshot manifest")
	}
	parent := filepath.Dir(directory)
	if err := os.MkdirAll(parent, 0700); err != nil {
		return false, err
	}
	tmp, err := os.MkdirTemp(parent, ".logs-")
	if err != nil {
		return false, err
	}
	defer os.RemoveAll(tmp)
	seen := map[string]bool{}
	for _, file := range r.Files {
		if !Allowed(file.Name) || seen[file.Name] || file.Size < 0 || file.Size > MaxFileSize {
			return false, errors.New("invalid log snapshot file")
		}
		seen[file.Name] = true
		f, err := os.OpenFile(filepath.Join(tmp, file.Name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return false, err
		}
		for offset := int64(0); offset < file.Size; {
			page, err := call(ctx, Request{Action: "read", Token: r.Token, Name: file.Name, Offset: offset})
			if err != nil {
				f.Close()
				return false, err
			}
			if !page.Available || len(page.Data) == 0 || len(page.Data) > ChunkSize || int64(len(page.Data)) > file.Size-offset {
				f.Close()
				return false, errors.New("incomplete log snapshot")
			}
			if _, err = f.Write(page.Data); err != nil {
				f.Close()
				return false, err
			}
			offset += int64(len(page.Data))
		}
		if err := f.Close(); err != nil {
			return false, err
		}
	}
	meta, err := json.MarshalIndent(Metadata{Format: 1, CapturedAt: time.Now().UTC(), Files: r.Files}, "", "  ")
	if err != nil {
		return false, err
	}
	if err = os.WriteFile(filepath.Join(tmp, "snapshot.json"), meta, 0600); err != nil {
		return false, err
	}
	if err = os.WriteFile(filepath.Join(tmp, "README.txt"), []byte(SnapshotReadme), 0600); err != nil {
		return false, err
	}
	backup := tmp + "-previous"
	if err = os.Rename(directory, backup); err != nil && !os.IsNotExist(err) {
		return false, err
	}
	if err = os.Rename(tmp, directory); err != nil {
		_ = os.Rename(backup, directory)
		return false, err
	}
	_ = os.RemoveAll(backup)
	return true, nil
}

func Cached(directory string) (map[string]string, error) {
	result := map[string]string{}
	for _, name := range Names {
		path := filepath.Join(directory, name)
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() || info.Size() > MaxFileSize {
			return nil, errors.New("invalid cached log")
		}
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(io.LimitReader(f, MaxFileSize+1))
		f.Close()
		if err != nil {
			return nil, err
		}
		if len(data) > MaxFileSize {
			return nil, errors.New("cached log is too large")
		}
		result[name] = string(data)
	}
	return result, nil
}

func Archive(w io.Writer, diagnostics []byte, logs map[string]string, metadata []byte) error {
	return support.Archive(w, diagnostics, logs, metadata, SnapshotReadme, Names)
}

package ioslogs

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSnapshotBoundsReads(t *testing.T) {
	dir := t.TempDir()
	data := bytes.Repeat([]byte("x"), ChunkSize+123)
	if err := os.WriteFile(filepath.Join(dir, "client.log"), data, 0600); err != nil {
		t.Fatal(err)
	}
	s := NewStore(dir)
	defer s.Close()
	r, err := s.Handle(Request{Action: "create"})
	if err != nil {
		t.Fatal(err)
	}
	var got []byte
	for offset := int64(0); offset < int64(len(data)); {
		p, err := s.Handle(Request{Action: "read", Token: r.Token, Name: "client.log", Offset: offset})
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, p.Data...)
		offset += int64(len(p.Data))
	}
	if !bytes.Equal(got, data) {
		t.Fatal("snapshot content mismatch")
	}
	for _, q := range []Request{{Action: "read", Token: r.Token, Name: "../client.json"}, {Action: "read", Token: "wrong", Name: "client.log"}, {Action: "read", Token: r.Token, Name: "client.log", Offset: -1}, {Action: "read", Token: r.Token, Name: "client.log", Offset: MaxFileSize + 1}} {
		if _, err := s.Handle(q); err == nil {
			t.Fatal("invalid read accepted")
		}
	}
	if _, err := s.Handle(Request{Action: "close", Token: r.Token}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Handle(Request{Action: "read", Token: r.Token, Name: "client.log"}); err == nil {
		t.Fatal("closed token accepted")
	}
}

func TestSyncPreservesCacheOnFailureAndOffline(t *testing.T) {
	dir := t.TempDir()
	storeDir := t.TempDir()
	payload := `{"msg":"client connected","state":"DIRECT"}`
	os.WriteFile(filepath.Join(storeDir, "client.log"), []byte(payload), 0600)
	os.WriteFile(filepath.Join(storeDir, "privileged.log"), []byte(`{"msg":"network settings applied"}`), 0600)
	s := NewStore(storeDir)
	defer s.Close()
	call := func(ctx context.Context, r Request) (Reply, error) { return s.Handle(r) }
	cache := filepath.Join(dir, "Logs")
	if ok, err := Sync(t.Context(), cache, call); err != nil || !ok {
		t.Fatalf("sync %v %v", ok, err)
	}
	before, _ := os.ReadFile(filepath.Join(cache, "snapshot.json"))
	if _, err := Sync(t.Context(), cache, func(_ context.Context, r Request) (Reply, error) {
		if r.Action == "read" {
			return Reply{}, errors.New("provider stopped")
		}
		return s.Handle(r)
	}); err == nil {
		t.Fatal("incomplete transfer succeeded")
	}
	if _, err := Sync(t.Context(), cache, func(context.Context, Request) (Reply, error) { return Reply{Available: false}, nil }); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(cache, "snapshot.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("failed/offline sync changed snapshot timestamp")
	}
	logs, err := Cached(cache)
	if err != nil || logs["client.log"] != payload || logs["privileged.log"] == "" {
		t.Fatal("lost cached component logs")
	}
}

func TestArchiveIncludesBothLogsAndFiltersSecrets(t *testing.T) {
	var b bytes.Buffer
	err := Archive(&b, []byte(`{"status":{"state":"DIRECT"},"password":"secret"}`), map[string]string{
		"client.log":     `{"msg":"client connected","password":"secret"}`,
		"privileged.log": `{"msg":"network settings applied","token":"secret"}`,
		"client.json":    "secret",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(b.Bytes()), int64(b.Len()))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, f := range z.File {
		seen[f.Name] = true
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(r)
		r.Close()
		if strings.Contains(string(data), "secret") {
			t.Fatal("archive leaked credentials")
		}
		if f.Name == "README.txt" && (!strings.Contains(string(data), "log snapshots") || !strings.Contains(string(data), "日志快照")) {
			t.Fatal("README must explain snapshots in English and Chinese")
		}
	}
	if !seen["README.txt"] || !seen["support.json"] || !seen["logs/client.log"] || !seen["logs/privileged.log"] || seen["logs/client.json"] {
		t.Fatal("archive contents incorrect")
	}
}

//go:build unix

package ioslogs

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// The iOS provider relies on Unix rename semantics: an open descriptor pins the
// old file through rotation. Windows does not allow this rename with os.Open.
func TestSnapshotPinsRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "client.log")
	data := bytes.Repeat([]byte("x"), ChunkSize+123)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	s := NewStore(dir)
	defer s.Close()
	r, err := s.Handle(Request{Action: "create"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("new generation"), 0600); err != nil {
		t.Fatal(err)
	}
	var got []byte
	for offset := int64(0); offset < int64(len(data)); {
		p, err := s.Handle(Request{Action: "read", Token: r.Token, Name: "client.log", Offset: offset})
		if err != nil {
			t.Fatal(err)
		}
		if len(p.Data) == 0 {
			t.Fatal("snapshot read made no progress")
		}
		got = append(got, p.Data...)
		offset += int64(len(p.Data))
	}
	if !bytes.Equal(got, data) {
		t.Fatal("rotation mixed file generations")
	}
}

package support

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestAndroidArchiveMatchesMobileFormat(t *testing.T) {
	var b bytes.Buffer
	err := Archive(&b, []byte(`{"status":{"state":"DIRECT"},"password":"secret"}`), map[string]string{
		"client.log":  `{"msg":"client connected","token":"secret"}`,
		"android.log": `{"event":"service_create","password":"secret"}`,
		"client.json": "secret",
	}, []byte(`{"format":1,"capturedAt":"2026-10-25T00:00:00Z","platform":"android"}`), "English / 中文", []string{"client.log", "android.log"})
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
		data, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "secret") {
			t.Fatal("credentials leaked")
		}
	}
	for _, name := range []string{"support.json", "snapshot.json", "README.txt", "logs/client.log", "logs/android.log"} {
		if !seen[name] {
			t.Fatalf("missing %s", name)
		}
	}
	if len(seen) != 5 {
		t.Fatal(seen)
	}
}

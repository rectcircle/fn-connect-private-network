package logging

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCrashOutputCapturesWorkerPanic(t *testing.T) {
	if path := os.Getenv("FNCPN_TEST_CRASH_PATH"); path != "" {
		if err := EnableCrashOutput(path); err != nil {
			panic(err)
		}
		go func() { panic("worker crash fixture") }()
		select {}
	}
	path := filepath.Join(t.TempDir(), "client.log.crash")
	for i := 0; i < 2; i++ {
		cmd := exec.Command(os.Args[0], "-test.run=^TestCrashOutputCapturesWorkerPanic$")
		cmd.Env = append(os.Environ(), "FNCPN_TEST_CRASH_PATH="+path, "GOTRACEBACK=single")
		if err := cmd.Run(); err == nil {
			t.Fatal("panic subprocess succeeded")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "panic: worker crash fixture") || !strings.Contains(string(data), "crash_test.go:") {
			t.Fatalf("missing panic/stack: %s", data)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0600 {
			t.Fatalf("crash file mode: %v", info.Mode())
		}
	}
	data, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "worker crash fixture") {
		t.Fatal("previous crash was not retained")
	}
}

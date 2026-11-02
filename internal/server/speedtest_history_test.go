//go:build !windows

package server

import (
	"archive/tar"
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// This opt-in integration test compiles the unmodified released RemoteClient
// decoder against the candidate server, including the newly extended response.
// It is targeted protocol evidence, not a substitute for full release acceptance.
func TestReleasedClientsAcceptExtendedPrivateAPI(t *testing.T) {
	if os.Getenv("FNCPN_TEST_HISTORY") != "1" {
		t.Skip("set FNCPN_TEST_HISTORY=1 to compile historical clients")
	}
	s, d := speedFixture(t)
	gatewayHandler := NewHTTPHandler(s.store, s)
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Set("X-Trim-Userid", "compatibility-test")
		gatewayHandler.ServeHTTP(w, r)
	}))
	defer gateway.Close()
	root, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, tag := range []string{"v1.0.0", "v1.0.9", "v1.1.0", "v1.1.8"} {
		t.Run(tag, func(t *testing.T) {
			command := exec.Command("git", "archive", tag)
			command.Dir = string(bytes.TrimSpace(root))
			archive, err := command.Output()
			if err != nil {
				t.Fatal("historical source unavailable", err)
			}
			directory := t.TempDir()
			reader := tar.NewReader(bytes.NewReader(archive))
			for {
				entry, err := reader.Next()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(directory, entry.Name)
				if entry.Typeflag == tar.TypeDir {
					if err := os.MkdirAll(path, 0755); err != nil {
						t.Fatal(err)
					}
					continue
				}
				if entry.Typeflag != tar.TypeReg {
					continue
				}
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
				data, err := io.ReadAll(reader)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			testSource := `package client
import("context";"os";"reflect";"testing")
func TestCandidatePrivateAPICompatibility(t *testing.T){
 remote,err:=NewRemoteClient(os.Getenv("FNCPN_CANDIDATE_URL"),nil,nil,nil);if err!=nil{t.Fatal(err)}
 if _,err:=remote.Bootstrap(context.Background());err!=nil{t.Fatal(err)}
 method:=reflect.ValueOf(remote).MethodByName("PrivateAPIConfiguration");if !method.IsValid(){method=reflect.ValueOf(remote).MethodByName("LocalProbeConfiguration")}
 if !method.IsValid(){t.Fatal("released configuration method missing")}
 result:=method.Call([]reflect.Value{reflect.ValueOf(context.Background()),reflect.ValueOf(os.Getenv("FNCPN_CANDIDATE_DEVICE"))})
 if !result[1].IsNil(){t.Fatal(result[1].Interface())}
 configuration:=result[0];if configuration.FieldByName("Key").String()=="" || configuration.FieldByName("Endpoints").Len()==0{t.Fatal("historical fields were lost")}
}`
			if err := os.WriteFile(filepath.Join(directory, "internal/client/candidate_compatibility_test.go"), []byte(testSource), 0600); err != nil {
				t.Fatal(err)
			}
			run := exec.CommandContext(t.Context(), "go", "test", "./internal/client", "-run", "^TestCandidatePrivateAPICompatibility$", "-count=1")
			run.Dir = directory
			run.Env = append(os.Environ(), "FNCPN_CANDIDATE_URL="+gateway.URL, "FNCPN_CANDIDATE_DEVICE="+d.ID)
			if output, err := run.CombinedOutput(); err != nil {
				t.Fatalf("%v\n%s", err, output)
			}
		})
	}
}

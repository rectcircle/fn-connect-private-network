package main

import (
	"context"
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNASRedirectPortAndLoop(t *testing.T) {
	for _, cycle := range []bool{false, true} {
		t.Run(map[bool]string{false: "web port", true: "loop"}[cycle], func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/" || cycle {
					http.Redirect(w, r, "/web/", http.StatusFound)
					return
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()
			got, err := resolveNASRedirects(context.Background(), server.URL+"/", server.Client().Transport)
			if cycle {
				if err == nil {
					t.Fatal("must reject redirect cycles")
				}
			} else if err != nil || got != server.URL+"/web/" {
				t.Fatalf("url %q error %v", got, err)
			}
		})
	}
}
func TestNASRedirectRejectsOtherHost(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.com/", http.StatusFound)
	}))
	defer server.Close()
	if _, err := resolveNASRedirects(context.Background(), server.URL, server.Client().Transport); err == nil {
		t.Fatal("must not follow other hosts")
	}
}
func TestCopyOnlyDNSSuffix(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	v := newClientView(&silentToastBackend{})
	defer v.cancel()
	clipboard := test.NewClipboard()
	v.window = &clipboardWindow{Window: app.NewWindow("test"), clipboard: clipboard}
	v.current.DNS.State = "ready"
	v.current.DNS.Suffix = "home.fncpn"
	v.copyDNSSuffix()
	if got := v.window.Clipboard().Content(); got != "home.fncpn" {
		t.Fatal(got)
	}
}

type clipboardWindow struct {
	fyne.Window
	clipboard fyne.Clipboard
}

func (w *clipboardWindow) Clipboard() fyne.Clipboard { return w.clipboard }

type silentToastBackend struct{ exitBackend }

func (*silentToastBackend) Toast(string) error { return nil }

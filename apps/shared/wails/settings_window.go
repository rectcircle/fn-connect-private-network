package main

import "net/http"

// Each Wails v2 process owns one window. The settings process shares the daemon
// and assets, but uses its own entry point and single-instance identity.
func settingsAssets(settings bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if settings && (r.URL.Path == "/" || r.URL.Path == "/index.html") {
				cloned := r.Clone(r.Context())
				address := *r.URL
				address.Path = "/settings-window.html"
				cloned.URL = &address
				r = cloned
			}
			next.ServeHTTP(w, r)
		})
	}
}

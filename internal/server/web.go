package server

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

const gatewayPrefix = "/app/fncpn"

//go:embed web/index.html
var embeddedWeb embed.FS

func serveIndex(writer http.ResponseWriter, request *http.Request) {
	if request.URL.Path != "/" {
		http.NotFound(writer, request)
		return
	}
	content, err := fs.ReadFile(embeddedWeb, "web/index.html")
	if err != nil {
		http.Error(writer, "application UI unavailable", http.StatusInternalServerError)
		return
	}
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.Header().Set(
		"Content-Security-Policy",
		"default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'",
	)
	_, _ = writer.Write(content)
}

func stripGatewayPrefix(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == gatewayPrefix {
			request.URL.Path = "/"
		} else if strings.HasPrefix(request.URL.Path, gatewayPrefix+"/") {
			request.URL.Path = strings.TrimPrefix(request.URL.Path, gatewayPrefix)
		}
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(writer, request)
	})
}

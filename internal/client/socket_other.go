//go:build !android

package client

import "net/http"

func configurePlatformTransport(*http.Transport) {}

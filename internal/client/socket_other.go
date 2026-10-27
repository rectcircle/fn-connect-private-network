//go:build !android && !openharmony

package client

import "net/http"

func configurePlatformTransport(*http.Transport) {}

//go:build android

package client

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"sync"
	"syscall"
	"time"
)

var androidSockets struct {
	sync.RWMutex
	protect func(int) error
	resolve func(string) ([]string, error)
}

func SetAndroidHostResolver(resolve func(string) ([]string, error)) {
	androidSockets.Lock()
	androidSockets.resolve = resolve
	androidSockets.Unlock()
}

// SetAndroidSocketProtector installs the system-owned VPN socket adapter.
// It must be configured before starting Manager and removed after Close.
func SetAndroidSocketProtector(protect func(int) error) {
	androidSockets.Lock()
	androidSockets.protect = protect
	androidSockets.Unlock()
}

func protectAndroidSocket(fd int) error {
	androidSockets.RLock()
	protect := androidSockets.protect
	androidSockets.RUnlock()
	if protect == nil {
		return errors.New("Android physical network adapter is unavailable")
	}
	return protect(fd)
}

func androidControl(_, _ string, raw syscall.RawConn) error {
	var failure error
	if err := raw.Control(func(fd uintptr) { failure = protectAndroidSocket(int(fd)) }); err != nil {
		return err
	}
	return failure
}

func configurePlatformTransport(t *http.Transport) {
	t.Proxy = nil
	t.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		addresses := []string{host}
		if _, err := netip.ParseAddr(host); err != nil {
			androidSockets.RLock()
			resolve := androidSockets.resolve
			androidSockets.RUnlock()
			if resolve == nil {
				return nil, errors.New("Android network resolver is unavailable")
			}
			addresses, err = resolve(host)
			if err != nil {
				return nil, err
			}
		}
		var failures []error
		for _, ip := range addresses {
			parsed, err := netip.ParseAddr(ip)
			if err != nil {
				return nil, errors.New("invalid physical DNS answer")
			}
			if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
				return nil, errors.New("invalid port")
			}
			conn, err := (&net.Dialer{Control: androidControl, Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext(ctx, network, net.JoinHostPort(parsed.String(), port))
			if err == nil {
				return conn, nil
			}
			failures = append(failures, err)
		}
		return nil, errors.Join(append(failures, errors.New("physical network connection failed"))...)
	}
}

func bindSocketToInterface(fd uintptr, _ int) error { return protectAndroidSocket(int(fd)) }

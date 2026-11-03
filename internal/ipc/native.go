package ipc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
)

// NativeEndpoint is obtained over the platform's authenticated system bridge.
type NativeEndpoint struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

var nativeToken = regexp.MustCompile(`^[a-f0-9]{64}$`)

// StreamNative adapts sandbox messaging to IPC without UI-specific protocols or
// task state. Cancellation closes the HTTP stream and cancels its daemon call.
func StreamNative(ctx context.Context, endpoint NativeEndpoint, method string, params any, receive func(json.RawMessage) (bool, error)) error {
	u, err := url.Parse(endpoint.URL)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.User != nil || u.Path != "/ipc" || u.RawQuery != "" || u.Fragment != "" || !nativeToken.MatchString(endpoint.Token) {
		return errors.New("invalid native IPC endpoint")
	}
	id, err := requestID()
	if err != nil {
		return err
	}
	request, err := NewRequest(id, method, params)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(request)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.URL, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+endpoint.Token)
	req.Header.Set("Content-Type", "application/json")
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return errors.New("native IPC request rejected")
	}
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 4096), MaxFrameSize)
	for scanner.Scan() {
		var reply Response
		if err := json.Unmarshal(scanner.Bytes(), &reply); err != nil {
			return err
		}
		if reply.Version != ProtocolVersion || reply.ID != id {
			return errors.New("native IPC response mismatch")
		}
		if !reply.OK {
			if reply.Error == nil {
				return errors.New("native IPC request failed")
			}
			return reply.Error
		}
		done, err := receive(reply.Result)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return errors.New("native IPC stream ended before completion")
}

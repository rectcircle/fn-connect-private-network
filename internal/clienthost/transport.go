package clienthost

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/ipc"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

// NativeTransport is a transport adapter for sandboxed native UIs whose system
// messaging API supports only one response. It forwards the normal IPC contract;
// it holds no measurement state and never starts or selects a network.
type NativeTransport struct {
	Version int    `json:"version"`
	URL     string `json:"url"`
	Token   string `json:"token"`
}

func (h *Host) nativeTransport() (NativeTransport, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed || h.ctx == nil || h.ctx.Err() != nil {
		return NativeTransport{}, model.NewError(model.ErrorUnavailable, "client service is not running", true)
	}
	if h.transport.URL != "" {
		return h.transport, nil
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return NativeTransport{}, err
	}
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		listener.Close()
		return NativeTransport{}, err
	}
	h.transport = NativeTransport{Version: ipc.ProtocolVersion, URL: "http://" + listener.Addr().String() + "/ipc", Token: hex.EncodeToString(token)}
	credential := "Bearer " + h.transport.Token
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 10 * time.Second, MaxHeaderBytes: 8192, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/ipc" || r.URL.RawQuery != "" || r.Header.Get("Origin") != "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte(credential)) != 1 {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if !strings.HasPrefix(r.Host, "127.0.0.1:") {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, ipc.MaxFrameSize)
		var request ipc.Request
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Version != ipc.ProtocolVersion {
			http.Error(w, "invalid IPC request", http.StatusBadRequest)
			return
		}
		// Full duplex is required: body decoding must not wait for the stream to end.
		_ = http.NewResponseController(w).EnableFullDuplex()
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Header().Set("Cache-Control", "no-store")
		encoder := json.NewEncoder(w)
		err := h.Stream(r.Context(), request.Method, request.Params, func(payload json.RawMessage) (bool, error) {
			err := encoder.Encode(ipc.Success(request.ID, payload))
			if err == nil {
				err = http.NewResponseController(w).Flush()
			}
			return false, err
		})
		if err != nil && r.Context().Err() == nil {
			_ = encoder.Encode(ipc.Failure(request.ID, model.PublicError(err)))
		}
	})}
	go func() { <-h.ctx.Done(); _ = server.Close() }()
	go func() { _ = server.Serve(listener) }()
	return h.transport, nil
}

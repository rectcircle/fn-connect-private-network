package iosvpn

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type streamSystem struct {
	server        string
	connection    net.Conn
	opens, closes int
}

func (s *streamSystem) WritePacket([]byte) error { return nil }
func (s *streamSystem) Call(method string, params, result any) error {
	b, _ := json.Marshal(params)
	var p struct {
		Data  []byte
		Limit int
	}
	_ = json.Unmarshal(b, &p)
	reply := func(value any) error { b, _ := json.Marshal(value); return json.Unmarshal(b, result) }
	switch method {
	case "tcp.open":
		var err error
		s.connection, err = net.Dial("tcp", s.server)
		s.opens++
		if err != nil {
			return err
		}
		return reply(map[string]string{"id": "test-stream"})
	case "tcp.write":
		_, err := s.connection.Write(p.Data)
		return err
	case "tcp.read":
		buf := make([]byte, p.Limit)
		n, err := s.connection.Read(buf)
		if err != nil && err != io.EOF {
			return err
		}
		return reply(map[string]any{"data": buf[:n]})
	case "tcp.close":
		s.closes++
		return s.connection.Close()
	}
	return errors.New("unexpected platform call")
}
func TestPrivateHTTPUsesPlatformStreamAndRejectsPublicTargets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/dns" {
			t.Error("wrong request path")
		}
		_, _ = w.Write([]byte("private DNS response"))
	}))
	defer server.Close()
	sys := &streamSystem{server: server.Listener.Addr().String()}
	engine := NewEngine(sys)
	engine.plan = &model.ClientPlan{Mode: "RELAY"}
	transport := &http.Transport{DialContext: engine.DialPrivate}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	response, err := client.Get("http://192.168.1.10:54999/dns")
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || string(data) != "private DNS response" {
		t.Fatalf("%s %v", data, err)
	}
	transport.CloseIdleConnections()
	if sys.opens != 1 || sys.closes != 1 {
		t.Fatalf("stream lifecycle %d/%d", sys.opens, sys.closes)
	}
	for _, address := range []string{"example.com:54790", "8.8.8.8:54790", "192.168.1.10:0"} {
		if _, err := engine.DialPrivate(context.Background(), "tcp", address); err == nil {
			t.Fatalf("accepted %s", address)
		}
	}
	if sys.opens != 1 {
		t.Fatal("invalid target reached native bridge")
	}
}

// net/http reads idle connections in the background, and configuration watches
// legitimately wait 25 seconds. Neither may acquire an implicit 3-second deadline.
func TestPrivateStreamTimeoutFollowsExplicitDeadline(t *testing.T) {
	c := &privateConn{}
	for _, read := range []bool{true, false} {
		if timeout, err := c.timeout(read); err != nil || timeout != 0 {
			t.Fatalf("implicit deadline: %d %v", timeout, err)
		}
	}
	c.SetDeadline(time.Now().Add(25 * time.Second))
	if timeout, err := c.timeout(true); err != nil || timeout < 24000 {
		t.Fatalf("watch deadline shortened: %d %v", timeout, err)
	}
	c.SetReadDeadline(time.Now().Add(-time.Second))
	if _, err := c.timeout(true); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired deadline: %v", err)
	}
	c.closed = true
	if _, err := c.timeout(false); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("closed connection: %v", err)
	}
}

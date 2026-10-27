package client

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/coder/websocket"
	"github.com/rectcircle/fn-connect-private-network/internal/logging"
)

func TestRecoveryLogsDistinguishMethodsWithoutCredentials(t *testing.T) {
	for _, kind := range []string{"cli", "web"} {
		for _, fallback := range []bool{false, true} {
			name := kind + "/short"
			if fallback {
				name = kind + "/long"
			}
			t.Run(name, func(t *testing.T) {
				session := webRecoverySession()
				const newToken = "rotated-token-canary"
				var output bytes.Buffer
				ctx := logging.WithLogger(t.Context(), slog.New(slog.NewJSONHandler(&output, nil)))
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					conn, err := websocket.Accept(w, r, nil)
					if err != nil {
						t.Error(err)
						return
					}
					defer conn.CloseNow()
					for {
						si := nativeTestRequest(t, r.Context(), conn, "")
						nativeTestResponse(t, r.Context(), conn, si, map[string]any{"result": "succ", "si": "log-test-si"})
						req := nativeTestRequest(t, r.Context(), conn, session.Secret)
						if req["req"] == "user.authToken" && fallback {
							nativeTestResponse(t, r.Context(), conn, req, map[string]any{"result": "fail", "errno": 1001, "errmsg": session.Token + " " + session.LongToken + " " + session.Secret})
							if kind == "cli" {
								return
							}
							continue
						}
						token := session.Token
						if req["req"] == "user.tokenLogin" {
							token = newToken
						}
						nativeTestResponse(t, r.Context(), conn, req, map[string]any{"result": "succ", "token": token})
						return
					}
				}))
				defer server.Close()
				client := newNativeTestClient("ws" + strings.TrimPrefix(server.URL, "http"))
				if kind == "web" {
					if _, err := client.RecoverWeb(ctx, session, "Mac"); err != nil {
						t.Fatal(err)
					}
				} else {
					native := NativeSession{Version: nativeSessionVersion, FNID: session.FNID, Username: session.Username, DeviceID: session.DeviceID, Token: session.Token, LongToken: session.LongToken, Secret: session.Secret, UpdatedAt: session.UpdatedAt}
					if _, err := client.Recover(ctx, native, "Mac"); err != nil {
						t.Fatal(err)
					}
				}
				for _, credential := range []string{session.Token, session.LongToken, session.Secret, newToken, "web-signing-key"} {
					if strings.Contains(output.String(), credential) {
						t.Fatal("recovery log exposed a credential")
					}
				}
				var events []map[string]any
				decoder := json.NewDecoder(bytes.NewReader(output.Bytes()))
				for {
					var row map[string]any
					err := decoder.Decode(&row)
					if err == io.EOF {
						break
					}
					if err != nil {
						t.Fatal(err)
					}
					if row["msg"] != "fnOS session recovery attempt" || row["session_kind"] != kind || row["fn_id"] != session.FNID {
						t.Fatal("missing recovery log identity")
					}
					events = append(events, row)
				}
				expected := 2
				if fallback {
					expected = 4
				}
				if len(events) != expected {
					t.Fatalf("events=%d want=%d", len(events), expected)
				}
				if events[0]["method"] != "user.authToken" || events[0]["outcome"] != "started" {
					t.Fatal("missing short-token attempt")
				}
				last := events[len(events)-1]
				method := "user.authToken"
				if fallback {
					method = "user.tokenLogin"
				}
				if last["method"] != method || last["outcome"] != "succeeded" || last["token_changed"] != fallback || last["long_token_changed"] != false {
					t.Fatal("wrong recovery outcome or token change metadata")
				}
				if fallback && (events[1]["outcome"] != "failed" || events[1]["code"] != "AUTH_REQUIRED" || events[2]["method"] != "user.tokenLogin") {
					t.Fatal("missing long-token fallback sequence")
				}
			})
		}
	}
}

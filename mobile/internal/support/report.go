// Package support creates an allowlisted report, never an archive of raw files.
package support

import (
	"bufio"
	"encoding/json"
	"strings"
)

// Unknown fields and free-form error text are omitted deliberately. Identity,
// endpoint, credential and request/response payload fields are never exported.
var fields = map[string]bool{}

func init() {
	for _, key := range strings.Fields("time level platform event errorType msg state path reason code operation retryable http_status remote_code attempts ipv6_candidates local_public_ipv6 ipv6_forbidden elapsed_ms direct localPublicIPv6 candidateCount attemptCount retryAfter lastError status dns errors active healthy mtu MTU interface privilegedAvailable privilegedActive privilegedDegraded administrator checkedAt") {
		fields[key] = true
	}
}

var events = map[string]bool{}

func init() {
	for _, event := range []string{"FN Connect discovery completed", "direct path unavailable", "direct connection attempted", "direct connection failed; trying next path", "client connected", "client reconnect requested", "WireGuard handshake waiting", "WireGuard handshake confirmed", "WireGuard handshake still pending"} {
		events[event] = true
	}
}
func clean(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := map[string]any{}
		for key, item := range v {
			if !fields[key] {
				continue
			}
			if key == "msg" {
				if text, ok := item.(string); !ok || !events[text] {
					continue
				}
			}
			out[key] = clean(item)
		}
		return out
	case []any:
		out := make([]any, 0, len(v))
		for _, item := range v {
			out = append(out, clean(item))
		}
		return out
	default:
		return value
	}
}
func Report(diagnostics []byte, logs map[string]string) ([]byte, error) {
	var report any
	if len(diagnostics) > 0 {
		if err := json.Unmarshal(diagnostics, &report); err != nil {
			return nil, err
		}
	}
	rows := map[string][]any{}
	for name, text := range logs {
		// Filenames are supplied by the fixed system adapter, not arbitrary paths.
		if !strings.HasPrefix(name, "client.log") && !strings.HasPrefix(name, "android.log") {
			continue
		}
		scan := bufio.NewScanner(strings.NewReader(text))
		scan.Buffer(make([]byte, 4096), 256*1024)
		for scan.Scan() {
			var row any
			if json.Unmarshal(scan.Bytes(), &row) == nil {
				rows[name] = append(rows[name], clean(row))
			}
		}
	}
	return json.MarshalIndent(map[string]any{"format": 1, "diagnostics": clean(report), "logs": rows}, "", "  ")
}

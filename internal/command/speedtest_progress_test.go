package command

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/rectcircle/fn-connect-private-network/internal/client"
)

func TestSpeedTestProgressClearsLongerPreviousFrames(t *testing.T) {
	var out bytes.Buffer
	width := 0
	// Both stage names and numeric fields can grow beyond their minimum widths.
	frames := []client.SpeedTestStatus{
		{Link: "fn-connect", Stage: "upload-confirming", Mbps: 123456.78, RTTMS: 12345.67},
		{Link: "fn-connect", Stage: "upload-waiting"},
		{Link: "fn-connect", Stage: "upload", Mbps: 3.78, RTTMS: 124.34},
		{Link: "fn-connect", Stage: "latency", Mbps: 3.78, RTTMS: 126.15},
		{Link: "fn-connect", Stage: "connecting", Mbps: 3.78, RTTMS: 126.15},
	}
	var screen []byte
	cursor := 0
	for _, status := range frames {
		out.Reset()
		if err := writeSpeedTestProgress(&out, &width, status); err != nil {
			t.Fatal(err)
		}
		for _, b := range out.Bytes() {
			if b == '\r' {
				cursor = 0
				continue
			}
			if cursor == len(screen) {
				screen = append(screen, b)
			} else {
				screen[cursor] = b
			}
			cursor++
		}
		expected := fmt.Sprintf("%-12s %s", status.Link, status.Stage)
		if status.Stage == "upload" {
			expected += "  3.78 Mbps"
		}
		if status.Stage == "latency" {
			expected += "  126.15 ms"
		}
		if strings.TrimRight(string(screen), " ") != expected {
			t.Fatalf("stale progress text: %q; want %q", screen, expected)
		}
	}
}

func TestSpeedTestTextPrintsCompletedLinksImmediatelyAndOnce(t *testing.T) {
	var out bytes.Buffer
	text := speedTestText{out: &out, printed: make(map[string]bool)}
	zero, down, up := 0.0, 21.0, 74.2
	ipv6 := client.SpeedTestResult{Link: "ipv6", Outcome: "success", LatencyMS: &zero, DownloadMbps: &down, UploadMbps: &up}
	status := client.SpeedTestStatus{Active: true, Stage: "upload", Link: "ipv6", Results: []client.SpeedTestResult{{Link: "ipv6", Outcome: "running", DownloadMbps: &down}}}
	if err := text.update(status); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "[success]") {
		t.Fatal("partial measurements must not print final results")
	}
	status.Link = "fn-connect"
	status.Stage = "connecting"
	status.Results = []client.SpeedTestResult{ipv6, {Link: "fn-connect", Outcome: "running"}}
	if err := text.update(status); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "ipv6 [success] | latency 0.00 ms | download 21.00 Mbps | upload 74.20 Mbps\n") {
		t.Fatalf("completed link not printed immediately: %q", out.String())
	}
	if err := text.update(status); err != nil {
		t.Fatal(err)
	}
	status.Active = false
	status.Stage = "completed"
	status.Results[1] = client.SpeedTestResult{Link: "fn-connect", Outcome: "failed", Error: "upload interrupted\nnetwork timeout", DownloadMbps: &down}
	if err := text.update(status); err != nil {
		t.Fatal(err)
	}
	if err := text.clear(); err != nil {
		t.Fatal(err)
	}
	if err := text.summary(status); err != nil {
		t.Fatal(err)
	}
	output := out.String()
	if strings.Count(output, "ipv6 [success]") != 1 || strings.Count(output, "fn-connect [failed]") != 1 {
		t.Fatalf("duplicate or missing result lines: %q", output)
	}
	if !strings.Contains(output, "upload — | upload interrupted network timeout\n") || !strings.Contains(output, "1 succeeded, 0 unavailable, 1 failed, 0 canceled.") {
		t.Fatalf("missing text report: %q", output)
	}
	if strings.ContainsAny(output, "{}") {
		t.Fatalf("unexpected JSON report: %q", output)
	}
}

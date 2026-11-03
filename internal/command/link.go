package command

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/client"
	"github.com/rectcircle/fn-connect-private-network/internal/ipc"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

func runLinkCommand(ctx context.Context, args []string, env Environment) error {
	invalid := func() error {
		return model.NewError(model.ErrorInvalidArgument, "use link list, priority ipv6 fn-connect, enable|disable <link>, reset, or speedtest [--link <link>|--all]", false)
	}
	if len(args) == 0 {
		return invalid()
	}
	action := args[0]
	args = args[1:]
	sub := ""
	if action == "speedtest" && len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub = args[0]
		args = args[1:]
	}
	// Accept flags before or after the positional priority/link arguments.
	var positional, options []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			options = append(options, a)
			if a == "--socket" || a == "--link" {
				i++
				if i >= len(args) {
					return invalid()
				}
				options = append(options, args[i])
			}
		} else {
			positional = append(positional, a)
		}
	}
	flags := flag.NewFlagSet("link", flag.ContinueOnError)
	flags.SetOutput(env.Stderr)
	socket := flags.String("socket", defaultClientSocket(), "client daemon socket")
	asJSON := flags.Bool("json", false, "JSON output (speedtest progress is NDJSON)")
	link := flags.String("link", "", "ipv6 or fn-connect")
	all := flags.Bool("all", false, "test both links serially")
	if err := flags.Parse(options); err != nil {
		return invalid()
	}
	if err := ensureClientDaemon(ctx, *socket); err != nil {
		return err
	}
	rpc := ipc.Client{SocketPath: *socket, Timeout: 5 * time.Second}
	output := func(v any) error { return json.NewEncoder(env.Stdout).Encode(v) }
	if action == "speedtest" {
		if len(positional) != 0 || (*all && *link != "") {
			return invalid()
		}
		if sub != "" {
			return invalid()
		}
		target := *link
		if *all {
			target = "all"
		}
		if target == "" {
			return invalid()
		}
		if !*asJSON {
			fmt.Fprintln(env.Stderr, "Testing the real tunnel; Ctrl-C cancels and restores normal selection.")
		}
		rpc.Timeout = 5 * time.Minute
		var status client.SpeedTestStatus
		text := speedTestText{out: env.Stdout, printed: make(map[string]bool)}
		err := rpc.Stream(ctx, client.MethodSpeedTest, client.SpeedTestRequest{Link: target}, func(payload json.RawMessage) (bool, error) {
			if err := json.Unmarshal(payload, &status); err != nil {
				return false, err
			}
			if *asJSON {
				if err := output(status); err != nil {
					return false, err
				}
			} else {
				if err := text.update(status); err != nil {
					return false, err
				}
			}
			return !status.Active, nil
		})
		if !*asJSON {
			if clearErr := text.clear(); err == nil {
				err = clearErr
			}
		}
		if err != nil {
			return err
		}

		if !*asJSON {
			if err := text.summary(status); err != nil {
				return err
			}
		}
		if status.Error != "" || status.RestoreError != "" || status.Stage == "canceled" {
			return model.NewError(model.ErrorUnavailable, "speed test did not complete successfully; inspect results", false)
		}
		for _, result := range status.Results {
			if result.Error != "" {
				return model.NewError(model.ErrorUnavailable, result.Error, false)
			}
		}
		return nil
	}
	if *all || *link != "" {
		return invalid()
	}
	var settings client.LinkSettings
	if err := rpc.Call(ctx, client.MethodLinkSettings, nil, &settings); err != nil {
		return err
	}
	switch action {
	case "list":
		if len(positional) != 0 {
			return invalid()
		}
	case "reset":
		if len(positional) != 0 {
			return invalid()
		}
		settings.Links = []client.LinkPreference{{Link: "ipv6", Enabled: true}, {Link: "fn-connect", Enabled: true}}
	case "priority":
		if len(positional) != 2 || positional[0] == positional[1] {
			return invalid()
		}
		reordered := make([]client.LinkPreference, 0, 2)
		for _, name := range positional {
			for _, entry := range settings.Links {
				if entry.Link == name {
					reordered = append(reordered, entry)
				}
			}
		}
		if len(reordered) != 2 {
			return invalid()
		}
		settings.Links = reordered
	case "enable", "disable":
		if len(positional) != 1 {
			return invalid()
		}
		found := false
		for i := range settings.Links {
			if settings.Links[i].Link == positional[0] {
				settings.Links[i].Enabled = action == "enable"
				found = true
			}
		}
		if !found {
			return invalid()
		}
	default:
		return invalid()
	}
	if action != "list" {
		if err := rpc.Call(ctx, client.MethodSetLinkSettings, settings, &settings); err != nil {
			return err
		}
	}
	if *asJSON {
		return output(settings)
	}
	fmt.Fprintf(env.Stdout, "NAS: %s\n1. local (always enabled, fixed)\n", settings.FNID)
	for i, l := range settings.Links {
		enabled := "enabled"
		if !l.Enabled {
			enabled = "disabled"
		}
		fmt.Fprintf(env.Stdout, "%d. %s (%s)\n", i+2, l.Link, enabled)
	}
	return nil
}

// Carriage return moves the cursor but does not erase a longer previous frame.
// Space padding also works on terminals without ANSI escape support.
func writeSpeedTestProgress(out io.Writer, previousWidth *int, status client.SpeedTestStatus) error {
	line := fmt.Sprintf("%-12s %s", status.Link, status.Stage)
	switch status.Stage {
	case "latency":
		line += fmt.Sprintf("  %.2f ms", status.RTTMS)
	case "download", "upload":
		line += fmt.Sprintf("  %.2f Mbps", status.Mbps)
	}
	padding := max(0, *previousWidth-len(line))
	_, err := fmt.Fprintf(out, "\r%s%s", line, strings.Repeat(" ", padding))
	if err == nil {
		*previousWidth = len(line)
	}
	return err
}

type speedTestText struct {
	out     io.Writer
	width   int
	printed map[string]bool
}

func (p *speedTestText) clear() error {
	if p.width == 0 {
		return nil
	}
	_, err := fmt.Fprintf(p.out, "\r%s\r", strings.Repeat(" ", p.width))
	if err == nil {
		p.width = 0
	}
	return err
}
func speedTestMetric(value *float64, unit string, limited bool) string {
	if value == nil {
		return "—"
	}
	text := fmt.Sprintf("%.2f %s", *value, unit)
	if limited {
		text += " (limited)"
	}
	return text
}
func (p *speedTestText) update(status client.SpeedTestStatus) error {
	for _, result := range status.Results {
		switch result.Outcome {
		case "success", "unavailable", "failed", "canceled":
		default:
			continue
		}
		if p.printed[result.Link] {
			continue
		}
		if err := p.clear(); err != nil {
			return err
		}
		detail := result.Error
		if detail == "" {
			detail = result.Message
		}
		if detail != "" {
			detail = " | " + strings.Join(strings.Fields(detail), " ")
		}
		_, err := fmt.Fprintf(p.out, "%s [%s] | latency %s | download %s | upload %s%s\n", result.Link, result.Outcome,
			speedTestMetric(result.LatencyMS, "ms", false), speedTestMetric(result.DownloadMbps, "Mbps", result.DownloadLimited), speedTestMetric(result.UploadMbps, "Mbps", result.UploadLimited), detail)
		if err != nil {
			return err
		}
		p.printed[result.Link] = true
	}
	if status.Active {
		return writeSpeedTestProgress(p.out, &p.width, status)
	}
	return nil
}
func (p *speedTestText) summary(status client.SpeedTestStatus) error {
	counts := make(map[string]int)
	for _, result := range status.Results {
		counts[result.Outcome]++
	}
	_, err := fmt.Fprintf(p.out, "Test %s: %d succeeded, %d unavailable, %d failed, %d canceled.\n", status.Stage, counts["success"], counts["unavailable"], counts["failed"], counts["canceled"])
	if err != nil {
		return err
	}
	if status.Error != "" {
		if _, err = fmt.Fprintln(p.out, "Error:", strings.Join(strings.Fields(status.Error), " ")); err != nil {
			return err
		}
	}
	if status.RestoreError != "" {
		_, err = fmt.Fprintln(p.out, "Restore error:", strings.Join(strings.Fields(status.RestoreError), " "))
	}
	return err
}

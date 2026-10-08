// Package version owns the product version and the remote compatibility rule.
package version

import (
	"embed"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

//go:embed VERSION
var source embed.FS

// Revision identifies the source commit of a packaged build.
var Revision = "unknown"

// Current is embedded from the same source read by the packaging scripts.
var Current = func() string { b, _ := source.ReadFile("VERSION"); return strings.TrimSpace(string(b)) }()

var pattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

type Number struct{ Major, Minor, Patch uint64 }

func Parse(value string) (Number, error) {
	parts := pattern.FindStringSubmatch(value)
	if parts == nil {
		return Number{}, fmt.Errorf("invalid release version %q", value)
	}
	var n Number
	for i, target := range []*uint64{&n.Major, &n.Minor, &n.Patch} {
		v, err := strconv.ParseUint(parts[i+1], 10, 64)
		if err != nil {
			return Number{}, err
		}
		*target = v
	}
	return n, nil
}

// Check deliberately ignores PATCH. Development versions follow the same rule
// for validation purposes, without gaining the formal 1.0 compatibility promise.
func Check(client, server string) error {
	c, ce := Parse(client)
	s, se := Parse(server)
	if ce != nil || se != nil {
		e := model.NewError(model.ErrorProtocol, "invalid or missing product version", false)
		e.Operation = "version.check"
		e.Detail = fmt.Sprintf("client=%s server=%s", client, server)
		return e
	}
	target := ""
	if c.Major != s.Major {
		if c.Major < s.Major {
			target = "client"
		} else {
			target = "server"
		}
	} else if c.Minor > s.Minor {
		target = "server"
	}
	if target == "" {
		return nil
	}
	e := model.NewError(model.ErrorVersionIncompatible, "client and server versions are incompatible", false)
	e.ClientVersion, e.ServerVersion, e.UpgradeTarget = client, server, target
	return e
}

// IsFailure identifies terminal admission failures, including invalid versions.
func IsFailure(err error) bool {
	if err == nil {
		return false
	}
	e := model.AsError(err)
	if e == nil {
		return false
	}
	return e.Code == model.ErrorVersionIncompatible || e.Operation == "version.check"
}

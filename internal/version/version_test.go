package version

import (
	"strings"
	"testing"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

func TestCompatibility(t *testing.T) {
	for _, tc := range []struct{ client, server, target string }{
		{"1.2.3", "1.2.0", ""}, {"1.0.0-rc.1", "1.0.0-rc.2", ""},
		{"1.0.0", "1.0.0-rc.1", ""}, {"1.1.0-rc.2", "1.0.99", "server"},
		{"1.0.0-rc.1", "2.0.0-rc.1", "client"}, {"1.2.0", "1.9.0", ""}, {"1.9.0", "1.2.99", "server"},
		{"1.9.0", "2.0.0", "client"}, {"2.0.0", "1.9.99", "server"}, {"1.0.0", "1.0.999", ""},
	} {
		t.Run(tc.client+"_"+tc.server, func(t *testing.T) {
			err := Check(tc.client, tc.server)
			if tc.target == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			e := model.AsError(err)
			if !IsFailure(err) || e.Code != model.ErrorVersionIncompatible || e.Retryable || e.UpgradeTarget != tc.target || e.ClientVersion != tc.client || e.ServerVersion != tc.server {
				t.Fatalf("unexpected failure: %+v", e)
			}
		})
	}
}
func TestInvalidVersionsNeverPass(t *testing.T) {
	for _, v := range []string{"", "dev", "v1.0.0", "1.0", "01.0.0", "1.0.0+build.1", "1.0.0-rc.01", "1.0.0-rc", "1.0.0-rc.-1", "1.0.18446744073709551616"} {
		for _, err := range []error{Check(v, "1.0.0"), Check("1.0.0", v)} {
			if !IsFailure(err) || model.AsError(err).Retryable {
				t.Fatalf("version %q passed", v)
			}
		}
	}
	if _, err := Parse(Current); err != nil {
		t.Fatal(err)
	}
}

func TestCLIErrorIdentifiesBothVersionsAndUpgradeTarget(t *testing.T) {
	text := model.FormatError(Check("1.5.0", "1.4.9"))
	for _, part := range []string{"1.5.0", "1.4.9", "upgrade server"} {
		if !strings.Contains(text, part) {
			t.Fatalf("missing %s: %s", part, text)
		}
	}
}

func TestIsFailureWithNilModelError(t *testing.T) {
	var failure *model.Error
	if IsFailure(failure) {
		t.Fatal("nil model error reported a version failure")
	}
}

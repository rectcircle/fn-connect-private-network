//go:build windows

package windowsenv

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestResolveLocalAppData(t *testing.T) {
	tests := []struct {
		name        string
		environment string
		knownPath   string
		knownErr    error
		home        string
		homeErr     error
		want        string
		wantErr     bool
	}{
		{
			name:        "environment override",
			environment: `C:\isolated\LocalAppData`,
			knownErr:    errors.New("must not be called"),
			homeErr:     errors.New("must not be called"),
			want:        `C:\isolated\LocalAppData`,
		},
		{
			name:      "known folder fallback",
			knownPath: `C:\Users\test\AppData\Local`,
			homeErr:   errors.New("must not be called"),
			want:      `C:\Users\test\AppData\Local`,
		},
		{
			name:     "profile fallback",
			knownErr: errors.New("known folder unavailable"),
			home:     `C:\Users\test`,
			want:     `C:\Users\test\AppData\Local`,
		},
		{
			name:        "reject relative environment instead of using working directory",
			environment: `relative\profile`,
			wantErr:     true,
		},
		{
			name:     "fail closed when no profile path exists",
			knownErr: errors.New("known folder unavailable"),
			homeErr:  errors.New("profile unavailable"),
			wantErr:  true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := resolveLocalAppData(
				test.environment,
				func() (string, error) { return test.knownPath, test.knownErr },
				func() (string, error) { return test.home, test.homeErr },
			)
			if (err != nil) != test.wantErr {
				t.Fatalf("resolveLocalAppData() error = %v, wantErr %v", err, test.wantErr)
			}
			if err == nil && filepath.Clean(got) != filepath.Clean(test.want) {
				t.Fatalf("resolveLocalAppData() = %q, want %q", got, test.want)
			}
		})
	}
}

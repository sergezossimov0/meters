package app

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"strings"
	"testing"

	"github.com/sergezossimov0/meters/internal/config"
)

func TestExitCode(t *testing.T) {
	usage := "Usage of server:\n  -a string\n"
	tests := []struct {
		name       string
		err        error
		want       int
		wantStdout string
		wantStderr []string
	}{
		{name: "success", err: nil, want: ExitOK},
		{
			name:       "help",
			err:        &config.UsageError{Err: flag.ErrHelp, Usage: usage},
			want:       ExitOK,
			wantStdout: usage,
		},
		{
			name:       "usage error",
			err:        &config.UsageError{Err: errors.New("bad flag"), Usage: usage},
			want:       ExitUsage,
			wantStderr: []string{"server: bad flag", usage},
		},
		{
			name:       "wrapped usage error",
			err:        fmt.Errorf("load: %w", &config.UsageError{Err: errors.New("bad flag"), Usage: usage}),
			want:       ExitUsage,
			wantStderr: []string{"bad flag", usage},
		},
		{
			name:       "runtime error",
			err:        errors.New("open database: refused"),
			want:       ExitError,
			wantStderr: []string{"server: open database: refused\n"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if got := ExitCode("server", tt.err, &stdout, &stderr); got != tt.want {
				t.Errorf("code = %d, want %d", got, tt.want)
			}
			if stdout.String() != tt.wantStdout {
				t.Errorf("stdout = %q, want %q", stdout.String(), tt.wantStdout)
			}
			for _, s := range tt.wantStderr {
				if !strings.Contains(stderr.String(), s) {
					t.Errorf("stderr %q does not contain %q", stderr.String(), s)
				}
			}
			if len(tt.wantStderr) == 0 && stderr.Len() > 0 {
				t.Errorf("unexpected stderr: %q", stderr.String())
			}
		})
	}
}

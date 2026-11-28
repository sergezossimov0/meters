package app

import (
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/sergezossimov0/meters/internal/config"
)

// Exit codes.
const (
	ExitOK    = 0
	ExitError = 1
	ExitUsage = 2
)

// ExitCode reports err the way a command-line tool should and returns the
// process exit code: help goes to stdout with 0, a usage error to stderr with
// the flag help and 2, anything else to stderr with 1.
func ExitCode(program string, err error, stdout, stderr io.Writer) int {
	if err == nil {
		return ExitOK
	}
	if usage, ok := errors.AsType[*config.UsageError](err); ok {
		if errors.Is(err, flag.ErrHelp) {
			_, _ = io.WriteString(stdout, usage.Usage)
			return ExitOK
		}
		_, _ = fmt.Fprintf(stderr, "%s: %v\n\n%s", program, err, usage.Usage)
		return ExitUsage
	}
	_, _ = fmt.Fprintf(stderr, "%s: %v\n", program, err)
	return ExitError
}

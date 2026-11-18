// Package config loads the server and gateway settings.
//
// Priority: environment variable → command-line flag → default.
// An environment variable set to an empty string counts as unset, so a
// compose file can pass KEY= without overriding a flag.
package config

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"
)

// LookupEnv has the signature of os.LookupEnv; tests pass a map-backed one.
type LookupEnv func(key string) (string, bool)

// UsageError is a command-line error. Usage holds the flag help text to show
// the user. errors.Is(err, flag.ErrHelp) is true for -h/--help.
type UsageError struct {
	Err   error
	Usage string
}

func (e *UsageError) Error() string { return e.Err.Error() }
func (e *UsageError) Unwrap() error { return e.Err }

// setting binds one config field to a flag and/or an environment variable.
// An empty flag means env-only; an empty env means flag-only.
type setting struct {
	flag  string
	env   string
	value flag.Value
	usage string
}

// load applies flags, then environment variables, on top of the defaults
// already stored in the fields behind the settings.
func load(program string, args []string, lookupEnv LookupEnv, settings []setting) error {
	fs := flag.NewFlagSet(program, flag.ContinueOnError)
	fs.SetOutput(io.Discard) // errors are returned, not printed
	for _, s := range settings {
		if s.flag == "" {
			continue
		}
		usage := s.usage
		if s.env != "" {
			usage += " (env " + s.env + ")"
		}
		fs.Var(s.value, s.flag, usage)
	}

	usage := func() string {
		var b bytes.Buffer
		fmt.Fprintf(&b, "Usage of %s:\n", program)
		fs.SetOutput(&b)
		fs.PrintDefaults()
		fs.SetOutput(io.Discard)
		b.WriteString(envOnlyHelp(settings))
		return b.String()
	}

	if err := fs.Parse(normalizeHelp(args)); err != nil {
		return &UsageError{Err: err, Usage: usage()}
	}
	if fs.NArg() > 0 {
		return &UsageError{
			Err:   fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " ")),
			Usage: usage(),
		}
	}

	var errs []error
	for _, s := range settings {
		if s.env == "" {
			continue
		}
		v, ok := lookupEnv(s.env)
		if !ok || v == "" {
			continue
		}
		if err := s.value.Set(v); err != nil {
			errs = append(errs, fmt.Errorf("env %s: %w", s.env, err))
		}
	}
	return errors.Join(errs...)
}

// normalizeHelp lets --help work: the flag package knows only -h/-help.
func normalizeHelp(args []string) []string {
	if !slices.Contains(args, "--help") {
		return args
	}
	out := slices.Clone(args)
	for i, a := range out {
		if a == "--help" {
			out[i] = "-h"
		}
	}
	return out
}

func envOnlyHelp(settings []setting) string {
	var b strings.Builder
	for _, s := range settings {
		if s.flag != "" || s.env == "" {
			continue
		}
		if b.Len() == 0 {
			b.WriteString("Environment-only settings:\n")
		}
		fmt.Fprintf(&b, "  %s\n    \t%s\n", s.env, s.usage)
	}
	return b.String()
}

var logLevels = []string{"debug", "info", "warn", "error"}

func validateLogLevel(level string) error {
	if !slices.Contains(logLevels, level) {
		return fmt.Errorf("LOG_LEVEL: must be one of %s, got %q", strings.Join(logLevels, ", "), level)
	}
	return nil
}

package main

import (
	"context"
	"errors"
	"flag"
	"strings"
	"testing"
	"time"

	"github.com/sergezossimov0/meters/internal/config"
)

func env(m map[string]string) config.LookupEnv {
	return func(key string) (string, bool) {
		v, ok := m[key]
		return v, ok
	}
}

func TestRun_UnreachableDatabase(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	err := run(ctx, nil, env(map[string]string{
		"DATABASE_DSN": "postgres://127.0.0.1:1/meters?sslmode=disable&connect_timeout=1",
		"JWT_SECRET":   strings.Repeat("x", 32), // dummy test value
		"LOG_LEVEL":    "error",
	}))
	if err == nil || !strings.Contains(err.Error(), "open database") {
		t.Fatalf("run error = %v, want open database failure", err)
	}
}

func TestRun_ConfigErrors(t *testing.T) {
	err := run(t.Context(), []string{"-h"}, env(nil))
	if !errors.Is(err, flag.ErrHelp) {
		t.Errorf("-h: error = %v, want flag.ErrHelp", err)
	}
	err = run(t.Context(), nil, env(nil))
	if err == nil || !strings.Contains(err.Error(), "DATABASE_DSN") {
		t.Errorf("no config: error = %v, want DATABASE_DSN required", err)
	}
}

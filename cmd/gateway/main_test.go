package main

import (
	"context"
	"errors"
	"flag"
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		v, ok := m[key]
		return v, ok
	}
}

func TestRun_StopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, nil, env(map[string]string{"GATEWAY_KEY": "test-gateway-key", "LOG_LEVEL": "error"}))
	}()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not stop after cancel")
	}
}

func TestRun_ConfigErrors(t *testing.T) {
	if err := run(t.Context(), []string{"-h"}, env(nil)); !errors.Is(err, flag.ErrHelp) {
		t.Errorf("-h: error = %v, want flag.ErrHelp", err)
	}
	if err := run(t.Context(), nil, env(nil)); err == nil || !strings.Contains(err.Error(), "GATEWAY_KEY") {
		t.Errorf("no config: error = %v, want GATEWAY_KEY required", err)
	}
}

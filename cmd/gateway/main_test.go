package main

import (
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
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
	file := filepath.Join(t.TempDir(), "buffer.json")
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, nil, env(map[string]string{
			"GATEWAY_KEY":       "test-gateway-key",
			"ADDRESS":           "127.0.0.1:1", // nothing listens: sends fail, readings stay
			"FILE_STORAGE_PATH": file,
			"LOG_LEVEL":         "error",
		}))
	}()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("run did not stop after cancel")
	}
	if _, err := os.Stat(file); err != nil {
		t.Errorf("the buffer file must be saved on shutdown: %v", err)
	}
}

func TestRun_DamagedBufferFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "buffer.json")
	if err := os.WriteFile(file, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := run(t.Context(), nil, env(map[string]string{
		"GATEWAY_KEY": "k", "FILE_STORAGE_PATH": file, "LOG_LEVEL": "error",
	}))
	if err == nil || !strings.Contains(err.Error(), "load buffer") {
		t.Errorf("error = %v, want a refusal to start on a damaged buffer", err)
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

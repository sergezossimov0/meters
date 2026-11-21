package main

import (
	"context"
	"errors"
	"flag"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"golang.org/x/crypto/bcrypt"

	"github.com/sergezossimov0/meters/internal/auth"
	"github.com/sergezossimov0/meters/internal/config"
	"github.com/sergezossimov0/meters/internal/service"
	"github.com/sergezossimov0/meters/internal/storage/memory"
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

func TestEnsureAdmin(t *testing.T) {
	passwords, err := auth.NewPasswords(bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	tokens, err := auth.NewTokens([]byte(strings.Repeat("x", auth.MinSecretBytes)), time.Hour) // dummy test key
	if err != nil {
		t.Fatal(err)
	}
	users := service.NewUsers(memory.NewUsers(), passwords, tokens)
	core, logs := observer.New(zap.InfoLevel)
	log := zap.New(core)

	if err := ensureAdmin(t.Context(), log, users, config.Server{}); err != nil || logs.Len() != 0 {
		t.Fatalf("no ADMIN_LOGIN: err = %v, %d log entries; want nothing done", err, logs.Len())
	}

	cfg := config.Server{AdminLogin: "admin", AdminPassword: config.Secret("pass-word-1")} // dummy test value
	for _, want := range []string{"first admin created", "first admin already exists, password unchanged"} {
		if err := ensureAdmin(t.Context(), log, users, cfg); err != nil {
			t.Fatalf("ensureAdmin: %v", err)
		}
		if got := logs.All()[logs.Len()-1].Message; got != want {
			t.Errorf("log = %q, want %q", got, want)
		}
	}

	cfg.AdminLogin = "x" // invalid login: the server must not start
	if err := ensureAdmin(t.Context(), log, users, cfg); err == nil {
		t.Error("invalid ADMIN_LOGIN accepted")
	}
}

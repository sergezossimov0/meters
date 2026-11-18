package config

import (
	"errors"
	"flag"
	"strings"
	"testing"
	"time"
)

// env builds a LookupEnv backed by a map.
func env(m map[string]string) LookupEnv {
	return func(k string) (string, bool) {
		v, ok := m[k]
		return v, ok
	}
}

var testJWTKey = strings.Repeat("j", jwtKeyMinBytes)

// minimalServerEnv has only the required settings.
func minimalServerEnv() map[string]string {
	return map[string]string{
		"DATABASE_DSN": "postgres://db:5432/meters",
		"JWT_SECRET":   testJWTKey,
	}
}

func TestLoadServer_Defaults(t *testing.T) {
	c, err := LoadServer(nil, env(minimalServerEnv()))
	if err != nil {
		t.Fatalf("LoadServer: %v", err)
	}
	if c.Address != "localhost:8080" {
		t.Errorf("Address = %q", c.Address)
	}
	if c.JWTTTL != 24*time.Hour {
		t.Errorf("JWTTTL = %s", c.JWTTTL)
	}
	if c.Timezone.String() != "Asia/Almaty" {
		t.Errorf("Timezone = %s", c.Timezone)
	}
	if c.AnomalyInterval != 600*time.Second || c.BatchRetentionDays != 7 || c.MaxBodyBytes != 10<<20 {
		t.Errorf("worker/limits defaults: %s %d %d", c.AnomalyInterval, c.BatchRetentionDays, c.MaxBodyBytes)
	}
	if c.LogLevel != "info" || c.Key.IsSet() || c.AdminLogin != "" {
		t.Errorf("unexpected defaults: %s", c)
	}
}

func TestLoadServer_Priority(t *testing.T) {
	tests := []struct {
		name string
		args []string
		env  map[string]string
		want string
	}{
		{"default", nil, nil, "localhost:8080"},
		{"flag", []string{"-a", ":9090"}, nil, ":9090"},
		{"env over flag", []string{"-a", ":9090"}, map[string]string{"ADDRESS": ":7070"}, ":7070"},
		{"empty env is unset", []string{"-a", ":9090"}, map[string]string{"ADDRESS": ""}, ":9090"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := minimalServerEnv()
			for k, v := range tt.env {
				e[k] = v
			}
			c, err := LoadServer(tt.args, env(e))
			if err != nil {
				t.Fatalf("LoadServer: %v", err)
			}
			if c.Address != tt.want {
				t.Errorf("Address = %q, want %q", c.Address, tt.want)
			}
		})
	}
}

func TestLoadServer_AllSettings(t *testing.T) {
	args := []string{"-a", ":1", "-d", "postgres://flag/db", "-k", "flag-sign", "-j", testJWTKey}
	c, err := LoadServer(args, env(map[string]string{
		"JWT_TTL":              "90m",
		"ADMIN_LOGIN":          "admin",
		"ADMIN_PASSWORD":       "long-enough",
		"TIMEZONE":             "UTC",
		"ANOMALY_INTERVAL":     "5",
		"BATCH_RETENTION_DAYS": "3",
		"MAX_BODY_BYTES":       "2048",
		"LOG_LEVEL":            "debug",
	}))
	if err != nil {
		t.Fatalf("LoadServer: %v", err)
	}
	if c.Address != ":1" || c.DatabaseDSN.Value() != "postgres://flag/db" || c.Key.Value() != "flag-sign" {
		t.Errorf("flags not applied: %s", c)
	}
	if c.JWTTTL != 90*time.Minute || c.AdminLogin != "admin" || c.AdminPassword.Value() != "long-enough" {
		t.Errorf("env not applied: %s", c)
	}
	if c.Timezone != time.UTC || c.AnomalyInterval != 5*time.Second || c.BatchRetentionDays != 3 ||
		c.MaxBodyBytes != 2048 || c.LogLevel != "debug" {
		t.Errorf("env not applied: %s", c)
	}
}

func TestLoadServer_Errors(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		env     map[string]string // merged over minimalServerEnv; "" deletes the key
		wantErr string
	}{
		{"unknown flag", []string{"-x"}, nil, "flag provided but not defined"},
		{"positional args", []string{"extra"}, nil, "unexpected arguments: extra"},
		{"missing dsn", nil, map[string]string{"DATABASE_DSN": ""}, "DATABASE_DSN (-d): required"},
		{"missing jwt", nil, map[string]string{"JWT_SECRET": ""}, "JWT_SECRET (-j): required"},
		{"short jwt", nil, map[string]string{"JWT_SECRET": "short"}, "at least 32 bytes"},
		{"bad ttl", nil, map[string]string{"JWT_TTL": "day"}, "env JWT_TTL: must be a duration"},
		{"zero ttl", nil, map[string]string{"JWT_TTL": "0s"}, "JWT_TTL: must be positive"},
		{"login only", nil, map[string]string{"ADMIN_LOGIN": "admin"}, "password missing"},
		{"admin password only", nil, map[string]string{"ADMIN_PASSWORD": "long-enough"}, "login missing"},
		{"short admin password", nil, map[string]string{"ADMIN_LOGIN": "admin", "ADMIN_PASSWORD": "short"}, "at least 8 characters"},
		{"bad timezone", nil, map[string]string{"TIMEZONE": "Mars/Base"}, `unknown time zone "Mars/Base"`},
		{"negative interval", nil, map[string]string{"ANOMALY_INTERVAL": "-1"}, "must not be negative"},
		{"zero interval", nil, map[string]string{"ANOMALY_INTERVAL": "0"}, "ANOMALY_INTERVAL: must be positive"},
		{"interval not int", nil, map[string]string{"ANOMALY_INTERVAL": "1.5"}, "whole number of seconds"},
		{"retention", nil, map[string]string{"BATCH_RETENTION_DAYS": "0"}, "BATCH_RETENTION_DAYS: must be at least 1"},
		{"body limit", nil, map[string]string{"MAX_BODY_BYTES": "100"}, "MAX_BODY_BYTES: must be at least 1024"},
		{"body limit not int", nil, map[string]string{"MAX_BODY_BYTES": "10MB"}, "must be an integer"},
		{"log level", nil, map[string]string{"LOG_LEVEL": "trace"}, "LOG_LEVEL: must be one of"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := minimalServerEnv()
			for k, v := range tt.env {
				if v == "" {
					delete(e, k)
					continue
				}
				e[k] = v
			}
			_, err := LoadServer(tt.args, env(e))
			if err == nil {
				t.Fatal("want error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error %q does not contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestLoadServer_ReportsAllErrors(t *testing.T) {
	_, err := LoadServer(nil, env(map[string]string{"LOG_LEVEL": "trace"}))
	if err == nil {
		t.Fatal("want error")
	}
	for _, want := range []string{"DATABASE_DSN", "JWT_SECRET", "LOG_LEVEL"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %s", err, want)
		}
	}
}

func TestLoadServer_Help(t *testing.T) {
	for _, arg := range []string{"-h", "--help"} {
		t.Run(arg, func(t *testing.T) {
			_, err := LoadServer([]string{arg}, env(nil))
			if !errors.Is(err, flag.ErrHelp) {
				t.Fatalf("err = %v, want flag.ErrHelp", err)
			}
			var ue *UsageError
			if !errors.As(err, &ue) {
				t.Fatalf("err is not *UsageError: %T", err)
			}
			for _, want := range []string{"-a", "(env ADDRESS)", "Environment-only settings", "TIMEZONE"} {
				if !strings.Contains(ue.Usage, want) {
					t.Errorf("usage does not contain %q:\n%s", want, ue.Usage)
				}
			}
		})
	}
}

func TestServer_StringHidesSecrets(t *testing.T) {
	c, err := LoadServer([]string{"-k", "sign-value-123"}, env(map[string]string{
		"DATABASE_DSN":   "postgres://db:5432/meters",
		"JWT_SECRET":     testJWTKey,
		"ADMIN_LOGIN":    "admin",
		"ADMIN_PASSWORD": "admin-value-123",
	}))
	if err != nil {
		t.Fatalf("LoadServer: %v", err)
	}
	s := c.String()
	for _, leaked := range []string{"sign-value-123", testJWTKey, "admin-value-123"} {
		if strings.Contains(s, leaked) {
			t.Errorf("String() leaks %q: %s", leaked, s)
		}
	}
	if !strings.Contains(s, "key=set") || !strings.Contains(s, "dsn=postgres://db:5432/meters") {
		t.Errorf("String() = %s", s)
	}
}

package config

import (
	"strings"
	"testing"
	"time"
)

func minimalGatewayEnv() map[string]string {
	return map[string]string{"GATEWAY_KEY": "gw-test-key"}
}

func TestLoadGateway_Defaults(t *testing.T) {
	c, err := LoadGateway(nil, env(minimalGatewayEnv()))
	if err != nil {
		t.Fatalf("LoadGateway: %v", err)
	}
	want := Gateway{
		Address:         "localhost:8080",
		ServerURL:       "http://localhost:8080",
		GatewayKey:      "gw-test-key",
		PollInterval:    2 * time.Second,
		ReportInterval:  10 * time.Second,
		RateLimit:       2,
		StoreInterval:   300 * time.Second,
		FileStoragePath: "gateway-buffer.json",
		Restore:         true,
		Devices:         10,
		Speed:           3600,
		Backfill:        720 * time.Hour,
		BatchSize:       1000,
		LogLevel:        "info",
	}
	if c != want {
		t.Errorf("got  %s\nwant %s", c, want)
	}
}

func TestLoadGateway_FlagsAndEnv(t *testing.T) {
	args := []string{
		"-a", "server:9000", "-k", "sign", "-p", "1", "-r", "5", "-l", "4", "-i", "0",
		"-f", "/tmp/buf.json", "-restore=false", "-n", "3", "-speed", "60", "-backfill", "48h", "-b", "500",
	}
	c, err := LoadGateway(args, env(map[string]string{
		"GATEWAY_KEY": "gw-test-key",
		"RATE_LIMIT":  "8", // env over flag
		"RESTORE":     "true",
		"LOG_LEVEL":   "warn",
	}))
	if err != nil {
		t.Fatalf("LoadGateway: %v", err)
	}
	if c.ServerURL != "http://server:9000" || c.Key.Value() != "sign" {
		t.Errorf("address/key: %s", c)
	}
	if c.PollInterval != time.Second || c.ReportInterval != 5*time.Second || c.StoreInterval != 0 {
		t.Errorf("intervals: %s", c)
	}
	if c.RateLimit != 8 || !c.Restore || c.LogLevel != "warn" {
		t.Errorf("env over flag: %s", c)
	}
	if c.FileStoragePath != "/tmp/buf.json" || c.Devices != 3 || c.Speed != 60 ||
		c.Backfill != 48*time.Hour || c.BatchSize != 500 {
		t.Errorf("flags: %s", c)
	}
}

func TestLoadGateway_BareRestoreFlag(t *testing.T) {
	c, err := LoadGateway([]string{"-restore"}, env(minimalGatewayEnv()))
	if err != nil {
		t.Fatalf("LoadGateway: %v", err)
	}
	if !c.Restore {
		t.Error("bare -restore must mean true")
	}
}

func TestLoadGateway_ServerURL(t *testing.T) {
	tests := []struct {
		address string
		want    string
		wantErr string
	}{
		{"localhost:8080", "http://localhost:8080", ""},
		{"http://server:8080/", "http://server:8080", ""},
		{"https://meters.example.com", "https://meters.example.com", ""},
		{"https://meters.example.com/base/", "https://meters.example.com/base", ""},
		{"ftp://server", "", "scheme must be http or https"},
		{"http://", "", "invalid address"},
		{"http://server?x=1", "", "invalid address"},
	}
	for _, tt := range tests {
		t.Run(tt.address, func(t *testing.T) {
			e := minimalGatewayEnv()
			e["ADDRESS"] = tt.address
			c, err := LoadGateway(nil, env(e))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadGateway: %v", err)
			}
			if c.ServerURL != tt.want {
				t.Errorf("ServerURL = %q, want %q", c.ServerURL, tt.want)
			}
		})
	}
}

func TestLoadGateway_Errors(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		env     map[string]string
		wantErr string
	}{
		{"unknown flag", []string{"-x"}, nil, "flag provided but not defined"},
		{"missing gateway key", nil, map[string]string{"GATEWAY_KEY": ""}, "GATEWAY_KEY (-g): required"},
		{"zero poll", []string{"-p", "0"}, nil, "POLL_INTERVAL (-p): must be positive"},
		{"zero report", []string{"-r", "0"}, nil, "REPORT_INTERVAL (-r): must be between 1 and 3600"},
		{"huge report", []string{"-r", "3601"}, nil, "REPORT_INTERVAL (-r): must be between 1 and 3600"},
		{"negative store", []string{"-i", "-1"}, nil, "must not be negative"},
		{"rate limit", []string{"-l", "0"}, nil, "RATE_LIMIT (-l): must be at least 1"},
		{"empty file", []string{"-f", ""}, nil, "FILE_STORAGE_PATH (-f): must not be empty"},
		{"devices", []string{"-n", "0"}, nil, "DEVICES (-n): must be between 1 and 10000"},
		{"speed", []string{"-speed", "0"}, nil, "SPEED (-speed): must be at least 1"},
		{"backfill too old", []string{"-backfill", "2161h"}, nil, "BACKFILL (-backfill): must be between 0"},
		{"backfill negative", []string{"-backfill", "-1h"}, nil, "BACKFILL (-backfill): must be between 0"},
		{"batch size", []string{"-b", "5001"}, nil, "BATCH_SIZE (-b): must be between 1 and 5000"},
		{"bad bool env", nil, map[string]string{"RESTORE": "yes"}, "env RESTORE: must be a boolean"},
		{"bad int flag", []string{"-n", "ten"}, nil, "must be an integer"},
		{"log level", nil, map[string]string{"LOG_LEVEL": "TRACE"}, "LOG_LEVEL: must be one of"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := minimalGatewayEnv()
			for k, v := range tt.env {
				if v == "" {
					delete(e, k)
					continue
				}
				e[k] = v
			}
			_, err := LoadGateway(tt.args, env(e))
			if err == nil {
				t.Fatal("want error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error %q does not contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestGateway_StringHidesSecrets(t *testing.T) {
	c, err := LoadGateway([]string{"-k", "sign-value-123"}, env(minimalGatewayEnv()))
	if err != nil {
		t.Fatalf("LoadGateway: %v", err)
	}
	s := c.String()
	for _, leaked := range []string{"sign-value-123", "gw-test-key"} {
		if strings.Contains(s, leaked) {
			t.Errorf("String() leaks %q: %s", leaked, s)
		}
	}
}

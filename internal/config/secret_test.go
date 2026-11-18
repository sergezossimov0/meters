package config

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestSecret_NeverPrints(t *testing.T) {
	s := Secret("sample-value")
	cfg := struct{ Key Secret }{s}

	jsonBytes, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	outputs := map[string]string{
		"%v":   fmt.Sprintf("%v", s),
		"%s":   fmt.Sprintf("key=%s", s),
		"%#v":  fmt.Sprintf("%#v", s),
		"%+v":  fmt.Sprintf("%+v", cfg),
		"json": string(jsonBytes),
	}
	for format, out := range outputs {
		if strings.Contains(out, "sample-value") {
			t.Errorf("%s leaks the value: %s", format, out)
		}
		if !strings.Contains(out, redacted) {
			t.Errorf("%s = %s, want %s", format, out, redacted)
		}
	}
	if s.Value() != "sample-value" || !s.IsSet() {
		t.Error("Value/IsSet broken")
	}
	if Secret("").String() != "" || Secret("").IsSet() {
		t.Error("empty secret must print empty and be unset")
	}
}

func TestRedactDSN(t *testing.T) {
	tests := []struct {
		name string
		dsn  string
		keep []string
	}{
		{"url without password", "postgres://db:5432/meters?sslmode=disable", []string{"db:5432", "sslmode=disable"}},
		{"url user info", "postgres://app:sample-value@db:5432/meters", []string{"app", "db:5432/meters"}},
		{"url query", "postgres://app@db:5432/meters?password=sample-value", []string{"db:5432/meters"}},
		{"key value", "host=db dbname=meters password=sample-value sslmode=disable", []string{"host=db", "sslmode=disable"}},
		{"key value quoted", "host=db password='sample value' dbname=meters", []string{"host=db", "dbname=meters"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RedactDSN(tt.dsn)
			if strings.Contains(got, "sample") {
				t.Errorf("RedactDSN leaks: %s", got)
			}
			for _, k := range tt.keep {
				if !strings.Contains(got, k) {
					t.Errorf("RedactDSN(%q) = %q, lost %q", tt.dsn, got, k)
				}
			}
		})
	}
}

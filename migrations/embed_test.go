package migrations

import (
	"fmt"
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

var fileName = regexp.MustCompile(`^\d{5}_[a-z0-9_]+\.sql$`)

// TestFS checks that every embedded migration is named NNNNN_name.sql, the
// versions are consecutive and each file has both goose sections.
func TestFS(t *testing.T) {
	entries, err := fs.ReadDir(FS, ".")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no migrations embedded")
	}
	for i, e := range entries {
		name := e.Name()
		if !fileName.MatchString(name) {
			t.Errorf("%s: name must match NNNNN_name.sql", name)
			continue
		}
		if prefix := fmt.Sprintf("%05d_", i+1); !strings.HasPrefix(name, prefix) {
			t.Errorf("%s: want prefix %s (versions must be consecutive)", name, prefix)
		}
		body, err := fs.ReadFile(FS, name)
		if err != nil {
			t.Fatal(err)
		}
		up := strings.Index(string(body), "-- +goose Up")
		down := strings.Index(string(body), "-- +goose Down")
		if up < 0 || down < up {
			t.Errorf("%s: needs '-- +goose Up' followed by '-- +goose Down'", name)
		}
	}
}

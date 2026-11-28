package postgres

import (
	"strings"
	"testing"
)

func TestOpen_InvalidDSNDoesNotLeakIt(t *testing.T) {
	dsn := "postgres://app@db:5432/meters?connect_timeout=notanumber&marker=dsn-marker"
	_, err := Open(t.Context(), dsn)
	if err == nil {
		t.Fatal("want error for invalid DSN")
	}
	if strings.Contains(err.Error(), "dsn-marker") {
		t.Errorf("error leaks the DSN: %v", err)
	}
}

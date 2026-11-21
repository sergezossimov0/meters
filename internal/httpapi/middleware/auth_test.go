package middleware

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/sergezossimov0/meters/internal/auth"
	"github.com/sergezossimov0/meters/internal/domain"
)

func newTokens(t *testing.T, ttl time.Duration) *auth.Tokens {
	t.Helper()
	tokens, err := auth.NewTokens([]byte(strings.Repeat("k", auth.MinSecretBytes)), ttl)
	if err != nil {
		t.Fatal(err)
	}
	return tokens
}

func issue(t *testing.T, tokens *auth.Tokens, id int64, role domain.Role) string {
	t.Helper()
	token, err := tokens.Issue(id, role)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

// whoami answers 200 with the caller's user ID and role from the context.
func whoami(w http.ResponseWriter, r *http.Request) {
	c, ok := ClaimsFrom(r.Context())
	if !ok {
		http.Error(w, "no claims", http.StatusTeapot)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"id": c.UserID, "role": c.Role})
}

func serve(h http.Handler, authorization string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestAuthenticate(t *testing.T) {
	tokens := newTokens(t, time.Hour)
	// Issued inside a synctest bubble, where the clock starts in 2000, so the
	// token expired long before the real now.
	var expired string
	synctest.Test(t, func(t *testing.T) { expired = issue(t, tokens, 7, domain.RoleCustomer) })
	other, err := auth.NewTokens([]byte(strings.Repeat("z", auth.MinSecretBytes)), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	foreign := issue(t, other, 7, domain.RoleCustomer)
	valid := issue(t, tokens, 7, domain.RoleCustomer)

	core, logs := observer.New(zapcore.DebugLevel)
	h := Authenticate(tokens, zap.New(core))(http.HandlerFunc(whoami))

	tests := []struct {
		name          string
		authorization string
		wantChallenge string
	}{
		{"no header", "", `Bearer`},
		{"basic scheme", "Basic aXZhbjpwYXNz", `Bearer`},
		{"bearer without token", "Bearer ", `Bearer`},
		{"no space", "Bearer" + valid, `Bearer`},
		{"garbage token", "Bearer not.a.jwt", `Bearer error="invalid_token"`},
		{"expired", "Bearer " + expired, `Bearer error="invalid_token"`},
		{"foreign signature", "Bearer " + foreign, `Bearer error="invalid_token"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := serve(h, tt.authorization)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rec.Code)
			}
			if got := rec.Header().Get("WWW-Authenticate"); got != tt.wantChallenge {
				t.Errorf("WWW-Authenticate = %q, want %q", got, tt.wantChallenge)
			}
			if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
				t.Errorf("Content-Type = %q", ct)
			}
		})
	}

	for _, authorization := range []string{"Bearer " + valid, "bearer " + valid, "BEARER  " + valid} {
		rec := serve(h, authorization)
		if rec.Code != http.StatusOK {
			t.Fatalf("%q: status = %d, want 200 (body %s)", authorization[:8], rec.Code, rec.Body)
		}
		if body := strings.TrimSpace(rec.Body.String()); body != `{"id":7,"role":"customer"}` {
			t.Errorf("claims in context = %s", body)
		}
	}

	for _, entry := range logs.All() {
		for _, token := range []string{valid, expired, foreign} {
			if strings.Contains(fmt.Sprint(entry.Message, entry.ContextMap()), token) {
				t.Errorf("log leaks a token: %v", entry.ContextMap())
			}
		}
	}
	if logs.FilterMessage("token rejected").Len() != 3 {
		t.Errorf("want 3 rejections logged, got %d", logs.FilterMessage("token rejected").Len())
	}
}

func TestRequireRole(t *testing.T) {
	tokens := newTokens(t, time.Hour)
	h := Authenticate(tokens, zap.NewNop())(RequireRole(domain.RoleAdmin)(http.HandlerFunc(whoami)))

	if rec := serve(h, "Bearer "+issue(t, tokens, 1, domain.RoleAdmin)); rec.Code != http.StatusOK {
		t.Errorf("admin: status = %d, want 200", rec.Code)
	}
	rec := serve(h, "Bearer "+issue(t, tokens, 2, domain.RoleCustomer))
	if rec.Code != http.StatusForbidden {
		t.Errorf("customer: status = %d, want 403", rec.Code)
	}
	if rec.Header().Get("WWW-Authenticate") != "" {
		t.Error("403 must not carry WWW-Authenticate")
	}
}

func TestRequireRole_WithoutAuthenticate(t *testing.T) {
	rec := serve(RequireRole(domain.RoleAdmin)(http.HandlerFunc(whoami)), "")
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401: missing Authenticate must deny", rec.Code)
	}
}

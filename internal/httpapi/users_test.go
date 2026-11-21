package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"golang.org/x/crypto/bcrypt"

	"github.com/sergezossimov0/meters/internal/auth"
	"github.com/sergezossimov0/meters/internal/service"
	"github.com/sergezossimov0/meters/internal/storage/memory"
)

// Dummy test values, not real credentials.
const (
	testLogin = "ivan"
	testPw    = "pass-word-1"
)

type apiEnv struct {
	h      http.Handler
	logs   *observer.ObservedLogs
	tokens *auth.Tokens
}

func newAPI(t *testing.T) apiEnv {
	t.Helper()
	passwords, err := auth.NewPasswords(bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	tokens, err := auth.NewTokens([]byte(strings.Repeat("k", auth.MinSecretBytes)), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	core, logs := observer.New(zapcore.DebugLevel)
	h := NewRouter(Deps{
		Log:          zap.New(core),
		DB:           pingerFunc(func(context.Context) error { return nil }),
		MaxBodyBytes: 1 << 10,
		Users:        service.NewUsers(memory.NewUsers(), passwords, tokens),
		Tokens:       tokens,
		MeterTypes:   service.NewMeterTypes(memory.NewMeterTypes()),
	})
	return apiEnv{h: h, logs: logs, tokens: tokens}
}

func (e apiEnv) post(path, body string, header ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func creds(login, password string) string {
	return fmt.Sprintf(`{"login":%q,"password":%q}`, login, password)
}

// token checks a 200 token response and returns the token.
func (e apiEnv) token(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s; want 200", rec.Code, rec.Body)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	var resp struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.Token == "" {
		t.Fatalf("body %s: want {\"token\": ...} (%v)", rec.Body, err)
	}
	if _, err := e.tokens.Parse(resp.Token); err != nil {
		t.Errorf("returned token is invalid: %v", err)
	}
	return resp.Token
}

func TestRegisterAndLogin(t *testing.T) {
	e := newAPI(t)

	regToken := e.token(t, e.post("/api/user/register", creds(testLogin, testPw)))
	loginToken := e.token(t, e.post("/api/user/login", creds("Ivan", testPw)))

	// Neither the password nor a token may reach the logs.
	for _, entry := range e.logs.All() {
		line := fmt.Sprint(entry.Message, entry.ContextMap())
		for _, secret := range []string{testPw, regToken, loginToken} {
			if strings.Contains(line, secret) {
				t.Errorf("log entry leaks a secret: %s", line)
			}
		}
	}
}

func TestRegister_Errors(t *testing.T) {
	e := newAPI(t)
	if rec := e.post("/api/user/register", creds(testLogin, testPw)); rec.Code != http.StatusOK {
		t.Fatalf("setup: status %d", rec.Code)
	}

	tests := []struct {
		name     string
		body     string
		header   []string
		wantCode int
		wantMsg  string
	}{
		{"login taken", creds("IVAN", "other-pass-1"), nil, http.StatusConflict, "login is already taken"},
		{"short login", creds("iv", testPw), nil, http.StatusBadRequest, "login must be 3 to 64 characters"},
		{"short password", creds("petr", "short"), nil, http.StatusBadRequest, "password must be 8 to 72 bytes"},
		{"empty body", "", nil, http.StatusBadRequest, "empty request body"},
		{"malformed JSON", `{"login":"petr","password":"secret-ish`, nil, http.StatusBadRequest, "malformed JSON: unexpected end of input"},
		{"syntax error", `{"login":"petr" "password":"x"}`, nil, http.StatusBadRequest, "malformed JSON at byte"},
		{"unknown field", `{"login":"petr","password":"pass-word-1","admin":true}`, nil, http.StatusBadRequest, `unknown field "admin"`},
		{"wrong type", `{"login":"petr","password":12345678}`, nil, http.StatusBadRequest, `field "password" must be string`},
		{"two objects", creds("petr", testPw) + creds("anna", testPw), nil, http.StatusBadRequest, "unexpected data after the JSON object"},
		{"not JSON", creds("petr", testPw), []string{"Content-Type", "text/plain"}, http.StatusUnsupportedMediaType, "Content-Type must be application/json"},
		{"too large", creds("petr", strings.Repeat("p", 2<<10)), nil, http.StatusRequestEntityTooLarge, "request body too large"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := e.post("/api/user/register", tt.body, tt.header...)
			if rec.Code != tt.wantCode {
				t.Errorf("status = %d, want %d (body %s)", rec.Code, tt.wantCode, rec.Body)
			}
			if got := errorBody(t, rec); !strings.HasPrefix(got, tt.wantMsg) {
				t.Errorf("error = %q, want prefix %q", got, tt.wantMsg)
			}
		})
	}
}

func TestLogin_Errors(t *testing.T) {
	e := newAPI(t)
	if rec := e.post("/api/user/register", creds(testLogin, testPw)); rec.Code != http.StatusOK {
		t.Fatalf("setup: status %d", rec.Code)
	}
	for name, body := range map[string]string{
		"wrong password": creds(testLogin, "wrong-pass-1"),
		"unknown login":  creds("petr", testPw),
		"invalid login":  creds("x", testPw),
	} {
		t.Run(name, func(t *testing.T) {
			rec := e.post("/api/user/login", body)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", rec.Code)
			}
			if got := errorBody(t, rec); got != "invalid login or password" {
				t.Errorf("error = %q", got)
			}
		})
	}
	if rec := e.post("/api/user/login", "{"); rec.Code != http.StatusBadRequest {
		t.Errorf("malformed JSON: status = %d, want 400", rec.Code)
	}
}

// brokenUsers fails like a storage outage would.
type brokenUsers struct{}

func (brokenUsers) Register(context.Context, string, string) (string, error) {
	return "", errors.New("db down")
}

func (brokenUsers) Login(context.Context, string, string) (string, error) {
	return "", errors.New("db down")
}

func TestUsers_InternalError(t *testing.T) {
	core, logs := observer.New(zapcore.ErrorLevel)
	h := NewRouter(Deps{Log: zap.New(core), MaxBodyBytes: 1 << 10, Users: brokenUsers{}})
	for _, path := range []string{"/api/user/register", "/api/user/login"} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(creds(testLogin, testPw)))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("%s: status = %d, want 500", path, rec.Code)
		}
		if got := errorBody(t, rec); got != "internal error" {
			t.Errorf("%s: error = %q; details must not reach the client", path, got)
		}
	}
	if logs.FilterMessage("request failed").Len() != 2 {
		t.Errorf("want both failures logged, got %d", logs.Len())
	}
}

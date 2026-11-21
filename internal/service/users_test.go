package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/sergezossimov0/meters/internal/auth"
	"github.com/sergezossimov0/meters/internal/domain"
	"github.com/sergezossimov0/meters/internal/storage"
	"github.com/sergezossimov0/meters/internal/storage/memory"
)

// Dummy test values, not real credentials.
const (
	pw      = "pass-word-1"
	otherPw = "pass-word-2"
)

type env struct {
	svc    *Users
	repo   storage.UserRepository
	tokens *auth.Tokens
}

func newEnv(t *testing.T, repo storage.UserRepository) env {
	t.Helper()
	if repo == nil {
		repo = memory.NewUsers()
	}
	passwords, err := auth.NewPasswords(bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	tokens, err := auth.NewTokens([]byte(strings.Repeat("k", auth.MinSecretBytes)), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return env{svc: NewUsers(repo, passwords, tokens), repo: repo, tokens: tokens}
}

// claims parses a token issued by the service.
func (e env) claims(t *testing.T, token string) auth.Claims {
	t.Helper()
	c, err := e.tokens.Parse(token)
	if err != nil {
		t.Fatalf("service issued an invalid token: %v", err)
	}
	return c
}

func TestRegister(t *testing.T) {
	e := newEnv(t, nil)
	ctx := t.Context()

	token, err := e.svc.Register(ctx, "Ivan", pw)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	u, err := e.repo.ByLogin(ctx, "ivan")
	if err != nil {
		t.Fatalf("user not stored under the normalized login: %v", err)
	}
	if u.Role != domain.RoleCustomer || u.PasswordHash == pw {
		t.Errorf("stored user = %+v; want a customer with a hashed password", u)
	}
	if c := e.claims(t, token); c.UserID != u.ID || c.Role != domain.RoleCustomer {
		t.Errorf("token claims = %+v, want user %d customer", c, u.ID)
	}

	if _, err := e.svc.Register(ctx, "IVAN", otherPw); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("same login in another case: err = %v, want ErrConflict", err)
	}
}

func TestRegister_Invalid(t *testing.T) {
	e := newEnv(t, nil)
	for name, tc := range map[string]struct{ login, password string }{
		"short login":    {"iv", pw},
		"bad characters": {"ivan petrov", pw},
		"short password": {"ivan", "short"},
		"long password":  {"ivan", strings.Repeat("p", 73)},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := e.svc.Register(t.Context(), tc.login, tc.password)
			if !errors.Is(err, domain.ErrInvalid) {
				t.Errorf("err = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestLogin(t *testing.T) {
	e := newEnv(t, nil)
	ctx := t.Context()
	if _, err := e.svc.Register(ctx, "ivan", pw); err != nil {
		t.Fatal(err)
	}

	token, err := e.svc.Login(ctx, "Ivan", pw)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if c := e.claims(t, token); c.Role != domain.RoleCustomer {
		t.Errorf("role = %s, want customer", c.Role)
	}

	for name, tc := range map[string]struct{ login, password string }{
		"wrong password": {"ivan", otherPw},
		"unknown login":  {"petr", pw},
		"invalid login":  {"x", pw},
		"empty password": {"ivan", ""},
	} {
		t.Run(name, func(t *testing.T) {
			// One error for every case: the client cannot tell them apart.
			if _, err := e.svc.Login(ctx, tc.login, tc.password); !errors.Is(err, ErrBadCredentials) {
				t.Errorf("err = %v, want ErrBadCredentials", err)
			}
		})
	}
}

// failingRepo fails every call: storage errors must not look like bad credentials.
type failingRepo struct{ storage.UserRepository }

var errStorage = errors.New("storage down")

func (failingRepo) ByLogin(context.Context, string) (domain.User, error) {
	return domain.User{}, errStorage
}

func (failingRepo) Create(context.Context, domain.User) (domain.User, error) {
	return domain.User{}, errStorage
}

func TestStorageErrorsPropagate(t *testing.T) {
	e := newEnv(t, failingRepo{})
	ctx := t.Context()
	if _, err := e.svc.Login(ctx, "ivan", pw); !errors.Is(err, errStorage) || errors.Is(err, ErrBadCredentials) {
		t.Errorf("Login err = %v, want the storage error", err)
	}
	if _, err := e.svc.Register(ctx, "ivan", pw); !errors.Is(err, errStorage) {
		t.Errorf("Register err = %v, want the storage error", err)
	}
	if _, err := e.svc.EnsureAdmin(ctx, "admin", pw); !errors.Is(err, errStorage) {
		t.Errorf("EnsureAdmin err = %v, want the storage error", err)
	}
}

func TestEnsureAdmin(t *testing.T) {
	e := newEnv(t, nil)
	ctx := t.Context()

	res, err := e.svc.EnsureAdmin(ctx, "Admin", pw)
	if err != nil || res != AdminCreated {
		t.Fatalf("first EnsureAdmin = %v, %v; want AdminCreated", res, err)
	}
	token, err := e.svc.Login(ctx, "admin", pw)
	if err != nil {
		t.Fatalf("admin cannot log in: %v", err)
	}
	if c := e.claims(t, token); c.Role != domain.RoleAdmin {
		t.Errorf("role = %s, want admin", c.Role)
	}

	// Restart with a changed ADMIN_PASSWORD: nothing changes.
	res, err = e.svc.EnsureAdmin(ctx, "admin", otherPw)
	if err != nil || res != AdminExists {
		t.Fatalf("second EnsureAdmin = %v, %v; want AdminExists", res, err)
	}
	if _, err := e.svc.Login(ctx, "admin", pw); err != nil {
		t.Errorf("old admin password stopped working: %v", err)
	}
	if _, err := e.svc.Login(ctx, "admin", otherPw); err == nil {
		t.Error("ADMIN_PASSWORD reset the existing admin's password")
	}
}

func TestEnsureAdmin_LoginTakenByCustomer(t *testing.T) {
	e := newEnv(t, nil)
	ctx := t.Context()
	if _, err := e.svc.Register(ctx, "boss", pw); err != nil {
		t.Fatal(err)
	}
	_, err := e.svc.EnsureAdmin(ctx, "boss", otherPw)
	if err == nil || !strings.Contains(err.Error(), "customer") {
		t.Fatalf("err = %v, want refusal to promote a customer", err)
	}
	u, _ := e.repo.ByLogin(ctx, "boss")
	if u.Role != domain.RoleCustomer {
		t.Errorf("customer was promoted to %s", u.Role)
	}
}

func TestEnsureAdmin_Invalid(t *testing.T) {
	e := newEnv(t, nil)
	if _, err := e.svc.EnsureAdmin(t.Context(), "a b", pw); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("bad login: err = %v", err)
	}
	if _, err := e.svc.EnsureAdmin(t.Context(), "admin", strings.Repeat("p", 73)); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("73-byte password: err = %v", err)
	}
}

// racingRepo simulates another server creating the admin between our
// lookup and our INSERT.
type racingRepo struct {
	*memory.Users
	raced bool
}

func (r *racingRepo) Create(ctx context.Context, u domain.User) (domain.User, error) {
	if !r.raced {
		r.raced = true
		if _, err := r.Users.Create(ctx, u); err != nil { // the "other server" wins
			return domain.User{}, err
		}
		return domain.User{}, domain.ErrConflict
	}
	return r.Users.Create(ctx, u)
}

func TestEnsureAdmin_ConcurrentStart(t *testing.T) {
	repo := &racingRepo{Users: memory.NewUsers()}
	e := newEnv(t, repo)
	res, err := e.svc.EnsureAdmin(t.Context(), "admin", pw)
	if err != nil || res != AdminExists {
		t.Errorf("EnsureAdmin after losing the race = %v, %v; want AdminExists", res, err)
	}
}

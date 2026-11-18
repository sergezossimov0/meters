package config

import (
	"errors"
	"fmt"
	"time"
	_ "time/tzdata" // TIMEZONE must load in distroless/scratch images without zoneinfo
	"unicode/utf8"
)

// Server is the configuration of cmd/server (spec п. 10.2).
type Server struct {
	Address            string
	DatabaseDSN        Secret
	Key                Secret // HashSHA256 signing key; empty disables signing
	JWTSecret          Secret
	JWTTTL             time.Duration
	AdminLogin         string
	AdminPassword      Secret
	Timezone           *time.Location
	AnomalyInterval    time.Duration
	BatchRetentionDays int
	MaxBodyBytes       int64
	LogLevel           string
}

// Validation limits.
const (
	jwtKeyMinBytes    = 32 // HS256 needs a key of at least 256 bits (RFC 7518 §3.2)
	adminCredMinChars = 8
	bodyLimitMinBytes = 1 << 10
)

// LoadServer reads the server configuration from args (without the program
// name) and the environment. All validation errors are reported at once.
func LoadServer(args []string, lookupEnv LookupEnv) (Server, error) {
	c := Server{
		Address:            "localhost:8080",
		JWTTTL:             24 * time.Hour,
		AnomalyInterval:    600 * time.Second,
		BatchRetentionDays: 7,
		MaxBodyBytes:       10 << 20,
		LogLevel:           "info",
	}
	timezone := "Asia/Almaty"

	settings := []setting{
		{"a", "ADDRESS", stringValue{&c.Address}, "HTTP server address host:port"},
		{"d", "DATABASE_DSN", secretValue{&c.DatabaseDSN}, "PostgreSQL DSN (required)"},
		{"k", "KEY", secretValue{&c.Key}, "HashSHA256 signing key; empty disables signing"},
		{"j", "JWT_SECRET", secretValue{&c.JWTSecret}, "JWT signing key, at least 32 bytes (required)"},
		{"", "JWT_TTL", durationValue{&c.JWTTTL}, "JWT lifetime, e.g. 24h (default 24h)"},
		{"", "ADMIN_LOGIN", stringValue{&c.AdminLogin}, "first admin login, created if absent"},
		{"", "ADMIN_PASSWORD", secretValue{&c.AdminPassword}, "first admin password, at least 8 characters"},
		{"", "TIMEZONE", stringValue{&timezone}, "time zone for monthly calculations (default Asia/Almaty)"},
		{"", "ANOMALY_INTERVAL", secondsValue{&c.AnomalyInterval}, "anomaly worker interval, seconds (default 600)"},
		{"", "BATCH_RETENTION_DAYS", intValue{&c.BatchRetentionDays}, "gateway_batches retention, days (default 7)"},
		{"", "MAX_BODY_BYTES", int64Value{&c.MaxBodyBytes}, "request body limit after gzip decoding (default 10485760)"},
		{"", "LOG_LEVEL", stringValue{&c.LogLevel}, "debug, info, warn, error (default info)"},
	}
	if err := load("server", args, lookupEnv, settings); err != nil {
		return Server{}, err
	}

	var errs []error
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		errs = append(errs, fmt.Errorf("TIMEZONE: unknown time zone %q", timezone))
	}
	c.Timezone = loc

	if err := c.validate(); err != nil {
		errs = append(errs, err)
	}
	if err := errors.Join(errs...); err != nil {
		return Server{}, err
	}
	return c, nil
}

func (c Server) validate() error {
	var errs []error
	add := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }

	if c.Address == "" {
		add("ADDRESS (-a): must not be empty")
	}
	if !c.DatabaseDSN.IsSet() {
		add("DATABASE_DSN (-d): required")
	}
	if !c.JWTSecret.IsSet() {
		add("JWT_SECRET (-j): required")
	} else if len(c.JWTSecret.Value()) < jwtKeyMinBytes {
		add("JWT_SECRET (-j): must be at least %d bytes", jwtKeyMinBytes)
	}
	if c.JWTTTL <= 0 {
		add("JWT_TTL: must be positive")
	}
	if err := checkAdmin(c.AdminLogin, c.AdminPassword); err != nil {
		errs = append(errs, err)
	}
	if c.AnomalyInterval <= 0 {
		add("ANOMALY_INTERVAL: must be positive")
	}
	if c.BatchRetentionDays < 1 {
		add("BATCH_RETENTION_DAYS: must be at least 1")
	}
	if c.MaxBodyBytes < bodyLimitMinBytes {
		add("MAX_BODY_BYTES: must be at least %d", bodyLimitMinBytes)
	}
	if err := validateLogLevel(c.LogLevel); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// checkAdmin validates the optional first-admin pair: both or neither.
func checkAdmin(login string, cred Secret) error {
	switch {
	case login == "" && cred.IsSet():
		return errors.New("ADMIN_LOGIN and ADMIN_PASSWORD must be set together (login missing)")
	case login != "" && !cred.IsSet():
		return errors.New("ADMIN_LOGIN and ADMIN_PASSWORD must be set together (password missing)")
	case cred.IsSet() && utf8.RuneCountInString(cred.Value()) < adminCredMinChars:
		return fmt.Errorf("the first admin password must be at least %d characters", adminCredMinChars)
	}
	return nil
}

// String is a log-safe summary: secrets are reported only as set/unset,
// the DSN without its password.
func (c Server) String() string {
	tz := ""
	if c.Timezone != nil {
		tz = c.Timezone.String()
	}
	return fmt.Sprintf(
		"address=%s dsn=%s key=%s jwt=%s jwt_ttl=%s admin_login=%s timezone=%s "+
			"anomaly_interval=%s batch_retention_days=%d max_body_bytes=%d log_level=%s",
		c.Address, RedactDSN(c.DatabaseDSN.Value()), setOrUnset(c.Key), setOrUnset(c.JWTSecret), c.JWTTTL,
		c.AdminLogin, tz, c.AnomalyInterval, c.BatchRetentionDays, c.MaxBodyBytes, c.LogLevel,
	)
}

func setOrUnset(s Secret) string {
	if s.IsSet() {
		return "set"
	}
	return "unset"
}

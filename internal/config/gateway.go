package config

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Gateway is the configuration of cmd/gateway (spec п. 10.2).
type Gateway struct {
	Address         string // as given: host:port or URL
	ServerURL       string // normalized base URL, e.g. http://localhost:8080
	GatewayKey      Secret
	Key             Secret // HashSHA256 signing key; empty disables signing
	PollInterval    time.Duration
	ReportInterval  time.Duration
	RateLimit       int
	StoreInterval   time.Duration // 0 = save the buffer after every change
	FileStoragePath string
	Restore         bool
	Devices         int
	Speed           int
	Backfill        time.Duration
	BatchSize       int
	LogLevel        string
}

// Validation limits.
const (
	maxReportInterval = time.Hour // heartbeat report_interval_s is limited to 3600 on the server
	maxDevices        = 10_000
	maxBackfill       = 90 * 24 * time.Hour // older readings are rejected as too_old
	maxBatchSize      = 5_000               // server limit per batch
)

// LoadGateway reads the gateway configuration from args (without the program
// name) and the environment. All validation errors are reported at once.
func LoadGateway(args []string, lookupEnv LookupEnv) (Gateway, error) {
	c := Gateway{
		Address:         "localhost:8080",
		PollInterval:    2 * time.Second,
		ReportInterval:  10 * time.Second,
		RateLimit:       2,
		StoreInterval:   300 * time.Second,
		FileStoragePath: "gateway-buffer.json",
		Restore:         true,
		Devices:         10,
		Speed:           3600,
		Backfill:        30 * 24 * time.Hour,
		BatchSize:       1000,
		LogLevel:        "info",
	}

	settings := []setting{
		{"a", "ADDRESS", stringValue{&c.Address}, "server address host:port or URL"},
		{"g", "GATEWAY_KEY", secretValue{&c.GatewayKey}, "gateway API key (required)"},
		{"k", "KEY", secretValue{&c.Key}, "HashSHA256 signing key; empty disables signing"},
		{"p", "POLL_INTERVAL", secondsValue{&c.PollInterval}, "device poll and metrics interval, seconds"},
		{"r", "REPORT_INTERVAL", secondsValue{&c.ReportInterval}, "batch and heartbeat send interval, seconds"},
		{"l", "RATE_LIMIT", intValue{&c.RateLimit}, "max concurrent requests to the server"},
		{"i", "STORE_INTERVAL", secondsValue{&c.StoreInterval}, "buffer save interval, seconds (0 = synchronous)"},
		{"f", "FILE_STORAGE_PATH", stringValue{&c.FileStoragePath}, "buffer file"},
		{"restore", "RESTORE", boolValue{&c.Restore}, "load the buffer from the file at start"},
		{"n", "DEVICES", intValue{&c.Devices}, "number of simulated devices"},
		{"speed", "SPEED", intValue{&c.Speed}, "model seconds per real second until model time catches up"},
		{"backfill", "BACKFILL", durationValue{&c.Backfill}, "how far in the past model time starts, e.g. 720h"},
		{"b", "BATCH_SIZE", intValue{&c.BatchSize}, "max readings per batch (1-5000)"},
		{"", "LOG_LEVEL", stringValue{&c.LogLevel}, "debug, info, warn, error (default info)"},
	}
	if err := load("gateway", args, lookupEnv, settings); err != nil {
		return Gateway{}, err
	}

	var errs []error
	serverURL, err := normalizeServerURL(c.Address)
	if err != nil {
		errs = append(errs, fmt.Errorf("ADDRESS (-a): %w", err))
	}
	c.ServerURL = serverURL

	if err := c.validate(); err != nil {
		errs = append(errs, err)
	}
	if err := errors.Join(errs...); err != nil {
		return Gateway{}, err
	}
	return c, nil
}

func (c Gateway) validate() error {
	var errs []error
	add := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }

	if !c.GatewayKey.IsSet() {
		add("GATEWAY_KEY (-g): required")
	}
	if c.PollInterval <= 0 {
		add("POLL_INTERVAL (-p): must be positive")
	}
	if c.ReportInterval <= 0 || c.ReportInterval > maxReportInterval {
		add("REPORT_INTERVAL (-r): must be between 1 and %d seconds", int(maxReportInterval/time.Second))
	}
	if c.RateLimit < 1 {
		add("RATE_LIMIT (-l): must be at least 1")
	}
	if c.FileStoragePath == "" {
		add("FILE_STORAGE_PATH (-f): must not be empty")
	}
	if c.Devices < 1 || c.Devices > maxDevices {
		add("DEVICES (-n): must be between 1 and %d", maxDevices)
	}
	if c.Speed < 1 {
		add("SPEED (-speed): must be at least 1")
	}
	if c.Backfill < 0 || c.Backfill > maxBackfill {
		add("BACKFILL (-backfill): must be between 0 and %s", maxBackfill)
	}
	if c.BatchSize < 1 || c.BatchSize > maxBatchSize {
		add("BATCH_SIZE (-b): must be between 1 and %d", maxBatchSize)
	}
	if err := validateLogLevel(c.LogLevel); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// normalizeServerURL accepts "host:port" (as in the course) or a full URL and
// returns a base URL without a trailing slash.
func normalizeServerURL(address string) (string, error) {
	if address == "" {
		return "", errors.New("must not be empty")
	}
	if !strings.Contains(address, "://") {
		address = "http://" + address
	}
	u, err := url.Parse(address)
	if err != nil {
		return "", fmt.Errorf("invalid address %q", address)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("scheme must be http or https, got %q", u.Scheme)
	}
	if u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("invalid address %q", address)
	}
	return strings.TrimRight(u.String(), "/"), nil
}

// String is a log-safe summary: secrets are reported only as set/unset.
func (c Gateway) String() string {
	return fmt.Sprintf(
		"server_url=%s gateway_key=%s key=%s poll=%s report=%s rate_limit=%d store=%s file=%s "+
			"restore=%t devices=%d speed=%d backfill=%s batch_size=%d log_level=%s",
		c.ServerURL, setOrUnset(c.GatewayKey), setOrUnset(c.Key), c.PollInterval, c.ReportInterval,
		c.RateLimit, c.StoreInterval, c.FileStoragePath, c.Restore, c.Devices, c.Speed, c.Backfill,
		c.BatchSize, c.LogLevel,
	)
}

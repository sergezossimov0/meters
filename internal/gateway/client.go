package gateway

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/sergezossimov0/meters/internal/domain"
	"github.com/sergezossimov0/meters/internal/retry"
	"github.com/sergezossimov0/meters/internal/sign"
)

// Errors of Client.SendBatch; check them with errors.Is.
var (
	// ErrPoison: the server refused the batch itself (400, 413). Sending it
	// again would fail again, so its readings are dropped (spec п. 7.4).
	ErrPoison = errors.New("server refused the batch")
	// ErrAuth: the server refused the gateway (401, 403). The readings stay:
	// the key may be fixed or the gateway unblocked.
	ErrAuth = errors.New("server refused the gateway")
	// errTransient marks failures worth repeating: no connection, a timeout,
	// 5xx, an unsigned or wrongly signed answer.
	errTransient = errors.New("transient failure")
)

// Client limits.
const (
	// RequestTimeout bounds one attempt, connection to last byte.
	RequestTimeout = 30 * time.Second
	// maxAnswerBytes bounds the uncompressed answer.
	maxAnswerBytes = 1 << 20

	gatewayKeyHeader = "X-Gateway-Key"
)

// Client talks to the server's gateway API: gzip both ways, HashSHA256 on the
// uncompressed bodies, repeats after 1, 3 and 5 s (spec п. 6.1, 7.4).
type Client struct {
	baseURL    string
	gatewayKey string
	signKey    []byte
	http       *http.Client
	retry      retry.Retrier
}

// NewClient returns a client of the server at baseURL. An empty signKey
// turns signing off. transport may be nil for http.DefaultTransport.
func NewClient(baseURL, gatewayKey string, signKey []byte, transport http.RoundTripper, log *zap.Logger) *Client {
	return &Client{
		baseURL:    baseURL,
		gatewayKey: gatewayKey,
		signKey:    signKey,
		http:       &http.Client{Timeout: RequestTimeout, Transport: transport},
		retry: retry.Retrier{
			Retryable: func(err error) bool { return errors.Is(err, errTransient) },
			OnRetry: func(attempt int, wait time.Duration, err error) {
				log.Warn("send failed, retrying",
					zap.Int("attempt", attempt), zap.Duration("wait", wait), zap.Error(err))
			},
		},
	}
}

// SendBatch posts a batch of readings and returns the server's answer.
func (c *Client) SendBatch(ctx context.Context, req domain.BatchRequest) (domain.BatchResult, error) {
	var res domain.BatchResult
	err := c.post(ctx, "/gateway/v1/readings", req, &res)
	return res, err
}

// post sends in as JSON to path and decodes a 200 answer into out.
func (c *Client) post(ctx context.Context, path string, in, out any) error {
	body, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}
	var sig string
	if len(c.signKey) > 0 {
		sig = sign.Sum(body, c.signKey) // over the uncompressed body
	}
	var zbuf bytes.Buffer
	zw := gzip.NewWriter(&zbuf)
	_, _ = zw.Write(body) // writes into memory cannot fail
	_ = zw.Close()

	return c.retry.Do(ctx, func(ctx context.Context) error {
		answer, err := c.attempt(ctx, path, zbuf.Bytes(), sig)
		if err != nil {
			return err
		}
		if out != nil {
			if err := json.Unmarshal(answer, out); err != nil {
				return fmt.Errorf("decode answer: %w", err)
			}
		}
		return nil
	})
}

// attempt makes one request and returns the uncompressed body of a 200.
func (c *Client) attempt(ctx context.Context, path string, gzBody []byte, sig string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(gzBody))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	// Set by hand, so the transport does not decompress the answer itself:
	// the signature is checked here, over the uncompressed body.
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set(gatewayKeyHeader, c.gatewayKey)
	if sig != "" {
		req.Header.Set(sign.Header, sig)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, err // the caller gave up: no repeats
		}
		return nil, fmt.Errorf("%w: %w", errTransient, err)
	}
	defer func() { _ = resp.Body.Close() }()

	answer, err := readAnswer(resp)
	if err != nil {
		return nil, fmt.Errorf("%w: read answer: %w", errTransient, err)
	}
	if len(c.signKey) > 0 && !sign.Verify(answer, c.signKey, resp.Header.Get(sign.Header)) {
		return nil, fmt.Errorf("%w: answer %d is not signed correctly", errTransient, resp.StatusCode)
	}

	switch code := resp.StatusCode; {
	case code == http.StatusOK:
		return answer, nil
	case code == http.StatusBadRequest || code == http.StatusRequestEntityTooLarge:
		return nil, fmt.Errorf("%w: %d %s", ErrPoison, code, errorText(answer))
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return nil, fmt.Errorf("%w: %d %s", ErrAuth, code, errorText(answer))
	case code >= 500:
		return nil, fmt.Errorf("%w: %d %s", errTransient, code, errorText(answer))
	default:
		// 404, 405, 415…: a wrong address or an incompatible server. Not
		// repeated now, but the readings stay for the next cycle.
		return nil, fmt.Errorf("unexpected answer %d %s", code, errorText(answer))
	}
}

// readAnswer reads the body, uncompressing it if the server used gzip.
func readAnswer(resp *http.Response) ([]byte, error) {
	r := io.Reader(resp.Body)
	if resp.Header.Get("Content-Encoding") == "gzip" {
		zr, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, err
		}
		defer func() { _ = zr.Close() }()
		r = zr
	}
	body, err := io.ReadAll(io.LimitReader(r, maxAnswerBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxAnswerBytes {
		return nil, fmt.Errorf("answer longer than %d bytes", maxAnswerBytes)
	}
	return body, nil
}

// errorText takes the message from {"error": "..."} for logs.
func errorText(body []byte) string {
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil && e.Error != "" {
		return e.Error
	}
	return ""
}

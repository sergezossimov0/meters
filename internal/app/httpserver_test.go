package app

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"go.uber.org/zap"
)

// pipeListener is an in-memory net.Listener built on net.Pipe, so the HTTP
// server runs inside a synctest bubble with fake time and no real sockets.
type pipeListener struct {
	conns  chan net.Conn
	closed chan struct{}
	once   sync.Once
}

func newPipeListener() *pipeListener {
	return &pipeListener{conns: make(chan net.Conn), closed: make(chan struct{})}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *pipeListener) Addr() net.Addr { return pipeAddr{} }

func (l *pipeListener) dial() (net.Conn, error) {
	client, server := net.Pipe()
	select {
	case l.conns <- server:
		return client, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

type pipeAddr struct{}

func (pipeAddr) Network() string { return "pipe" }
func (pipeAddr) String() string  { return "pipe" }

// get sends GET / over a fresh pipe connection and returns the status code.
func get(t *testing.T, l *pipeListener) (int, error) {
	t.Helper()
	conn, err := l.dial()
	if err != nil {
		return 0, err
	}
	defer func() { _ = conn.Close() }()
	if _, err := io.WriteString(conn, "GET / HTTP/1.1\r\nHost: test\r\n\r\n"); err != nil {
		return 0, err
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		return 0, err
	}
	_ = resp.Body.Close()
	return resp.StatusCode, nil
}

// slowHandler answers after delay and reports when the request has arrived.
func slowHandler(delay time.Duration, started chan<- struct{}) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		started <- struct{}{}
		time.Sleep(delay)
		w.WriteHeader(http.StatusOK)
	})
}

func TestServeHTTP_ShutdownWaitsForInFlightRequest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		l := newPipeListener()
		started := make(chan struct{})
		srv := NewHTTPServer(slowHandler(3*time.Second, started))

		served := make(chan error, 1)
		begin := time.Now()
		go func() { served <- ServeHTTP(ctx, srv, l, ShutdownTimeout, zap.NewNop()) }()

		status := make(chan int, 1)
		go func() {
			code, err := get(t, l)
			if err != nil {
				t.Errorf("request: %v", err)
			}
			status <- code
		}()

		<-started
		cancel() // SIGTERM while the request is being handled

		if err := <-served; err != nil {
			t.Fatalf("ServeHTTP: %v", err)
		}
		if elapsed := time.Since(begin); elapsed < 3*time.Second {
			t.Errorf("returned after %v, before the handler finished", elapsed)
		}
		if code := <-status; code != http.StatusOK {
			t.Errorf("in-flight request status = %d, want 200", code)
		}
		if _, err := l.dial(); !errors.Is(err, net.ErrClosed) {
			t.Errorf("new connection after shutdown: err = %v, want net.ErrClosed", err)
		}
	})
}

func TestServeHTTP_ShutdownTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		l := newPipeListener()
		started := make(chan struct{})
		srv := NewHTTPServer(slowHandler(time.Minute, started))

		served := make(chan error, 1)
		go func() { served <- ServeHTTP(ctx, srv, l, ShutdownTimeout, zap.NewNop()) }()
		go func() { _, _ = get(t, l) }()

		<-started
		begin := time.Now()
		cancel()

		err := <-served
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("ServeHTTP error = %v, want deadline exceeded", err)
		}
		if elapsed := time.Since(begin); elapsed != ShutdownTimeout {
			t.Errorf("gave up after %v, want %v", elapsed, ShutdownTimeout)
		}
		// Let the stuck handler finish so the bubble can exit.
		time.Sleep(time.Minute)
	})
}

func TestServeHTTP_ListenerFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newPipeListener()
		_ = l.Close()
		err := ServeHTTP(t.Context(), NewHTTPServer(http.NotFoundHandler()), l, ShutdownTimeout, zap.NewNop())
		if !errors.Is(err, net.ErrClosed) {
			t.Errorf("error = %v, want net.ErrClosed", err)
		}
	})
}

func TestNewHTTPServer_Timeouts(t *testing.T) {
	srv := NewHTTPServer(http.NotFoundHandler())
	if srv.ReadHeaderTimeout != 5*time.Second || srv.ReadTimeout != 30*time.Second || srv.WriteTimeout != 30*time.Second {
		t.Errorf("timeouts = %v/%v/%v", srv.ReadHeaderTimeout, srv.ReadTimeout, srv.WriteTimeout)
	}
}

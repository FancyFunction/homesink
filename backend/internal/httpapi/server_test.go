package httpapi_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FancyFunction/homesink/backend/internal/config"
	"github.com/FancyFunction/homesink/backend/internal/httpapi"
)

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	env := map[string]string{
		"HOMESINK_DATA": t.TempDir(),
		"HOMESINK_ADDR": "127.0.0.1:0",
		"HOMESINK_TLS":  "off",
	}
	c, err := config.Load(func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	return c
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

// --- Acceptance: /healthz returns 200 before any other subsystem is ready ---

func TestHealthz_Returns200BeforeSubsystemsReady(t *testing.T) {
	// A freshly constructed server: no store, no jobs, no health check wired.
	s := httpapi.New(testConfig(t), discardLogger())

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != "ok" {
		t.Errorf("body = %q, want %q", got, "ok")
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain", ct)
	}
}

func TestHealthz_503WhenHealthCheckFails(t *testing.T) {
	s := httpapi.New(testConfig(t), discardLogger())
	s.SetHealthCheck(func(context.Context) error { return errors.New("db: read-only data dir") })

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "read-only") {
		t.Errorf("body = %q, want the failure reason", rec.Body.String())
	}
}

// --- Acceptance: SIGTERM during an in-flight request completes it ---

func TestShutdown_CompletesInFlightRequest(t *testing.T) {
	s := httpapi.New(testConfig(t), discardLogger())

	release := make(chan struct{})
	reached := make(chan struct{})
	s.Register(func(mux *http.ServeMux) {
		mux.HandleFunc("GET /slow", func(w http.ResponseWriter, _ *http.Request) {
			close(reached)
			<-release // hold the request open until the test lets it finish
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "done")
		})
	})

	serveErr := make(chan error, 1)
	go func() { serveErr <- s.Start() }()
	<-s.Ready()

	type result struct {
		status int
		body   string
		err    error
	}
	resc := make(chan result, 1)
	go func() {
		resp, err := http.Get("http://" + s.Addr() + "/slow")
		if err != nil {
			resc <- result{err: err}
			return
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		resc <- result{status: resp.StatusCode, body: string(b)}
	}()

	<-reached // request is now inside the handler

	// Simulate the SIGTERM path: begin draining while the request is in flight.
	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- s.Shutdown(context.Background()) }()

	// Give Shutdown a moment to start refusing new connections, then let the
	// in-flight request finish.
	time.Sleep(50 * time.Millisecond)
	close(release)

	select {
	case r := <-resc:
		if r.err != nil {
			t.Fatalf("in-flight request failed instead of completing: %v", r.err)
		}
		if r.status != http.StatusOK || r.body != "done" {
			t.Fatalf("in-flight request got %d %q, want 200 %q", r.status, r.body, "done")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("in-flight request never completed")
	}

	if err := <-shutdownDone; err != nil {
		t.Errorf("Shutdown returned %v", err)
	}
	if err := <-serveErr; err != nil {
		t.Errorf("Start returned %v", err)
	}
}

func TestShutdown_RefusesNewConnectionsWhileDraining(t *testing.T) {
	s := httpapi.New(testConfig(t), discardLogger())
	serveErr := make(chan error, 1)
	go func() { serveErr <- s.Start() }()
	<-s.Ready()
	addr := s.Addr()

	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if err := <-serveErr; err != nil {
		t.Fatalf("Start: %v", err)
	}

	client := &http.Client{Timeout: 500 * time.Millisecond}
	if _, err := client.Get("http://" + addr + "/healthz"); err == nil {
		t.Error("expected the server to reject requests after shutdown")
	}
}

// --- Middleware ---

func TestMiddleware_SetsServerTimeHeader(t *testing.T) {
	s := httpapi.New(testConfig(t), discardLogger())
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	ts := rec.Header().Get("X-Homesink-Time")
	if ts == "" {
		t.Fatal("X-Homesink-Time header missing")
	}
	if _, err := time.Parse(time.RFC3339, ts); err != nil {
		t.Errorf("X-Homesink-Time %q is not RFC3339: %v", ts, err)
	}
}

func TestMiddleware_RequestIDGeneratedWhenAbsent(t *testing.T) {
	s := httpapi.New(testConfig(t), discardLogger())
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if id := rec.Header().Get("X-Request-Id"); len(id) < 8 {
		t.Errorf("X-Request-Id = %q, want a generated id", id)
	}
}

func TestMiddleware_RequestIDEchoedWhenValid(t *testing.T) {
	s := httpapi.New(testConfig(t), discardLogger())
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("X-Request-Id", "client-supplied-123")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Request-Id"); got != "client-supplied-123" {
		t.Errorf("X-Request-Id = %q, want it echoed", got)
	}
}

func TestMiddleware_RejectsHostileRequestID(t *testing.T) {
	s := httpapi.New(testConfig(t), discardLogger())
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("X-Request-Id", "bad id with spaces\nand newline")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)

	got := rec.Header().Get("X-Request-Id")
	if strings.ContainsAny(got, " \n") || got == "bad id with spaces\nand newline" {
		t.Errorf("hostile X-Request-Id was not replaced: %q", got)
	}
}

func TestMiddleware_PanicRecoveryReturns500Envelope(t *testing.T) {
	s := httpapi.New(testConfig(t), discardLogger())
	s.Register(func(mux *http.ServeMux) {
		mux.HandleFunc("GET /boom", func(http.ResponseWriter, *http.Request) {
			panic("kaboom")
		})
	})

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/boom", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "INTERNAL") {
		t.Errorf("body = %q, want the INTERNAL error envelope", rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}

// concurrencyGuard is a tiny race-detector target: many requests through the
// middleware chain at once must not trip -race.
func TestMiddleware_ConcurrentRequestsAreRaceFree(t *testing.T) {
	s := httpapi.New(testConfig(t), discardLogger())
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
			if rec.Code != http.StatusOK {
				t.Errorf("status = %d", rec.Code)
			}
		}()
	}
	wg.Wait()
}

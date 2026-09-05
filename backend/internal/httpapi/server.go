// Package httpapi owns the HTTP server: the router, the middleware chain, and
// the daemon lifecycle. Feature packages register their handlers from their own
// handlers_<x>.go files via Register; they must not edit this file or
// middleware.go (08-ROADMAP.md §4).
package httpapi

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/FancyFunction/homesink/backend/internal/config"
)

// drainTimeout bounds graceful shutdown: stop accepting, let in-flight
// requests finish, then force-close (WP-B1: "drain in-flight ≤30 s").
const drainTimeout = 30 * time.Second

// HealthFunc reports service readiness for GET /healthz. A nil error means
// ready (200); a non-nil error means not ready (503) and its message is the
// response body. WP-B11 installs the real check (DB open, migrations applied,
// library root writable) via SetHealthCheck.
type HealthFunc func(ctx context.Context) error

// Server is the daemon's HTTP surface.
type Server struct {
	cfg *config.Config
	log *slog.Logger
	mux *http.ServeMux
	srv *http.Server

	handler http.Handler

	mu        sync.RWMutex
	health    HealthFunc
	tlsConfig *tls.Config
	ln        net.Listener
	ready     chan struct{}
}

// ServeHTTP runs the full middleware chain and router. It lets tests exercise
// the server without opening a socket, and lets the server be embedded as a
// plain http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.handler.ServeHTTP(w, r) }

// New builds the server, its middleware chain, and the always-available
// /healthz route. No other subsystem needs to be ready for /healthz to answer
// 200 (WP-B1 acceptance).
func New(cfg *config.Config, log *slog.Logger) *Server {
	s := &Server{
		cfg:   cfg,
		log:   log,
		mux:   http.NewServeMux(),
		ready: make(chan struct{}),
	}
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)

	s.handler = s.chain(s.mux)
	s.srv = &http.Server{
		Addr:              cfg.Addr,
		Handler:           s.handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	return s
}

// Register lets a feature package add its routes without touching the router
// file. Call it before Start.
func (s *Server) Register(fn func(mux *http.ServeMux)) { fn(s.mux) }

// SetHealthCheck installs the readiness probe used by GET /healthz. Passing nil
// restores the default (always ready).
func (s *Server) SetHealthCheck(fn HealthFunc) {
	s.mu.Lock()
	s.health = fn
	s.mu.Unlock()
}

// SetTLSConfig supplies the TLS configuration produced by WP-B3's EnsureTLS.
// When nil (the WP-B1 default) the server listens cleartext; HOMESINK_TLS=off
// keeps it cleartext regardless (D-02).
func (s *Server) SetTLSConfig(c *tls.Config) {
	s.mu.Lock()
	s.tlsConfig = c
	s.mu.Unlock()
}

// Start begins serving and blocks until Shutdown is called or the listener
// fails. http.ErrServerClosed is treated as a clean stop and reported as nil.
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.srv.Addr)
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.ln = ln
	tlsCfg := s.tlsConfig
	s.mu.Unlock()
	close(s.ready)

	if tlsCfg != nil && s.cfg.TLS {
		s.log.Info("http server listening", "addr", s.srv.Addr, "tls", true)
		err = s.srv.ServeTLS(ln, "", "")
	} else {
		if s.cfg.TLS {
			s.log.Warn("HOMESINK_TLS=on but no certificate is wired yet; serving cleartext until WP-B3 provides one")
		}
		s.log.Info("http server listening", "addr", s.srv.Addr, "tls", false)
		err = s.srv.Serve(ln)
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Shutdown stops accepting new connections and waits up to drainTimeout for
// in-flight requests to complete (WP-B1). The caller closes the DB and other
// subsystems after this returns.
func (s *Server) Shutdown(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, drainTimeout)
	defer cancel()
	s.log.Info("http server draining", "timeout", drainTimeout.String())
	return s.srv.Shutdown(ctx)
}

// Ready returns a channel that is closed once the server is listening. Useful
// for tests and for signalling readiness to a supervisor.
func (s *Server) Ready() <-chan struct{} { return s.ready }

// Addr returns the actual listen address (resolved, so an ":0" port becomes a
// real one). It returns "" until the server is listening.
func (s *Server) Addr() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.ln == nil {
		return ""
	}
	return s.ln.Addr().String()
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	check := s.health
	s.mu.RUnlock()

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if check != nil {
		if err := check(r.Context()); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(err.Error()))
			return
		}
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

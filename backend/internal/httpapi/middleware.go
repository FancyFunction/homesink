package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/FancyFunction/homesink/backend/internal/core"
)

// ctxKey is the private type for this package's request-context values.
type ctxKey int

const ctxKeyRequestID ctxKey = iota

// requestIDHeader is echoed on every response and appears in the access log
// (02-API.md §1).
const requestIDHeader = "X-Request-Id"

// homesinkTimeHeader carries the server clock for client skew detection (D-04).
const homesinkTimeHeader = "X-Homesink-Time"

// RequestID returns the request id assigned to r's context, or "" if none.
func RequestID(r *http.Request) string {
	if v, ok := r.Context().Value(ctxKeyRequestID).(string); ok {
		return v
	}
	return ""
}

// chain wraps h with, from outermost to innermost: panic recovery, request-id,
// X-Homesink-Time, access logging.
func (s *Server) chain(h http.Handler) http.Handler {
	return s.recoverPanic(s.withRequestID(s.withServerTime(s.accessLog(h))))
}

// recoverPanic converts a handler panic into a logged 500 with the standard
// error envelope, so one bad handler never takes down the process.
func (s *Server) recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.log.Error("panic recovered",
					"requestId", RequestID(r),
					"method", r.Method,
					"path", r.URL.Path,
					"panic", fmt.Sprint(rec),
					"stack", string(debug.Stack()),
				)
				writeError(w, core.ErrInternal())
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// withRequestID reuses a client-supplied X-Request-Id when present and sane,
// otherwise mints a UUIDv4. The value is stored in the context and echoed on
// the response.
func (s *Server) withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(requestIDHeader)
		if !validRequestID(id) {
			id = newUUID()
		}
		w.Header().Set(requestIDHeader, id)
		ctx := context.WithValue(r.Context(), ctxKeyRequestID, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// withServerTime stamps every response with the server clock in the configured
// timezone (D-04). It is set before the handler runs so it is present even on a
// streaming or hijacked response.
func (s *Server) withServerTime(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(homesinkTimeHeader, time.Now().In(s.cfg.Location).Format(time.RFC3339))
		next.ServeHTTP(w, r)
	})
}

// accessLog emits one structured line per request after it completes. Only the
// request URL is logged, never a resolved library path (00-ARCHITECTURE.md §6).
func (s *Server) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		s.log.LogAttrs(r.Context(), slog.LevelInfo, "request",
			slog.String("requestId", RequestID(r)),
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", rec.status),
			slog.Int64("bytes", rec.written),
			slog.Int64("durationMs", time.Since(start).Milliseconds()),
			slog.String("remote", clientIP(r)),
		)
	})
}

// writeError serialises a *core.Error as the standard envelope
// {"error": {code, message, retryable, details}} with its HTTP status.
func writeError(w http.ResponseWriter, e *core.Error) {
	body := map[string]any{
		"error": map[string]any{
			"code":      e.Code,
			"message":   e.Message,
			"retryable": e.Retryable,
		},
	}
	if len(e.Details) > 0 {
		body["error"].(map[string]any)["details"] = e.Details
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	status := e.HTTPStatus
	if status == 0 {
		status = http.StatusInternalServerError
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// statusRecorder captures the status code and body size for the access log
// while staying transparent to http.ResponseController (via Unwrap).
type statusRecorder struct {
	http.ResponseWriter
	status  int
	written int64
	wrote   bool
}

func (rec *statusRecorder) WriteHeader(code int) {
	if rec.wrote {
		return
	}
	rec.wrote = true
	rec.status = code
	rec.ResponseWriter.WriteHeader(code)
}

func (rec *statusRecorder) Write(b []byte) (int, error) {
	if !rec.wrote {
		rec.WriteHeader(http.StatusOK)
	}
	n, err := rec.ResponseWriter.Write(b)
	rec.written += int64(n)
	return n, err
}

// Unwrap exposes the underlying ResponseWriter so http.NewResponseController
// can reach Flusher/Hijacker on it.
func (rec *statusRecorder) Unwrap() http.ResponseWriter { return rec.ResponseWriter }

// validRequestID accepts a modest, safe subset so a hostile header cannot
// inject log noise or unbounded data.
func validRequestID(s string) bool {
	if len(s) < 8 || len(s) > 128 {
		return false
	}
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

// newUUID returns a random RFC 4122 version-4 UUID string.
func newUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failure is not recoverable; fall back to a timestamp so
		// the request still gets a unique-enough id.
		return fmt.Sprintf("ts-%d", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// clientIP extracts the remote IP without the port. Proxies are out of scope on
// a LAN, so X-Forwarded-For is deliberately ignored.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

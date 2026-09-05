package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/FancyFunction/homesink/backend/internal/core"
	"github.com/FancyFunction/homesink/backend/internal/store"
)

// DefaultTouchInterval is how often an authenticated device's last_seen_at and
// app_version_code are refreshed. Every request would turn a read-mostly API
// into a write on every call, so the middleware writes at most this often per
// device (WP-B3).
const DefaultTouchInterval = time.Minute

// clientVersionHeader carries "<versionCode>/<versionName>" on every client
// request (D-33). Only the code is used; the name is cosmetic.
const clientVersionHeader = "X-Homesink-Client"

// ctxKey is this package's private request-context key type.
type ctxKey int

const ctxKeyDevice ctxKey = iota

// AuthenticatorOptions configures NewAuthenticator; the zero value is usable.
type AuthenticatorOptions struct {
	// Now is the clock; nil means time.Now.
	Now func() time.Time
	// Logger receives authentication events; nil discards them.
	Logger *slog.Logger
	// TouchInterval overrides DefaultTouchInterval when non-zero.
	TouchInterval time.Duration
}

// Authenticator verifies device tokens. It is safe for concurrent use, and one
// instance should be shared by every protected route so the touch throttle is
// global rather than per-handler.
type Authenticator struct {
	store      store.Store
	now        func() time.Time
	log        *slog.Logger
	touchEvery time.Duration

	mu        sync.Mutex
	lastTouch map[string]time.Time
}

// NewAuthenticator returns an Authenticator backed by st.
func NewAuthenticator(st store.Store, opts AuthenticatorOptions) *Authenticator {
	a := &Authenticator{
		store:      st,
		now:        opts.Now,
		log:        opts.Logger,
		touchEvery: opts.TouchInterval,
		lastTouch:  map[string]time.Time{},
	}
	if a.now == nil {
		a.now = time.Now
	}
	if a.log == nil {
		a.log = slog.New(slog.DiscardHandler)
	}
	if a.touchEvery <= 0 {
		a.touchEvery = DefaultTouchInterval
	}
	return a
}

// Require wraps next so that only a request carrying a live device token
// reaches it. Everything else gets 401 UNAUTHORIZED with the standard envelope:
// a missing or malformed Authorization header, an unknown token, and a token
// whose device has been revoked (D-01 revocation is effective on the very next
// request).
//
// On success the device is available to the handler through DeviceFromContext.
func (a *Authenticator) Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		device, err := a.authenticate(r)
		if err != nil {
			var cerr *core.Error
			if !errors.As(err, &cerr) {
				a.log.ErrorContext(r.Context(), "authentication failed",
					"path", r.URL.Path, "error", err)
				cerr = core.ErrInternal()
			}
			writeError(w, cerr)
			return
		}
		next.ServeHTTP(w, r.WithContext(WithDevice(r.Context(), device)))
	})
}

// authenticate resolves the request's bearer token to a live device. It returns
// a *core.Error for anything the client should see and a plain error for an
// infrastructure failure it should not.
func (a *Authenticator) authenticate(r *http.Request) (*store.Device, error) {
	token, ok := BearerToken(r)
	if !ok || !ValidTokenFormat(token) {
		a.log.DebugContext(r.Context(), "request without a usable bearer token", "path", r.URL.Path)
		return nil, core.ErrUnauthorized()
	}

	device, err := a.store.DeviceByTokenHash(r.Context(), HashToken(token))
	if errors.Is(err, core.ErrNotFound()) {
		// Unknown or revoked: DeviceByTokenHash excludes revoked devices, which
		// is what makes a revocation bite on the next request.
		a.log.InfoContext(r.Context(), "token rejected",
			"path", r.URL.Path, "tokenFingerprint", Fingerprint(token))
		return nil, core.ErrUnauthorized()
	}
	if err != nil {
		return nil, fmt.Errorf("auth: look up device: %w", err)
	}
	if device.Revoked() || !VerifyToken(token, device.TokenHash) {
		a.log.InfoContext(r.Context(), "token rejected",
			"deviceId", device.DeviceID, "revoked", device.Revoked(),
			"tokenFingerprint", Fingerprint(token))
		return nil, core.ErrUnauthorized()
	}

	a.touch(r, device)
	return device, nil
}

// touch refreshes last_seen_at and app_version_code at most once per
// TouchInterval per device. A failure is logged and swallowed: bookkeeping must
// never fail a request that was correctly authenticated.
func (a *Authenticator) touch(r *http.Request, device *store.Device) {
	now := a.now()

	a.mu.Lock()
	last, seen := a.lastTouch[device.DeviceID]
	due := !seen || now.Sub(last) >= a.touchEvery
	if due {
		a.lastTouch[device.DeviceID] = now
	}
	a.mu.Unlock()
	if !due {
		return
	}

	version := ClientVersionCode(r)
	if err := a.store.TouchDevice(r.Context(), device.DeviceID, version); err != nil {
		a.log.WarnContext(r.Context(), "updating device last_seen failed",
			"deviceId", device.DeviceID, "error", err)
		return
	}
	device.LastSeenAt = now.UnixMilli()
	if version != 0 {
		device.AppVersionCode = version
	}
}

// BearerToken extracts the token from an Authorization header. The scheme match
// is case-insensitive, as RFC 7235 requires.
func BearerToken(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	if h == "" {
		return "", false
	}
	scheme, rest, found := strings.Cut(h, " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	token := strings.TrimSpace(rest)
	return token, token != ""
}

// ClientVersionCode parses the versionCode out of "X-Homesink-Client:
// <code>/<name>" (D-33). It returns 0 when the header is absent or unparseable,
// which TouchDevice reads as "leave the stored value alone".
func ClientVersionCode(r *http.Request) int64 {
	h := strings.TrimSpace(r.Header.Get(clientVersionHeader))
	if h == "" {
		return 0
	}
	codePart, _, _ := strings.Cut(h, "/")
	code, err := strconv.ParseInt(strings.TrimSpace(codePart), 10, 64)
	if err != nil || code < 0 {
		return 0
	}
	return code
}

// WithDevice returns a copy of ctx carrying device. Handlers normally receive
// it from Require; tests use this directly.
func WithDevice(ctx context.Context, device *store.Device) context.Context {
	return context.WithValue(ctx, ctxKeyDevice, device)
}

// DeviceFromContext returns the authenticated device behind a request, or false
// if the request did not pass through Require.
func DeviceFromContext(ctx context.Context) (*store.Device, bool) {
	d, ok := ctx.Value(ctxKeyDevice).(*store.Device)
	return d, ok
}

// ClientIP is the remote address without its port, the key the pairing rate
// limiter uses (D-01). Proxies are out of scope on a LAN, so X-Forwarded-For is
// deliberately ignored.
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// writeError serialises a *core.Error as the envelope of 02-API.md §2. The
// httpapi package has its own copy for handlers; this one keeps the middleware
// from importing it, which would be a cycle.
func writeError(w http.ResponseWriter, e *core.Error) {
	payload := map[string]any{
		"code":      e.Code,
		"message":   e.Message,
		"retryable": e.Retryable,
	}
	if len(e.Details) > 0 {
		payload["details"] = e.Details
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	status := e.HTTPStatus
	if status == 0 {
		status = http.StatusInternalServerError
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": payload})
}

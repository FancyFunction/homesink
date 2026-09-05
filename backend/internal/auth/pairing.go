package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/FancyFunction/homesink/backend/internal/core"
	"github.com/FancyFunction/homesink/backend/internal/store"
)

// The pairing parameters of D-01. They are constants rather than configuration:
// the requirement is minimal configuration, and a household that can retype a
// code does not need to tune its TTL.
const (
	// CodeDigits is the length of a pairing code.
	CodeDigits = 6
	// DefaultCodeTTL is how long an issued code stays redeemable.
	DefaultCodeTTL = 10 * time.Minute
	// DefaultMaxAttempts is how many failed redemptions one client IP may make
	// before it is locked out.
	DefaultMaxAttempts = 5
	// DefaultLockout is how long a client IP stays locked out after
	// DefaultMaxAttempts failures.
	DefaultLockout = 15 * time.Minute

	// MetaKeyServerID is the server_meta row holding this server's identity,
	// handed to every client during pairing.
	MetaKeyServerID = "server_id"

	serverIDPrefix = "srv_"
	deviceIDPrefix = "dev_"

	// issueRetries bounds the retries when a freshly drawn code collides with a
	// live one. With a 6-digit space and a household's worth of open codes a
	// collision is already vanishingly rare; three draws makes it impossible in
	// practice.
	issueRetries = 3
	// maxDeviceNameLen matches the contract's PairRequest.deviceName maxLength.
	maxDeviceNameLen = 64
)

// GeneratePairingCode returns a uniformly distributed 6-digit code from
// crypto/rand. Draws that would skew the distribution are rejected rather than
// folded with a modulo, so every code from 000000 to 999999 is equally likely
// (WP-B3: "reject modulo bias").
func GeneratePairingCode() (string, error) {
	const space = 1_000_000
	// The largest multiple of space that fits in a uint32; anything at or above
	// it would make the low codes marginally more likely.
	const limit = (1 << 32) - ((1 << 32) % space)

	var b [4]byte
	for {
		if _, err := rand.Read(b[:]); err != nil {
			return "", fmt.Errorf("auth: read random bytes: %w", err)
		}
		v := uint64(binary.BigEndian.Uint32(b[:]))
		if v >= limit {
			continue
		}
		return fmt.Sprintf("%0*d", CodeDigits, v%space), nil
	}
}

// HashPairingCode is the value stored in pairing_codes.code_hash: the SHA-256
// of the code, lowercase hex. The code itself is never persisted (D-01).
func HashPairingCode(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}

// ValidCodeFormat reports whether s is exactly CodeDigits ASCII digits.
func ValidCodeFormat(s string) bool {
	if len(s) != CodeDigits {
		return false
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// PairerOptions configures NewPairer. Every field has a working default, so the
// zero value is usable.
type PairerOptions struct {
	// Now is the clock; nil means time.Now. Tests inject a fixed clock.
	Now func() time.Time
	// Logger receives pairing events; nil discards them.
	Logger *slog.Logger
	// CodeTTL overrides DefaultCodeTTL when non-zero.
	CodeTTL time.Duration
	// MaxAttempts overrides DefaultMaxAttempts when non-zero.
	MaxAttempts int
	// Lockout overrides DefaultLockout when non-zero.
	Lockout time.Duration
}

// Pairer issues and redeems pairing codes. It is safe for concurrent use.
//
// The failure counter behind the lockout lives in memory, keyed by client IP,
// because that is what D-01 specifies ("rate-limited ... keyed by client IP")
// and because a restart-surviving counter would let an attacker lock a
// household out of its own server by guessing from a spoofed address. The
// per-code attempts column is maintained alongside it as the durable record.
type Pairer struct {
	store store.Store
	now   func() time.Time
	log   *slog.Logger

	codeTTL     time.Duration
	maxAttempts int
	lockout     time.Duration

	mu       sync.Mutex
	failures map[string]*failureState
}

// failureState is one client IP's standing with the rate limiter.
type failureState struct {
	count       int
	lockedUntil time.Time
	lastFailure time.Time
}

// NewPairer returns a Pairer backed by st.
func NewPairer(st store.Store, opts PairerOptions) *Pairer {
	p := &Pairer{
		store:       st,
		now:         opts.Now,
		log:         opts.Logger,
		codeTTL:     opts.CodeTTL,
		maxAttempts: opts.MaxAttempts,
		lockout:     opts.Lockout,
		failures:    map[string]*failureState{},
	}
	if p.now == nil {
		p.now = time.Now
	}
	if p.log == nil {
		p.log = slog.New(slog.DiscardHandler)
	}
	if p.codeTTL <= 0 {
		p.codeTTL = DefaultCodeTTL
	}
	if p.maxAttempts <= 0 {
		p.maxAttempts = DefaultMaxAttempts
	}
	if p.lockout <= 0 {
		p.lockout = DefaultLockout
	}
	return p
}

// Code is a freshly issued pairing code. Value is cleartext and must reach the
// user's eyes and nowhere else — not a log line, not a response to an
// unauthenticated caller other than the one that asked for it.
type Code struct {
	Value       string
	ExpiresAtMs int64
}

// IssueCode draws a new code, stores its hash with a TTL, and sweeps codes that
// have already expired. The returned Value is the only time the code exists in
// cleartext server-side.
func (p *Pairer) IssueCode(ctx context.Context) (Code, error) {
	now := p.now()
	if removed, err := p.store.DeletePairingCodesBefore(ctx, now.UnixMilli()); err != nil {
		// Housekeeping only: a failed sweep must not stop someone pairing.
		p.log.WarnContext(ctx, "sweeping expired pairing codes failed", "error", err)
	} else if removed > 0 {
		p.log.DebugContext(ctx, "expired pairing codes swept", "removed", removed)
	}

	expiresAt := now.Add(p.codeTTL).UnixMilli()
	for attempt := range issueRetries {
		code, err := GeneratePairingCode()
		if err != nil {
			return Code{}, err
		}
		err = p.store.InsertPairingCode(ctx, store.PairingCode{
			CodeHash:    HashPairingCode(code),
			ExpiresAtMs: expiresAt,
		})
		if errors.Is(err, store.ErrAlreadyExists) {
			p.log.DebugContext(ctx, "pairing code collision, drawing again", "attempt", attempt+1)
			continue
		}
		if err != nil {
			return Code{}, fmt.Errorf("auth: issue pairing code: %w", err)
		}
		p.log.InfoContext(ctx, "pairing code issued",
			"expiresAtMs", expiresAt, "ttl", p.codeTTL.String())
		return Code{Value: code, ExpiresAtMs: expiresAt}, nil
	}
	return Code{}, errors.New("auth: could not draw an unused pairing code")
}

// PairRequest is one redemption attempt.
type PairRequest struct {
	Code           string
	DeviceName     string
	Platform       string
	AppVersionCode int64
	// ClientIP keys the rate limiter (D-01). An empty value is its own bucket,
	// so a caller that cannot determine the peer address still gets limited.
	ClientIP string
}

// PairResult is a successful enrolment. Token is cleartext and is returned to
// the client exactly once; only its hash is stored.
type PairResult struct {
	Token      string
	DeviceID   string
	PairedAtMs int64
}

// Pair redeems a code and enrols a device. Every failure path returns a
// *core.Error carrying the status the contract specifies: 400
// PAIRING_CODE_INVALID for a code that is unknown or already spent, 410
// PAIRING_CODE_EXPIRED for one past its TTL, and 429 PAIRING_RATE_LIMITED once
// this client IP has burned MaxAttempts (D-01, 02-API.md §2).
func (p *Pairer) Pair(ctx context.Context, req PairRequest) (*PairResult, error) {
	if locked := p.checkLocked(req.ClientIP); locked != nil {
		return nil, locked
	}

	name := strings.TrimSpace(req.DeviceName)
	if name == "" || strings.TrimSpace(req.Platform) == "" {
		// Not a code guess, so it does not count against the limiter. The
		// contract documents only 400/410/429 for this endpoint and core is
		// frozen, so a malformed body reuses PAIRING_CODE_INVALID.
		return nil, core.ErrPairingCodeInvalid()
	}
	name = truncateRunes(name, maxDeviceNameLen)

	if !ValidCodeFormat(req.Code) {
		return nil, p.failed(ctx, req.ClientIP, "malformed", core.ErrPairingCodeInvalid())
	}

	codeHash := HashPairingCode(req.Code)
	pc, err := p.store.PairingCodeByHash(ctx, codeHash)
	if errors.Is(err, core.ErrNotFound()) {
		return nil, p.failed(ctx, req.ClientIP, "unknown", core.ErrPairingCodeInvalid())
	}
	if err != nil {
		return nil, fmt.Errorf("auth: look up pairing code: %w", err)
	}

	// A spent code is a wrong code: it will never pair again, so the client is
	// told 400 and the attempt counts (D-01 single use).
	if pc.Used() {
		p.countCodeAttempt(ctx, codeHash)
		return nil, p.failed(ctx, req.ClientIP, "used", core.ErrPairingCodeInvalid())
	}
	if p.now().UnixMilli() >= pc.ExpiresAtMs {
		p.countCodeAttempt(ctx, codeHash)
		return nil, p.failed(ctx, req.ClientIP, "expired", core.ErrPairingCodeExpired())
	}

	// Burn the code before minting anything. MarkPairingCodeUsed is the
	// single-use guarantee: two devices racing on the same code have exactly one
	// winner, and the loser sees the same 400 as any other spent code.
	if err := p.store.MarkPairingCodeUsed(ctx, codeHash); err != nil {
		if errors.Is(err, store.ErrAlreadyExists) || errors.Is(err, core.ErrNotFound()) {
			return nil, p.failed(ctx, req.ClientIP, "used", core.ErrPairingCodeInvalid())
		}
		return nil, fmt.Errorf("auth: burn pairing code: %w", err)
	}

	token, err := GenerateToken()
	if err != nil {
		return nil, err
	}
	deviceID, err := newID(deviceIDPrefix)
	if err != nil {
		return nil, err
	}
	now := p.now().UnixMilli()
	if err := p.store.InsertDevice(ctx, store.Device{
		DeviceID:       deviceID,
		Name:           name,
		TokenHash:      HashToken(token),
		Platform:       strings.TrimSpace(req.Platform),
		AppVersionCode: req.AppVersionCode,
		CreatedAt:      now,
	}); err != nil {
		return nil, fmt.Errorf("auth: enrol device: %w", err)
	}

	p.clearFailures(req.ClientIP)
	p.log.InfoContext(ctx, "device paired",
		"deviceId", deviceID, "platform", req.Platform, "appVersionCode", req.AppVersionCode,
		"tokenFingerprint", Fingerprint(token))
	return &PairResult{Token: token, DeviceID: deviceID, PairedAtMs: now}, nil
}

// EnsureServerID reads this server's identity from server_meta, generating and
// persisting one on first call. It is stable for the life of the database, which
// is what lets a client tell "my server" from "a server" after a DHCP change
// (D-03).
func EnsureServerID(ctx context.Context, st store.Store) (string, error) {
	id, err := st.Meta(ctx, MetaKeyServerID)
	if err == nil && id != "" {
		return id, nil
	}
	if err != nil && !errors.Is(err, core.ErrNotFound()) {
		return "", fmt.Errorf("auth: read server id: %w", err)
	}
	id, err = newID(serverIDPrefix)
	if err != nil {
		return "", err
	}
	if err := st.SetMeta(ctx, MetaKeyServerID, id); err != nil {
		return "", fmt.Errorf("auth: persist server id: %w", err)
	}
	return id, nil
}

// countCodeAttempt records a wrong guess against the durable per-code counter.
// The lockout itself is IP-keyed and lives in memory; this column is the audit
// trail, so a failure to write it must not change the caller's answer.
func (p *Pairer) countCodeAttempt(ctx context.Context, codeHash string) {
	if _, err := p.store.IncrementPairingCodeAttempts(ctx, codeHash); err != nil {
		p.log.WarnContext(ctx, "recording pairing attempt failed", "error", err)
	}
}

// checkLocked returns the 429 for a client IP that is still locked out, or nil.
func (p *Pairer) checkLocked(ip string) *core.Error {
	now := p.now()
	p.mu.Lock()
	defer p.mu.Unlock()
	st, ok := p.failures[ip]
	if !ok {
		return nil
	}
	if now.Before(st.lockedUntil) {
		return core.ErrPairingRateLimited(st.lockedUntil.Sub(now).Milliseconds())
	}
	if !st.lockedUntil.IsZero() {
		// The lockout has run out; the client starts over with a clean slate.
		delete(p.failures, ip)
	}
	return nil
}

// failed records a failed redemption and returns the error the client should
// see: the 429 once this IP has spent its last attempt, otherwise want.
func (p *Pairer) failed(ctx context.Context, ip, reason string, want *core.Error) *core.Error {
	now := p.now()

	p.mu.Lock()
	p.sweepFailuresLocked(now)
	st, ok := p.failures[ip]
	if !ok {
		st = &failureState{}
		p.failures[ip] = st
	}
	st.count++
	st.lastFailure = now
	count := st.count
	locked := count >= p.maxAttempts
	if locked {
		st.lockedUntil = now.Add(p.lockout)
	}
	retryAfter := p.lockout.Milliseconds()
	p.mu.Unlock()

	p.log.WarnContext(ctx, "pairing attempt rejected",
		"reason", reason, "remote", ip, "attempts", count, "lockedOut", locked)
	if locked {
		return core.ErrPairingRateLimited(retryAfter)
	}
	return want
}

// clearFailures wipes an IP's record after a successful pairing.
func (p *Pairer) clearFailures(ip string) {
	p.mu.Lock()
	delete(p.failures, ip)
	p.mu.Unlock()
}

// sweepFailuresLocked drops records that can no longer affect anyone, keeping
// the map from growing without bound on a server that is scanned from many
// addresses. The caller holds p.mu.
func (p *Pairer) sweepFailuresLocked(now time.Time) {
	cutoff := now.Add(-2 * p.lockout)
	for ip, st := range p.failures {
		if st.lastFailure.Before(cutoff) && now.After(st.lockedUntil) {
			delete(p.failures, ip)
		}
	}
}

// truncateRunes cuts s to at most n runes, never splitting one.
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

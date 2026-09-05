package auth_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FancyFunction/homesink/backend/internal/auth"
	"github.com/FancyFunction/homesink/backend/internal/core"
	"github.com/FancyFunction/homesink/backend/internal/store"
	"github.com/FancyFunction/homesink/backend/internal/testutil"
)

const testIP = "192.0.2.10"

func newPairer(t *testing.T, clock *testClock, opts auth.PairerOptions) (*auth.Pairer, *testutil.MemStore) {
	t.Helper()
	st := testutil.NewMemStore()
	st.Now = func() int64 { return clock.Now().UnixMilli() }
	opts.Now = clock.Now
	return auth.NewPairer(st, opts), st
}

func pairRequest(code string) auth.PairRequest {
	return auth.PairRequest{
		Code:           code,
		DeviceName:     "Pixel 8",
		Platform:       "android",
		AppVersionCode: 14,
		ClientIP:       testIP,
	}
}

// codeError unwraps the *core.Error a failed redemption must carry.
func codeError(t *testing.T, err error) *core.Error {
	t.Helper()
	var cerr *core.Error
	if !errors.As(err, &cerr) {
		t.Fatalf("want a *core.Error, got %T: %v", err, err)
	}
	return cerr
}

func TestGeneratePairingCode_IsSixDigitsAndSpansTheWholeSpace(t *testing.T) {
	// A modulo fold of a 32-bit draw into 10^6 is far too small a bias to detect
	// statistically, so what is asserted here is the shape the rejection
	// sampling must preserve: six ASCII digits, leading zeros kept, and every
	// digit reachable in every position.
	const draws = 5000
	seen := map[string]struct{}{}
	var perPosition [auth.CodeDigits]map[byte]struct{}
	for i := range perPosition {
		perPosition[i] = map[byte]struct{}{}
	}

	for range draws {
		code, err := auth.GeneratePairingCode()
		if err != nil {
			t.Fatalf("GeneratePairingCode: %v", err)
		}
		if !auth.ValidCodeFormat(code) {
			t.Fatalf("code %q is not %d ASCII digits", code, auth.CodeDigits)
		}
		seen[code] = struct{}{}
		for i := range auth.CodeDigits {
			perPosition[i][code[i]] = struct{}{}
		}
	}

	if len(seen) < draws*9/10 {
		t.Errorf("only %d distinct codes in %d draws; the generator is not spanning the space", len(seen), draws)
	}
	for i, digits := range perPosition {
		if len(digits) != 10 {
			t.Errorf("position %d produced only %d distinct digits, want all 10", i, len(digits))
		}
	}
}

func TestIssueCode_StoresOnlyTheHashWithATTL(t *testing.T) {
	clock := newClock()
	p, st := newPairer(t, clock, auth.PairerOptions{})
	ctx := context.Background()

	code, err := p.IssueCode(ctx)
	if err != nil {
		t.Fatalf("IssueCode: %v", err)
	}
	mustNotBeEmpty(t, "code", code.Value)

	if _, err := st.PairingCodeByHash(ctx, code.Value); !errors.Is(err, core.ErrNotFound()) {
		t.Error("the cleartext code was used as a storage key; only its hash may be persisted")
	}
	pc, err := st.PairingCodeByHash(ctx, auth.HashPairingCode(code.Value))
	if err != nil {
		t.Fatalf("PairingCodeByHash: %v", err)
	}
	if want := clock.Now().Add(auth.DefaultCodeTTL).UnixMilli(); pc.ExpiresAtMs != want {
		t.Errorf("expires_at = %d, want %d (10 min TTL)", pc.ExpiresAtMs, want)
	}
	if code.ExpiresAtMs != pc.ExpiresAtMs {
		t.Errorf("returned expiry %d does not match the stored one %d", code.ExpiresAtMs, pc.ExpiresAtMs)
	}
}

func TestIssueCode_SweepsCodesThatAlreadyExpired(t *testing.T) {
	clock := newClock()
	p, st := newPairer(t, clock, auth.PairerOptions{})
	ctx := context.Background()

	stale, err := p.IssueCode(ctx)
	if err != nil {
		t.Fatalf("IssueCode: %v", err)
	}
	clock.Advance(auth.DefaultCodeTTL + time.Minute)
	if _, err := p.IssueCode(ctx); err != nil {
		t.Fatalf("IssueCode: %v", err)
	}

	_, err = st.PairingCodeByHash(ctx, auth.HashPairingCode(stale.Value))
	if !errors.Is(err, core.ErrNotFound()) {
		t.Errorf("expired code survived the sweep: err = %v", err)
	}
}

func TestPair_SucceedsOnceAndStoresOnlyTheTokenHash(t *testing.T) {
	clock := newClock()
	p, st := newPairer(t, clock, auth.PairerOptions{})
	ctx := context.Background()

	code, err := p.IssueCode(ctx)
	if err != nil {
		t.Fatalf("IssueCode: %v", err)
	}
	res, err := p.Pair(ctx, pairRequest(code.Value))
	if err != nil {
		t.Fatalf("Pair: %v", err)
	}
	if !auth.ValidTokenFormat(res.Token) {
		t.Errorf("token %q is not hs_ + 43 base64url characters", res.Token)
	}
	if !strings.HasPrefix(res.DeviceID, "dev_") {
		t.Errorf("deviceId = %q, want a dev_ prefix", res.DeviceID)
	}

	d, err := st.DeviceByID(ctx, res.DeviceID)
	if err != nil {
		t.Fatalf("DeviceByID: %v", err)
	}
	if d.TokenHash == res.Token {
		t.Fatal("the cleartext token was stored in devices.token_hash")
	}
	if !auth.VerifyToken(res.Token, d.TokenHash) {
		t.Error("the stored hash does not verify against the issued token")
	}
	if d.Name != "Pixel 8" || d.Platform != "android" || d.AppVersionCode != 14 {
		t.Errorf("device row = %+v, want the paired device's own details", d)
	}
}

// --- Acceptance: a used code → 400 ---

func TestPair_UsedCodeIsRejectedAs400(t *testing.T) {
	clock := newClock()
	p, _ := newPairer(t, clock, auth.PairerOptions{})
	ctx := context.Background()

	code, err := p.IssueCode(ctx)
	if err != nil {
		t.Fatalf("IssueCode: %v", err)
	}
	if _, err := p.Pair(ctx, pairRequest(code.Value)); err != nil {
		t.Fatalf("first Pair: %v", err)
	}

	_, err = p.Pair(ctx, pairRequest(code.Value))
	cerr := codeError(t, err)
	if cerr.Code != core.CodePairingCodeInvalid || cerr.HTTPStatus != 400 {
		t.Errorf("second redemption = %s/%d, want PAIRING_CODE_INVALID/400", cerr.Code, cerr.HTTPStatus)
	}
}

// --- Acceptance: expired code → 410 ---

func TestPair_ExpiredCodeIsRejectedAs410(t *testing.T) {
	clock := newClock()
	p, _ := newPairer(t, clock, auth.PairerOptions{})
	ctx := context.Background()

	code, err := p.IssueCode(ctx)
	if err != nil {
		t.Fatalf("IssueCode: %v", err)
	}
	clock.Advance(auth.DefaultCodeTTL)

	_, err = p.Pair(ctx, pairRequest(code.Value))
	cerr := codeError(t, err)
	if cerr.Code != core.CodePairingCodeExpired || cerr.HTTPStatus != 410 {
		t.Errorf("expired redemption = %s/%d, want PAIRING_CODE_EXPIRED/410", cerr.Code, cerr.HTTPStatus)
	}
}

// --- Acceptance: wrong code 5× → 429 with retryAfterMs ---

func TestPair_FiveWrongCodesAreRateLimitedWithRetryAfterMs(t *testing.T) {
	clock := newClock()
	p, _ := newPairer(t, clock, auth.PairerOptions{})
	ctx := context.Background()

	if _, err := p.IssueCode(ctx); err != nil {
		t.Fatalf("IssueCode: %v", err)
	}

	// The first four wrong guesses are ordinary rejections.
	for attempt := 1; attempt < auth.DefaultMaxAttempts; attempt++ {
		_, err := p.Pair(ctx, pairRequest("000000"))
		cerr := codeError(t, err)
		if cerr.Code != core.CodePairingCodeInvalid {
			t.Fatalf("attempt %d = %s, want PAIRING_CODE_INVALID", attempt, cerr.Code)
		}
	}

	// The fifth spends the allowance and locks the client out.
	_, err := p.Pair(ctx, pairRequest("000000"))
	cerr := codeError(t, err)
	if cerr.Code != core.CodePairingRateLimited || cerr.HTTPStatus != 429 {
		t.Fatalf("fifth attempt = %s/%d, want PAIRING_RATE_LIMITED/429", cerr.Code, cerr.HTTPStatus)
	}
	retry, ok := cerr.Details["retryAfterMs"].(int64)
	if !ok || retry <= 0 {
		t.Fatalf("details.retryAfterMs = %#v, want a positive int64", cerr.Details["retryAfterMs"])
	}
	if retry > auth.DefaultLockout.Milliseconds() {
		t.Errorf("retryAfterMs = %d, want at most the %s lockout", retry, auth.DefaultLockout)
	}
}

func TestPair_LockedOutClientIsRefusedEvenWithTheRightCode(t *testing.T) {
	clock := newClock()
	p, _ := newPairer(t, clock, auth.PairerOptions{})
	ctx := context.Background()

	code, err := p.IssueCode(ctx)
	if err != nil {
		t.Fatalf("IssueCode: %v", err)
	}
	for range auth.DefaultMaxAttempts {
		if _, err := p.Pair(ctx, pairRequest("000000")); err == nil {
			t.Fatal("a wrong code was accepted")
		}
	}

	_, err = p.Pair(ctx, pairRequest(code.Value))
	if cerr := codeError(t, err); cerr.Code != core.CodePairingRateLimited {
		t.Errorf("during lockout = %s, want PAIRING_RATE_LIMITED", cerr.Code)
	}

	// Once the window passes the client starts over. The original code has
	// outlived its 10-minute TTL by then, so pairing resumes with a fresh one.
	clock.Advance(auth.DefaultLockout)
	fresh, err := p.IssueCode(ctx)
	if err != nil {
		t.Fatalf("IssueCode: %v", err)
	}
	if _, err := p.Pair(ctx, pairRequest(fresh.Value)); err != nil {
		t.Errorf("after the lockout expired: %v", err)
	}
}

func TestPair_LockoutIsKeyedByClientIP(t *testing.T) {
	clock := newClock()
	p, _ := newPairer(t, clock, auth.PairerOptions{})
	ctx := context.Background()

	code, err := p.IssueCode(ctx)
	if err != nil {
		t.Fatalf("IssueCode: %v", err)
	}
	for range auth.DefaultMaxAttempts {
		if _, err := p.Pair(ctx, pairRequest("000000")); err == nil {
			t.Fatal("a wrong code was accepted")
		}
	}

	other := pairRequest(code.Value)
	other.ClientIP = "192.0.2.99"
	if _, err := p.Pair(ctx, other); err != nil {
		t.Errorf("a second phone on another address was locked out too: %v", err)
	}
}

func TestPair_RecordsWrongGuessesAgainstTheCodeRow(t *testing.T) {
	clock := newClock()
	p, st := newPairer(t, clock, auth.PairerOptions{})
	ctx := context.Background()

	code, err := p.IssueCode(ctx)
	if err != nil {
		t.Fatalf("IssueCode: %v", err)
	}
	clock.Advance(auth.DefaultCodeTTL)
	if _, err := p.Pair(ctx, pairRequest(code.Value)); err == nil {
		t.Fatal("an expired code was accepted")
	}

	pc, err := st.PairingCodeByHash(ctx, auth.HashPairingCode(code.Value))
	if err != nil {
		t.Fatalf("PairingCodeByHash: %v", err)
	}
	if pc.Attempts != 1 {
		t.Errorf("attempts = %d, want 1", pc.Attempts)
	}
}

func TestPair_ConcurrentRedemptionOfOneCodeHasExactlyOneWinner(t *testing.T) {
	clock := newClock()
	p, _ := newPairer(t, clock, auth.PairerOptions{})
	ctx := context.Background()

	code, err := p.IssueCode(ctx)
	if err != nil {
		t.Fatalf("IssueCode: %v", err)
	}

	const racers = 8
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		winners int
	)
	for i := range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := pairRequest(code.Value)
			req.ClientIP = "198.51.100." + string(rune('0'+i))
			if _, err := p.Pair(ctx, req); err == nil {
				mu.Lock()
				winners++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if winners != 1 {
		t.Errorf("%d of %d concurrent redemptions succeeded, want exactly 1 (single use, D-01)", winners, racers)
	}
}

func TestPair_RejectsAMalformedRequestWithoutSpendingAnAttempt(t *testing.T) {
	clock := newClock()
	p, _ := newPairer(t, clock, auth.PairerOptions{})
	ctx := context.Background()

	code, err := p.IssueCode(ctx)
	if err != nil {
		t.Fatalf("IssueCode: %v", err)
	}
	for range auth.DefaultMaxAttempts + 2 {
		req := pairRequest(code.Value)
		req.DeviceName = "  "
		if _, err := p.Pair(ctx, req); err == nil {
			t.Fatal("a request without a device name was accepted")
		}
	}

	if _, err := p.Pair(ctx, pairRequest(code.Value)); err != nil {
		t.Errorf("malformed requests counted towards the lockout: %v", err)
	}
}

func TestPair_TruncatesAnOverlongDeviceName(t *testing.T) {
	clock := newClock()
	p, st := newPairer(t, clock, auth.PairerOptions{})
	ctx := context.Background()

	code, err := p.IssueCode(ctx)
	if err != nil {
		t.Fatalf("IssueCode: %v", err)
	}
	req := pairRequest(code.Value)
	req.DeviceName = strings.Repeat("ä", 100)

	res, err := p.Pair(ctx, req)
	if err != nil {
		t.Fatalf("Pair: %v", err)
	}
	d, err := st.DeviceByID(ctx, res.DeviceID)
	if err != nil {
		t.Fatalf("DeviceByID: %v", err)
	}
	if got := len([]rune(d.Name)); got != 64 {
		t.Errorf("stored name is %d runes, want it truncated to the contract's 64", got)
	}
}

func TestEnsureServerID_IsGeneratedOnceAndReused(t *testing.T) {
	st := testutil.NewMemStore()
	ctx := context.Background()

	first, err := auth.EnsureServerID(ctx, st)
	if err != nil {
		t.Fatalf("EnsureServerID: %v", err)
	}
	if !strings.HasPrefix(first, "srv_") {
		t.Errorf("serverId = %q, want a srv_ prefix", first)
	}

	second, err := auth.EnsureServerID(ctx, st)
	if err != nil {
		t.Fatalf("EnsureServerID: %v", err)
	}
	if second != first {
		t.Errorf("serverId changed between calls: %q then %q", first, second)
	}

	stored, err := st.Meta(ctx, auth.MetaKeyServerID)
	if err != nil || stored != first {
		t.Errorf("server_meta[%s] = %q, %v; want %q", auth.MetaKeyServerID, stored, err, first)
	}
}

// storeErrorsOnInsert is a store whose device insert always fails, to prove an
// infrastructure failure never reaches the client as a pairing verdict.
type storeErrorsOnInsert struct {
	store.Store
}

func (s storeErrorsOnInsert) InsertDevice(context.Context, store.Device) error {
	return errors.New("disk on fire")
}

func TestPair_InfrastructureFailureIsNotACoreError(t *testing.T) {
	clock := newClock()
	base := testutil.NewMemStore()
	base.Now = func() int64 { return clock.Now().UnixMilli() }
	p := auth.NewPairer(storeErrorsOnInsert{Store: base}, auth.PairerOptions{Now: clock.Now})
	ctx := context.Background()

	code, err := p.IssueCode(ctx)
	if err != nil {
		t.Fatalf("IssueCode: %v", err)
	}
	_, err = p.Pair(ctx, pairRequest(code.Value))
	if err == nil {
		t.Fatal("Pair succeeded despite a failing store")
	}
	var cerr *core.Error
	if errors.As(err, &cerr) {
		t.Errorf("store failure surfaced as the wire error %s; it must stay a plain error", cerr.Code)
	}
}

package auth_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/FancyFunction/homesink/backend/internal/auth"
	"github.com/FancyFunction/homesink/backend/internal/core"
	"github.com/FancyFunction/homesink/backend/internal/store"
	"github.com/FancyFunction/homesink/backend/internal/testutil"
)

// protectedHandler records whether the request got past Require, and which
// device it carried.
type protectedHandler struct {
	called bool
	device *store.Device
}

func (h *protectedHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.called = true
	h.device, _ = auth.DeviceFromContext(r.Context())
	w.WriteHeader(http.StatusOK)
}

// pairedDevice enrols one device and returns its token.
func pairedDevice(t *testing.T, p *auth.Pairer, st *testutil.MemStore) (string, string) {
	t.Helper()
	ctx := context.Background()
	code, err := p.IssueCode(ctx)
	if err != nil {
		t.Fatalf("IssueCode: %v", err)
	}
	res, err := p.Pair(ctx, pairRequest(code.Value))
	if err != nil {
		t.Fatalf("Pair: %v", err)
	}
	if _, err := st.DeviceByID(ctx, res.DeviceID); err != nil {
		t.Fatalf("DeviceByID: %v", err)
	}
	return res.Token, res.DeviceID
}

func authRequest(token string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/v1/devices", nil)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	r.Header.Set("X-Homesink-Client", "14/1.0.0")
	return r
}

func errorCode(t *testing.T, body []byte) string {
	t.Helper()
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decoding the error envelope: %v (body %q)", err, body)
	}
	return env.Error.Code
}

// --- Acceptance: revoked token → 401 on the next request ---

func TestRequire_RevokedTokenIs401OnTheVeryNextRequest(t *testing.T) {
	clock := newClock()
	p, st := newPairer(t, clock, auth.PairerOptions{})
	a := auth.NewAuthenticator(st, auth.AuthenticatorOptions{Now: clock.Now})
	token, deviceID := pairedDevice(t, p, st)

	// The token works before revocation.
	before := &protectedHandler{}
	rec := httptest.NewRecorder()
	a.Require(before).ServeHTTP(rec, authRequest(token))
	if rec.Code != http.StatusOK || !before.called {
		t.Fatalf("a live token was refused: status %d", rec.Code)
	}

	if err := st.RevokeDevice(context.Background(), deviceID); err != nil {
		t.Fatalf("RevokeDevice: %v", err)
	}

	after := &protectedHandler{}
	rec = httptest.NewRecorder()
	a.Require(after).ServeHTTP(rec, authRequest(token))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status after revocation = %d, want 401", rec.Code)
	}
	if got := errorCode(t, rec.Body.Bytes()); got != core.CodeUnauthorized {
		t.Errorf("error code = %s, want UNAUTHORIZED", got)
	}
	if after.called {
		t.Error("a revoked token reached the protected handler")
	}
}

func TestRequire_RejectsMissingMalformedAndUnknownTokens(t *testing.T) {
	clock := newClock()
	_, st := newPairer(t, clock, auth.PairerOptions{})
	a := auth.NewAuthenticator(st, auth.AuthenticatorOptions{Now: clock.Now})

	unknown, err := auth.GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	tests := []struct {
		name    string
		prepare func(*http.Request)
	}{
		{"no header", func(*http.Request) {}},
		{"wrong scheme", func(r *http.Request) { r.Header.Set("Authorization", "Basic "+unknown) }},
		{"empty bearer", func(r *http.Request) { r.Header.Set("Authorization", "Bearer ") }},
		{"malformed token", func(r *http.Request) { r.Header.Set("Authorization", "Bearer nonsense") }},
		{"unknown token", func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+unknown) }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := &protectedHandler{}
			r := httptest.NewRequest(http.MethodGet, "/v1/devices", nil)
			tc.prepare(r)
			rec := httptest.NewRecorder()
			a.Require(h).ServeHTTP(rec, r)

			if rec.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", rec.Code)
			}
			if h.called {
				t.Error("the request reached the protected handler")
			}
		})
	}
}

func TestRequire_AcceptsALiveTokenAndExposesTheDevice(t *testing.T) {
	clock := newClock()
	p, st := newPairer(t, clock, auth.PairerOptions{})
	a := auth.NewAuthenticator(st, auth.AuthenticatorOptions{Now: clock.Now})
	token, deviceID := pairedDevice(t, p, st)

	h := &protectedHandler{}
	rec := httptest.NewRecorder()
	a.Require(h).ServeHTTP(rec, authRequest(token))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if h.device == nil || h.device.DeviceID != deviceID {
		t.Fatalf("handler saw device %+v, want %s", h.device, deviceID)
	}
	if h.device.TokenHash == token {
		t.Error("the cleartext token was handed to the handler")
	}
}

func TestRequire_TouchesTheDeviceAtMostOncePerMinute(t *testing.T) {
	clock := newClock()
	p, st := newPairer(t, clock, auth.PairerOptions{})
	a := auth.NewAuthenticator(st, auth.AuthenticatorOptions{Now: clock.Now})
	token, deviceID := pairedDevice(t, p, st)
	ctx := context.Background()

	serve := func() {
		rec := httptest.NewRecorder()
		a.Require(&protectedHandler{}).ServeHTTP(rec, authRequest(token))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
	}

	serve()
	first, err := st.DeviceByID(ctx, deviceID)
	if err != nil {
		t.Fatalf("DeviceByID: %v", err)
	}
	if first.LastSeenAt != clock.Now().UnixMilli() {
		t.Fatalf("last_seen_at = %d, want the first request's instant", first.LastSeenAt)
	}

	// Ten more requests inside the same minute must not write again.
	clock.Advance(30 * time.Second)
	for range 10 {
		serve()
	}
	second, err := st.DeviceByID(ctx, deviceID)
	if err != nil {
		t.Fatalf("DeviceByID: %v", err)
	}
	if second.LastSeenAt != first.LastSeenAt {
		t.Errorf("last_seen_at moved to %d inside the throttle window, want it unchanged at %d",
			second.LastSeenAt, first.LastSeenAt)
	}

	// Past the interval the next request refreshes it again.
	clock.Advance(auth.DefaultTouchInterval)
	serve()
	third, err := st.DeviceByID(ctx, deviceID)
	if err != nil {
		t.Fatalf("DeviceByID: %v", err)
	}
	if third.LastSeenAt != clock.Now().UnixMilli() {
		t.Errorf("last_seen_at = %d, want %d once the interval passed",
			third.LastSeenAt, clock.Now().UnixMilli())
	}
	if third.AppVersionCode != 14 {
		t.Errorf("app_version_code = %d, want 14 from X-Homesink-Client", third.AppVersionCode)
	}
}

func TestClientVersionCode(t *testing.T) {
	tests := []struct {
		header string
		want   int64
	}{
		{"", 0},
		{"14/1.0.0", 14},
		{" 14 / 1.0.0", 14},
		{"14", 14},
		{"nonsense", 0},
		{"-3/1.0.0", 0},
	}
	for _, tc := range tests {
		t.Run(tc.header, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/v1/devices", nil)
			if tc.header != "" {
				r.Header.Set("X-Homesink-Client", tc.header)
			}
			if got := auth.ClientVersionCode(r); got != tc.want {
				t.Errorf("ClientVersionCode(%q) = %d, want %d", tc.header, got, tc.want)
			}
		})
	}
}

// --- Acceptance: the token is never written to a log at any level ---

func TestSecrets_AreNeverWrittenToALogAtAnyLevel(t *testing.T) {
	clock := newClock()
	logs := newLogBuffer()
	st := testutil.NewMemStore()
	st.Now = func() int64 { return clock.Now().UnixMilli() }
	p := auth.NewPairer(st, auth.PairerOptions{Now: clock.Now, Logger: logs.logger()})
	a := auth.NewAuthenticator(st, auth.AuthenticatorOptions{Now: clock.Now, Logger: logs.logger()})
	ctx := context.Background()

	// Exercise every path that has a secret in hand: issuing, a wrong guess, a
	// successful pairing, an authenticated request, and a rejected one.
	code, err := p.IssueCode(ctx)
	if err != nil {
		t.Fatalf("IssueCode: %v", err)
	}
	if _, err := p.Pair(ctx, pairRequest("000000")); err == nil {
		t.Fatal("a wrong code was accepted")
	}
	res, err := p.Pair(ctx, pairRequest(code.Value))
	if err != nil {
		t.Fatalf("Pair: %v", err)
	}

	rec := httptest.NewRecorder()
	a.Require(&protectedHandler{}).ServeHTTP(rec, authRequest(res.Token))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	stranger, err := auth.GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	rec = httptest.NewRecorder()
	a.Require(&protectedHandler{}).ServeHTTP(rec, authRequest(stranger))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}

	if logs.String() == "" {
		t.Fatal("nothing was logged at all; the assertion below would be vacuous")
	}
	for _, secret := range []struct{ what, value string }{
		{"the pairing code", code.Value},
		{"the issued token", res.Token},
		{"the presented but unknown token", stranger},
		{"the stored token hash", auth.HashToken(res.Token)},
	} {
		if logs.contains(secret.value) {
			t.Errorf("%s appears in the logs:\n%s", secret.what, logs.String())
		}
	}
}

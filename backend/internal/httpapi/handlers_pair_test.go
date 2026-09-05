package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FancyFunction/homesink/backend/internal/auth"
	"github.com/FancyFunction/homesink/backend/internal/core"
	"github.com/FancyFunction/homesink/backend/internal/httpapi"
	"github.com/FancyFunction/homesink/backend/internal/testutil"
)

// pairClock is a manually advanced clock shared by a test's Pairer and
// Authenticator, so code TTLs and lockout windows need no sleeps.
type pairClock struct {
	mu sync.Mutex
	t  time.Time
}

func newPairClock() *pairClock {
	return &pairClock{t: time.Date(2025, 8, 17, 14, 32, 1, 0, time.UTC)}
}

func (c *pairClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *pairClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// pairFixture is a server with the pairing routes registered over a MemStore.
type pairFixture struct {
	srv    *httpapi.Server
	pairer *auth.Pairer
	clock  *pairClock
}

func newPairFixture(t *testing.T) *pairFixture {
	t.Helper()
	clock := newPairClock()
	st := testutil.NewMemStore()
	st.Now = func() int64 { return clock.Now().UnixMilli() }

	pairer := auth.NewPairer(st, auth.PairerOptions{Now: clock.Now})
	srv := httpapi.New(testConfig(t), discardLogger())
	httpapi.RegisterPairing(srv, httpapi.PairingDeps{
		Pairer:        pairer,
		Auth:          auth.NewAuthenticator(st, auth.AuthenticatorOptions{Now: clock.Now}),
		Store:         st,
		ServerID:      "srv_a1b2c3d4e5f60718",
		ServerName:    "Wohnzimmer-NAS",
		TLSSPKISHA256: "aGVsbG8td29ybGQtc3BraS1zaGEyNTYtYmFzZTY0LXZhbHVlPT0=",
		Now:           clock.Now,
	})
	return &pairFixture{srv: srv, pairer: pairer, clock: clock}
}

// issueCode mints a live pairing code.
func (f *pairFixture) issueCode(t *testing.T) string {
	t.Helper()
	code, err := f.pairer.IssueCode(context.Background())
	if err != nil {
		t.Fatalf("IssueCode: %v", err)
	}
	return code.Value
}

// postPair sends a pairing request from remoteIP.
func (f *pairFixture) postPair(t *testing.T, remoteIP, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/v1/pair", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.RemoteAddr = remoteIP + ":51000"
	rec := httptest.NewRecorder()
	f.srv.ServeHTTP(rec, r)
	return rec
}

// pairWithCode runs a complete, well-formed pairing exchange.
func (f *pairFixture) pairWithCode(t *testing.T, remoteIP, code string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"code":           code,
		"deviceName":     "Pixel 8",
		"platform":       "android",
		"appVersionCode": 14,
	})
	if err != nil {
		t.Fatalf("marshalling the request: %v", err)
	}
	return f.postPair(t, remoteIP, string(body))
}

// authed sends an authenticated request through the full middleware chain.
func (f *pairFixture) authed(t *testing.T, method, path, token string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, nil)
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("X-Homesink-Client", "14/1.0.0")
	r.RemoteAddr = "192.0.2.10:51000"
	rec := httptest.NewRecorder()
	f.srv.ServeHTTP(rec, r)
	return rec
}

// wireError is the envelope of 02-API.md §2.
type wireError struct {
	Error struct {
		Code      string         `json:"code"`
		Message   string         `json:"message"`
		Retryable bool           `json:"retryable"`
		Details   map[string]any `json:"details"`
	} `json:"error"`
}

func decodeError(t *testing.T, rec *httptest.ResponseRecorder) wireError {
	t.Helper()
	var e wireError
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("decoding the error envelope: %v (body %q)", err, rec.Body.String())
	}
	return e
}

// pairSuccess mirrors the contract's PairResponse.
type pairSuccess struct {
	Token         string `json:"token"`
	DeviceID      string `json:"deviceId"`
	ServerID      string `json:"serverId"`
	ServerName    string `json:"serverName"`
	TLSSpkiSha256 string `json:"tlsSpkiSha256"`
	ServerTimeMs  int64  `json:"serverTimeMs"`
}

func decodePairSuccess(t *testing.T, rec *httptest.ResponseRecorder) pairSuccess {
	t.Helper()
	var res pairSuccess
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decoding the pair response: %v (body %q)", err, rec.Body.String())
	}
	return res
}

func TestPostPair_ReturnsTheContractResponseOn201(t *testing.T) {
	f := newPairFixture(t)
	rec := f.pairWithCode(t, "192.0.2.10", f.issueCode(t))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body %q)", rec.Code, rec.Body.String())
	}
	res := decodePairSuccess(t, rec)
	if !auth.ValidTokenFormat(res.Token) {
		t.Errorf("token = %q, want hs_ + 43 base64url characters", res.Token)
	}
	if !strings.HasPrefix(res.DeviceID, "dev_") {
		t.Errorf("deviceId = %q, want a dev_ prefix", res.DeviceID)
	}
	if res.ServerID != "srv_a1b2c3d4e5f60718" || res.ServerName != "Wohnzimmer-NAS" {
		t.Errorf("server identity = %q/%q, want the configured one", res.ServerID, res.ServerName)
	}
	if res.TLSSpkiSha256 == "" {
		t.Error("tlsSpkiSha256 is empty; the client has nothing to pin (D-02)")
	}
	if res.ServerTimeMs != f.clock.Now().UnixMilli() {
		t.Errorf("serverTimeMs = %d, want the server clock %d", res.ServerTimeMs, f.clock.Now().UnixMilli())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}

// --- Acceptance: a used code → 400 ---

func TestPostPair_UsedCodeReturns400(t *testing.T) {
	f := newPairFixture(t)
	code := f.issueCode(t)

	if rec := f.pairWithCode(t, "192.0.2.10", code); rec.Code != http.StatusCreated {
		t.Fatalf("first pairing: status = %d, want 201", rec.Code)
	}

	rec := f.pairWithCode(t, "192.0.2.11", code)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %q)", rec.Code, rec.Body.String())
	}
	if got := decodeError(t, rec).Error.Code; got != core.CodePairingCodeInvalid {
		t.Errorf("error code = %s, want PAIRING_CODE_INVALID", got)
	}
}

// --- Acceptance: an expired code → 410 ---

func TestPostPair_ExpiredCodeReturns410(t *testing.T) {
	f := newPairFixture(t)
	code := f.issueCode(t)

	f.clock.Advance(auth.DefaultCodeTTL)

	rec := f.pairWithCode(t, "192.0.2.10", code)
	if rec.Code != http.StatusGone {
		t.Fatalf("status = %d, want 410 (body %q)", rec.Code, rec.Body.String())
	}
	if got := decodeError(t, rec).Error.Code; got != core.CodePairingCodeExpired {
		t.Errorf("error code = %s, want PAIRING_CODE_EXPIRED", got)
	}
}

// --- Acceptance: a wrong code 5× → 429 with retryAfterMs ---

func TestPostPair_FiveWrongCodesReturn429WithRetryAfterMs(t *testing.T) {
	f := newPairFixture(t)
	f.issueCode(t)

	for attempt := 1; attempt < auth.DefaultMaxAttempts; attempt++ {
		rec := f.pairWithCode(t, "192.0.2.10", "000000")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("attempt %d: status = %d, want 400 (body %q)", attempt, rec.Code, rec.Body.String())
		}
	}

	rec := f.pairWithCode(t, "192.0.2.10", "000000")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("fifth attempt: status = %d, want 429 (body %q)", rec.Code, rec.Body.String())
	}
	e := decodeError(t, rec)
	if e.Error.Code != core.CodePairingRateLimited {
		t.Errorf("error code = %s, want PAIRING_RATE_LIMITED", e.Error.Code)
	}
	if !e.Error.Retryable {
		t.Error("the rate-limit error is not marked retryable")
	}
	retry, ok := e.Error.Details["retryAfterMs"].(float64)
	if !ok || retry <= 0 {
		t.Fatalf("details.retryAfterMs = %#v, want a positive number", e.Error.Details["retryAfterMs"])
	}
	if int64(retry) != auth.DefaultLockout.Milliseconds() {
		t.Errorf("retryAfterMs = %d, want the %s lockout", int64(retry), auth.DefaultLockout)
	}
}

func TestPostPair_MalformedBodyIs400(t *testing.T) {
	f := newPairFixture(t)
	tests := []struct {
		name string
		body string
	}{
		{"not json", "{"},
		{"wrong type", `{"code":123456,"deviceName":"P","platform":"android"}`},
		{"missing device name", `{"code":"123456","platform":"android","appVersionCode":1}`},
		{"code is not six digits", `{"code":"12","deviceName":"P","platform":"android","appVersionCode":1}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := f.postPair(t, "192.0.2.20", tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400 (body %q)", rec.Code, rec.Body.String())
			}
			if got := decodeError(t, rec).Error.Code; got != core.CodePairingCodeInvalid {
				t.Errorf("error code = %s, want PAIRING_CODE_INVALID", got)
			}
		})
	}
}

func TestPostPair_RejectsAnOversizedBody(t *testing.T) {
	f := newPairFixture(t)
	rec := f.postPair(t, "192.0.2.21", `{"code":"123456","deviceName":"`+strings.Repeat("x", 8192)+`"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a body past the cap", rec.Code)
	}
}

func TestDevices_ListRequiresAuthAndFlagsTheCaller(t *testing.T) {
	f := newPairFixture(t)

	if rec := f.authed(t, http.MethodGet, "/v1/devices", "hs_not-a-real-token"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated list: status = %d, want 401", rec.Code)
	}

	first := decodePairSuccess(t, f.pairWithCode(t, "192.0.2.10", f.issueCode(t)))
	f.clock.Advance(time.Minute)
	second := decodePairSuccess(t, f.pairWithCode(t, "192.0.2.11", f.issueCode(t)))

	rec := f.authed(t, http.MethodGet, "/v1/devices", first.Token)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}

	var devices []struct {
		DeviceID       string `json:"deviceId"`
		Name           string `json:"name"`
		LastSeenMs     int64  `json:"lastSeenMs"`
		AppVersionCode int64  `json:"appVersionCode"`
		Current        bool   `json:"current"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &devices); err != nil {
		t.Fatalf("decoding the device list: %v (body %q)", err, rec.Body.String())
	}
	if len(devices) != 2 {
		t.Fatalf("got %d devices, want 2", len(devices))
	}
	for _, d := range devices {
		wantCurrent := d.DeviceID == first.DeviceID
		if d.Current != wantCurrent {
			t.Errorf("device %s: current = %v, want %v", d.DeviceID, d.Current, wantCurrent)
		}
		if d.Name != "Pixel 8" || d.AppVersionCode != 14 {
			t.Errorf("device %s = %+v, want the paired details", d.DeviceID, d)
		}
	}
	if devices[0].DeviceID != first.DeviceID || devices[1].DeviceID != second.DeviceID {
		t.Errorf("devices listed as %s, %s; want the older one first",
			devices[0].DeviceID, devices[1].DeviceID)
	}
}

func TestDevices_RevokeIsIdempotentAnd404sOnAnUnknownID(t *testing.T) {
	f := newPairFixture(t)
	keeper := decodePairSuccess(t, f.pairWithCode(t, "192.0.2.10", f.issueCode(t)))
	doomed := decodePairSuccess(t, f.pairWithCode(t, "192.0.2.11", f.issueCode(t)))

	for range 2 {
		rec := f.authed(t, http.MethodDelete, "/v1/devices/"+doomed.DeviceID, keeper.Token)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204 (body %q)", rec.Code, rec.Body.String())
		}
		if rec.Body.Len() != 0 {
			t.Errorf("204 carried a body: %q", rec.Body.String())
		}
	}

	rec := f.authed(t, http.MethodDelete, "/v1/devices/dev_doesnotexist00", keeper.Token)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
	if got := decodeError(t, rec).Error.Code; got != core.CodeNotFound {
		t.Errorf("error code = %s, want NOT_FOUND", got)
	}
}

// --- Acceptance: a revoked token → 401 on the next request ---

func TestDevices_RevokedTokenIs401OnTheNextRequest(t *testing.T) {
	f := newPairFixture(t)
	victim := decodePairSuccess(t, f.pairWithCode(t, "192.0.2.10", f.issueCode(t)))

	if rec := f.authed(t, http.MethodGet, "/v1/devices", victim.Token); rec.Code != http.StatusOK {
		t.Fatalf("before revocation: status = %d, want 200", rec.Code)
	}

	// A device may revoke itself; the contract says that logs it out.
	if rec := f.authed(t, http.MethodDelete, "/v1/devices/"+victim.DeviceID, victim.Token); rec.Code != http.StatusNoContent {
		t.Fatalf("self-revocation: status = %d, want 204", rec.Code)
	}

	rec := f.authed(t, http.MethodGet, "/v1/devices", victim.Token)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("after revocation: status = %d, want 401 (body %q)", rec.Code, rec.Body.String())
	}
	e := decodeError(t, rec)
	if e.Error.Code != core.CodeUnauthorized {
		t.Errorf("error code = %s, want UNAUTHORIZED", e.Error.Code)
	}
	if e.Error.Retryable {
		t.Error("UNAUTHORIZED is marked retryable; the client must re-pair instead (02-API.md §2)")
	}
}

func TestPostPair_DoesNotLogTheIssuedToken(t *testing.T) {
	var logs bytes.Buffer
	clock := newPairClock()
	st := testutil.NewMemStore()
	st.Now = func() int64 { return clock.Now().UnixMilli() }
	pairer := auth.NewPairer(st, auth.PairerOptions{Now: clock.Now})

	srv := httpapi.New(testConfig(t), slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	httpapi.RegisterPairing(srv, httpapi.PairingDeps{
		Pairer: pairer, Auth: auth.NewAuthenticator(st, auth.AuthenticatorOptions{Now: clock.Now}),
		Store: st, ServerID: "srv_1", ServerName: "test", Now: clock.Now,
	})

	code, err := pairer.IssueCode(context.Background())
	if err != nil {
		t.Fatalf("IssueCode: %v", err)
	}
	f := &pairFixture{srv: srv, pairer: pairer, clock: clock}
	rec := f.pairWithCode(t, "192.0.2.10", code.Value)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rec.Code)
	}

	res := decodePairSuccess(t, rec)
	if logs.Len() == 0 {
		t.Fatal("the access log wrote nothing; the assertion below would be vacuous")
	}
	if strings.Contains(logs.String(), res.Token) {
		t.Errorf("the issued token reached the access log:\n%s", logs.String())
	}
	if strings.Contains(logs.String(), code.Value) {
		t.Errorf("the pairing code reached the access log:\n%s", logs.String())
	}
}

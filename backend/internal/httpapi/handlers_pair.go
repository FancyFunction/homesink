package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/FancyFunction/homesink/backend/internal/auth"
	"github.com/FancyFunction/homesink/backend/internal/core"
	"github.com/FancyFunction/homesink/backend/internal/store"
)

// maxPairBodyBytes caps the pairing request body. The contract's largest legal
// PairRequest is a few hundred bytes; anything past this is not a client.
const maxPairBodyBytes = 4 << 10

// PairingDeps is everything the pairing and device routes need. ServerName
// falls back to the configured HOMESINK_SERVER_NAME when empty.
type PairingDeps struct {
	Pairer     *auth.Pairer
	Auth       *auth.Authenticator
	Store      store.Store
	ServerID   string
	ServerName string
	// TLSSPKISHA256 is the base64 SPKI fingerprint the client pins (D-02). It is
	// empty when HOMESINK_TLS=off, in which case there is nothing to pin.
	TLSSPKISHA256 string
	// Now is the clock behind serverTimeMs; nil means time.Now.
	Now func() time.Time
}

// pairingHandlers serves POST /v1/pair, GET /v1/devices and
// DELETE /v1/devices/{deviceId} (02-API.md §3).
type pairingHandlers struct {
	deps PairingDeps
	log  *slog.Logger
}

// RegisterPairing adds the pairing and device routes to s. Pairing itself is
// unauthenticated — it is how a device gets its credential — while the device
// list and revocation sit behind the bearer middleware.
func RegisterPairing(s *Server, deps PairingDeps) {
	if deps.ServerName == "" {
		deps.ServerName = s.cfg.ServerName
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	h := &pairingHandlers{deps: deps, log: s.log}

	s.Register(func(mux *http.ServeMux) {
		mux.HandleFunc("POST /v1/pair", h.pair)
		mux.Handle("GET /v1/devices", deps.Auth.Require(http.HandlerFunc(h.listDevices)))
		mux.Handle("DELETE /v1/devices/{deviceId}", deps.Auth.Require(http.HandlerFunc(h.revokeDevice)))
	})
}

// pairRequest mirrors the contract's PairRequest schema.
type pairRequest struct {
	Code           string `json:"code"`
	DeviceName     string `json:"deviceName"`
	Platform       string `json:"platform"`
	AppVersionCode int64  `json:"appVersionCode"`
}

// pairResponse mirrors the contract's PairResponse schema. Token is the only
// time the credential crosses the wire in cleartext.
type pairResponse struct {
	Token         string `json:"token"`
	DeviceID      string `json:"deviceId"`
	ServerID      string `json:"serverId"`
	ServerName    string `json:"serverName"`
	TLSSpkiSha256 string `json:"tlsSpkiSha256"`
	ServerTimeMs  int64  `json:"serverTimeMs"`
}

// deviceResponse mirrors the contract's Device schema. AppVersionCode is
// omitted while unknown; the contract marks it optional.
type deviceResponse struct {
	DeviceID       string `json:"deviceId"`
	Name           string `json:"name"`
	LastSeenMs     int64  `json:"lastSeenMs"`
	AppVersionCode int64  `json:"appVersionCode,omitempty"`
	Current        bool   `json:"current"`
}

// pair exchanges a pairing code for a device token (D-01).
func (h *pairingHandlers) pair(w http.ResponseWriter, r *http.Request) {
	var req pairRequest
	// Unknown fields are tolerated: the contract does not close PairRequest, so
	// a newer client sending an extra field must still be able to pair.
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxPairBodyBytes))
	if err := dec.Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		// The contract documents 400/410/429 for this operation and core is
		// frozen, so a body the server cannot read reuses PAIRING_CODE_INVALID:
		// the client action it prescribes — show the error, ask for a new code —
		// is right for a malformed request too.
		writeError(w, core.ErrPairingCodeInvalid())
		return
	}

	res, err := h.deps.Pairer.Pair(r.Context(), auth.PairRequest{
		Code:           req.Code,
		DeviceName:     req.DeviceName,
		Platform:       req.Platform,
		AppVersionCode: req.AppVersionCode,
		ClientIP:       clientIP(r),
	})
	if err != nil {
		h.fail(w, r, "pairing failed", err)
		return
	}

	writePairJSON(w, http.StatusCreated, pairResponse{
		Token:         res.Token,
		DeviceID:      res.DeviceID,
		ServerID:      h.deps.ServerID,
		ServerName:    h.deps.ServerName,
		TLSSpkiSha256: h.deps.TLSSPKISHA256,
		ServerTimeMs:  h.deps.Now().UnixMilli(),
	})
}

// listDevices returns every paired device, flagging the caller's own (D-01
// revocation needs a list to revoke from).
func (h *pairingHandlers) listDevices(w http.ResponseWriter, r *http.Request) {
	caller, _ := auth.DeviceFromContext(r.Context())

	devices, err := h.deps.Store.ListDevices(r.Context())
	if err != nil {
		h.fail(w, r, "listing devices failed", err)
		return
	}

	out := make([]deviceResponse, 0, len(devices))
	for _, d := range devices {
		if d.Revoked() {
			// A revoked device is gone as far as the app is concerned; the row
			// survives only so its token can never be re-issued.
			continue
		}
		out = append(out, deviceResponse{
			DeviceID:       d.DeviceID,
			Name:           d.Name,
			LastSeenMs:     d.LastSeenAt,
			AppVersionCode: d.AppVersionCode,
			Current:        caller != nil && caller.DeviceID == d.DeviceID,
		})
	}
	writePairJSON(w, http.StatusOK, out)
}

// revokeDevice cuts off one device's token. Revoking your own device is allowed
// and logs you out (02-API.md §3); revoking an already-revoked one is a no-op,
// so the endpoint is idempotent.
func (h *pairingHandlers) revokeDevice(w http.ResponseWriter, r *http.Request) {
	deviceID := r.PathValue("deviceId")
	if err := h.deps.Store.RevokeDevice(r.Context(), deviceID); err != nil {
		h.fail(w, r, "revoking device failed", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// fail writes err as the standard envelope. A *core.Error is the client's to
// see; anything else is logged here and becomes an opaque 500, so an internal
// message never reaches the wire.
func (h *pairingHandlers) fail(w http.ResponseWriter, r *http.Request, msg string, err error) {
	var cerr *core.Error
	if errors.As(err, &cerr) {
		writeError(w, cerr)
		return
	}
	h.log.ErrorContext(r.Context(), msg,
		"requestId", RequestID(r), "path", r.URL.Path, "error", err)
	writeError(w, core.ErrInternal())
}

// writePairJSON encodes v as the response body. It is named for its package
// neighbourhood so a later work package's own JSON helper cannot collide with
// it (08-ROADMAP.md §4).
func writePairJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

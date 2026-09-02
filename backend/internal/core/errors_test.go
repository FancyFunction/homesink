package core_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/FancyFunction/homesink/backend/internal/core"
)

func TestError_MessageAndCode(t *testing.T) {
	e := core.ErrHashMismatch("aa", "bb")
	if !strings.Contains(e.Error(), core.CodeHashMismatch) {
		t.Errorf("Error() = %q, want it to contain the code", e.Error())
	}

	bare := &core.Error{Code: "X"}
	if bare.Error() != "X" {
		t.Errorf("Error() with no message = %q, want %q", bare.Error(), "X")
	}
}

func TestError_IsMatchesByCode(t *testing.T) {
	wrapped := core.ErrNotFound()
	if !errors.Is(wrapped, core.ErrNotFound()) {
		t.Error("errors.Is should match two NOT_FOUND errors")
	}
	if errors.Is(wrapped, core.ErrInternal()) {
		t.Error("errors.Is should not match different codes")
	}
}

// Every constructor in 02-API.md §2 (plus BATCH_TOO_LARGE from §4.1) maps to a
// stable code, HTTP status and retryability.
func TestError_Constructors(t *testing.T) {
	cases := []struct {
		name      string
		err       *core.Error
		code      string
		status    int
		retryable bool
	}{
		{"unauthorized", core.ErrUnauthorized(), core.CodeUnauthorized, 401, false},
		{"pairing invalid", core.ErrPairingCodeInvalid(), core.CodePairingCodeInvalid, 400, false},
		{"pairing expired", core.ErrPairingCodeExpired(), core.CodePairingCodeExpired, 410, false},
		{"pairing rate limited", core.ErrPairingRateLimited(900000), core.CodePairingRateLimited, 429, true},
		{"range mismatch", core.ErrRangeMismatch(16777216, 25165824), core.CodeRangeMismatch, 409, true},
		{"hash mismatch", core.ErrHashMismatch("a", "b"), core.CodeHashMismatch, 409, true},
		{"unsupported media", core.ErrUnsupportedMediaType("application/zip"), core.CodeUnsupportedMedia, 415, false},
		{"file too large", core.ErrFileTooLarge(17179869184), core.CodeFileTooLarge, 413, false},
		{"insufficient storage", core.ErrInsufficientStorage(1048576, 10737418240), core.CodeInsufficientStorage, 507, false},
		{"batch too large", core.ErrBatchTooLarge(500, 731), core.CodeBatchTooLarge, 400, false},
		{"not found", core.ErrNotFound(), core.CodeNotFound, 404, false},
		{"internal", core.ErrInternal(), core.CodeInternal, 500, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.err.Code != tc.code {
				t.Errorf("Code = %q, want %q", tc.err.Code, tc.code)
			}
			if tc.err.HTTPStatus != tc.status {
				t.Errorf("HTTPStatus = %d, want %d", tc.err.HTTPStatus, tc.status)
			}
			if tc.err.Retryable != tc.retryable {
				t.Errorf("Retryable = %v, want %v", tc.err.Retryable, tc.retryable)
			}
			if tc.err.Message == "" {
				t.Error("Message should not be empty")
			}
		})
	}
}

func TestError_DetailsPopulated(t *testing.T) {
	if got := core.ErrRangeMismatch(10, 20).Details["expectedStart"]; got != int64(10) {
		t.Errorf("expectedStart detail = %v, want 10", got)
	}
	if got := core.ErrHashMismatch("exp", "act").Details["actual"]; got != "act" {
		t.Errorf("actual detail = %v, want %q", got, "act")
	}
	if core.ErrInsufficientStorage(0, 0).Details != nil {
		t.Error("insufficient-storage details should be nil when no numbers are given")
	}
}

package core

import "fmt"

// Error is the single error shape crossing the wire (02-API.md §2). Handlers
// serialise it to {"error": {code, message, retryable, details}}; the HTTP
// status is carried alongside for the handler to apply and is not itself part
// of the JSON body.
type Error struct {
	Code       string
	Message    string
	Retryable  bool
	HTTPStatus int
	Details    map[string]any
}

// Error implements the error interface.
func (e *Error) Error() string {
	if e.Message == "" {
		return e.Code
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Is reports whether target is a *Error with the same Code, so callers can
// write errors.Is(err, core.ErrNotFound()).
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Code == e.Code
}

// Error codes. These mirror 02-API.md §2 exactly, plus BATCH_TOO_LARGE from
// §4.1 (this file freezes after WP-B1, so every code any later package needs
// must be defined here now).
const (
	CodeUnauthorized        = "UNAUTHORIZED"
	CodePairingCodeInvalid  = "PAIRING_CODE_INVALID"
	CodePairingCodeExpired  = "PAIRING_CODE_EXPIRED"
	CodePairingRateLimited  = "PAIRING_RATE_LIMITED"
	CodeRangeMismatch       = "RANGE_MISMATCH"
	CodeHashMismatch        = "HASH_MISMATCH"
	CodeUnsupportedMedia    = "UNSUPPORTED_MEDIA_TYPE"
	CodeFileTooLarge        = "FILE_TOO_LARGE"
	CodeInsufficientStorage = "INSUFFICIENT_STORAGE"
	CodeBatchTooLarge       = "BATCH_TOO_LARGE"
	CodeNotFound            = "NOT_FOUND"
	CodeInternal            = "INTERNAL"
)

// ErrUnauthorized — 401, not retryable. Client drops the token and re-pairs.
func ErrUnauthorized() *Error {
	return &Error{
		Code:       CodeUnauthorized,
		Message:    "Nicht autorisiert. Bitte das Gerät neu koppeln.",
		Retryable:  false,
		HTTPStatus: 401,
	}
}

// ErrPairingCodeInvalid — 400, not retryable.
func ErrPairingCodeInvalid() *Error {
	return &Error{
		Code:       CodePairingCodeInvalid,
		Message:    "Der eingegebene Kopplungscode ist ungültig.",
		Retryable:  false,
		HTTPStatus: 400,
	}
}

// ErrPairingCodeExpired — 410, not retryable.
func ErrPairingCodeExpired() *Error {
	return &Error{
		Code:       CodePairingCodeExpired,
		Message:    "Der Kopplungscode ist abgelaufen. Bitte einen neuen Code anfordern.",
		Retryable:  false,
		HTTPStatus: 410,
	}
}

// ErrPairingRateLimited — 429, retryable after retryAfterMs.
func ErrPairingRateLimited(retryAfterMs int64) *Error {
	return &Error{
		Code:       CodePairingRateLimited,
		Message:    "Zu viele Fehlversuche. Bitte 15 Minuten warten.",
		Retryable:  true,
		HTTPStatus: 429,
		Details:    map[string]any{"retryAfterMs": retryAfterMs},
	}
}

// ErrRangeMismatch — 409, retryable. The client re-HEADs and resumes from
// expectedStart (02-API.md §4.2).
func ErrRangeMismatch(expectedStart, receivedStart int64) *Error {
	return &Error{
		Code:       CodeRangeMismatch,
		Message:    "Der Chunk beginnt nicht an der erwarteten Position.",
		Retryable:  true,
		HTTPStatus: 409,
		Details:    map[string]any{"expectedStart": expectedStart, "receivedStart": receivedStart},
	}
}

// ErrHashMismatch — 409. The staged file is destroyed; the client re-hashes
// once and retries, then fails the file (D-18).
func ErrHashMismatch(expected, actual string) *Error {
	return &Error{
		Code:       CodeHashMismatch,
		Message:    "Die hochgeladene Datei stimmt nicht mit dem angegebenen Hash überein.",
		Retryable:  true,
		HTTPStatus: 409,
		Details:    map[string]any{"expected": expected, "actual": actual},
	}
}

// ErrUnsupportedMediaType — 415, not retryable. Rejected at preflight (D-14).
func ErrUnsupportedMediaType(mimeType string) *Error {
	return &Error{
		Code:       CodeUnsupportedMedia,
		Message:    "Dieser Medientyp wird nicht unterstützt.",
		Retryable:  false,
		HTTPStatus: 415,
		Details:    map[string]any{"mimeType": mimeType},
	}
}

// ErrFileTooLarge — 413, not retryable. maxBytes is HOMESINK_MAX_FILE_BYTES (D-21).
func ErrFileTooLarge(maxBytes int64) *Error {
	return &Error{
		Code:       CodeFileTooLarge,
		Message:    "Die Datei überschreitet die maximal zulässige Größe.",
		Retryable:  false,
		HTTPStatus: 413,
		Details:    map[string]any{"maxBytes": maxBytes},
	}
}

// ErrInsufficientStorage — 507, not retryable. freeBytes/requiredBytes describe
// the shortfall the disk guard found (D-20); pass 0 for either when unknown.
func ErrInsufficientStorage(freeBytes, requiredBytes int64) *Error {
	e := &Error{
		Code:       CodeInsufficientStorage,
		Message:    "Auf dem Speicher ist nicht genügend Platz.",
		Retryable:  false,
		HTTPStatus: 507,
	}
	if freeBytes != 0 || requiredBytes != 0 {
		e.Details = map[string]any{"freeBytes": freeBytes, "requiredBytes": requiredBytes}
	}
	return e
}

// ErrBatchTooLarge — 400, not retryable. Preflight accepts at most limit items
// (02-API.md §4.1).
func ErrBatchTooLarge(limit, received int) *Error {
	return &Error{
		Code:       CodeBatchTooLarge,
		Message:    "Höchstens 500 Einträge pro Anfrage.",
		Retryable:  false,
		HTTPStatus: 400,
		Details:    map[string]any{"limit": limit, "received": received},
	}
}

// ErrNotFound — 404, not retryable.
func ErrNotFound() *Error {
	return &Error{
		Code:       CodeNotFound,
		Message:    "Die angeforderte Ressource wurde nicht gefunden.",
		Retryable:  false,
		HTTPStatus: 404,
	}
}

// ErrInternal — 500, retryable. The underlying cause is logged server-side by
// the handler layer and never travels to the client.
func ErrInternal() *Error {
	return &Error{
		Code:       CodeInternal,
		Message:    "Interner Serverfehler.",
		Retryable:  true,
		HTTPStatus: 500,
	}
}

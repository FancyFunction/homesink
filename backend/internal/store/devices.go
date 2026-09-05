package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Device is one paired phone. TokenHash is the Argon2id hash of the device
// token — never the token itself (D-01), and never logged.
type Device struct {
	DeviceID       string
	Name           string
	TokenHash      string
	Platform       string
	AppVersionCode int64
	CreatedAt      int64
	// LastSeenAt is 0 until the device's first authenticated request.
	LastSeenAt int64
	// RevokedAt is 0 while the device is active; once set the token is dead and
	// the next request gets 401 (D-01 revocation).
	RevokedAt int64
}

// Revoked reports whether this device's token has been cut off.
func (d Device) Revoked() bool { return d.RevokedAt != 0 }

// PairingCode is one issued 6-digit code, stored only as a hash. Attempts is the
// wrong-guess counter behind the 5-strikes rule (D-01).
type PairingCode struct {
	CodeHash    string
	ExpiresAtMs int64
	UsedAtMs    int64
	Attempts    int
}

// Used reports whether the code has already been redeemed — single use, per D-01.
func (p PairingCode) Used() bool { return p.UsedAtMs != 0 }

// deviceColumns is the read projection, kept in one place so every scanner agrees.
const deviceColumns = `device_id, name, token_hash, platform, app_version_code,
	created_at, last_seen_at, revoked_at`

// InsertDevice records a newly paired device. A repeated device id is
// ErrAlreadyExists.
func (s *SQLite) InsertDevice(ctx context.Context, d Device) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO devices (device_id, name, token_hash, platform, app_version_code,
				created_at, last_seen_at, revoked_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			d.DeviceID, d.Name, d.TokenHash, d.Platform, nullInt64(d.AppVersionCode),
			d.CreatedAt, nullInt64(d.LastSeenAt), nullInt64(d.RevokedAt))
		if err != nil {
			return fmt.Errorf("store: insert device: %w", asAlreadyExists(err))
		}
		return nil
	})
}

// DeviceByID returns one device whether or not it is revoked, so the devices
// endpoint can show a cut-off phone. An unknown id is core.ErrNotFound.
func (s *SQLite) DeviceByID(ctx context.Context, deviceID string) (*Device, error) {
	row := s.read.QueryRowContext(ctx, `SELECT `+deviceColumns+` FROM devices WHERE device_id = ?`, deviceID)
	d, err := scanDevice(row)
	if err != nil {
		return nil, notFound(err)
	}
	return d, nil
}

// DeviceByTokenHash resolves a token hash to its device for the auth middleware.
// Revoked devices are excluded — which is both the D-01 requirement that a
// revoked token fails on the next request, and what lets the query ride the
// partial index idx_devices_token. A revoked or unknown hash is core.ErrNotFound.
func (s *SQLite) DeviceByTokenHash(ctx context.Context, tokenHash string) (*Device, error) {
	row := s.read.QueryRowContext(ctx,
		`SELECT `+deviceColumns+` FROM devices WHERE token_hash = ? AND revoked_at IS NULL`, tokenHash)
	d, err := scanDevice(row)
	if err != nil {
		return nil, notFound(err)
	}
	return d, nil
}

// ListDevices returns every device, revoked ones included, oldest first.
func (s *SQLite) ListDevices(ctx context.Context) ([]Device, error) {
	rows, err := s.read.QueryContext(ctx,
		`SELECT `+deviceColumns+` FROM devices ORDER BY created_at, device_id`)
	if err != nil {
		return nil, fmt.Errorf("store: list devices: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Device
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list devices: %w", err)
	}
	return out, nil
}

// RevokeDevice stamps revoked_at, killing the device's token. Revoking twice
// keeps the first timestamp and is not an error; an unknown id is
// core.ErrNotFound.
func (s *SQLite) RevokeDevice(ctx context.Context, deviceID string) error {
	now := s.now()
	return s.withTx(ctx, func(tx *sql.Tx) error {
		var revokedAt sql.NullInt64
		if err := tx.QueryRowContext(ctx,
			`SELECT revoked_at FROM devices WHERE device_id = ?`, deviceID).Scan(&revokedAt); err != nil {
			return notFound(err)
		}
		if revokedAt.Valid {
			return nil
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE devices SET revoked_at = ? WHERE device_id = ?`, now, deviceID); err != nil {
			return fmt.Errorf("store: revoke device: %w", err)
		}
		return nil
	})
}

// TouchDevice refreshes last_seen_at and, when appVersionCode is non-zero,
// app_version_code. The middleware calls it at most once per minute per device
// rather than on every request (D-01/WP-B3), so this stays a rare write.
func (s *SQLite) TouchDevice(ctx context.Context, deviceID string, appVersionCode int64) error {
	now := s.now()
	return s.withTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`UPDATE devices SET last_seen_at = ?,
				app_version_code = COALESCE(?, app_version_code)
			 WHERE device_id = ?`,
			now, nullInt64(appVersionCode), deviceID)
		if err != nil {
			return fmt.Errorf("store: touch device: %w", err)
		}
		return requireOneRow(res, "device")
	})
}

// InsertPairingCode records an issued code hash and its expiry. A repeated hash
// is ErrAlreadyExists, which the issuer answers by drawing a new code.
func (s *SQLite) InsertPairingCode(ctx context.Context, pc PairingCode) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO pairing_codes (code_hash, expires_at, used_at, attempts)
			 VALUES (?, ?, ?, ?)`,
			pc.CodeHash, pc.ExpiresAtMs, nullInt64(pc.UsedAtMs), pc.Attempts)
		if err != nil {
			return fmt.Errorf("store: insert pairing code: %w", asAlreadyExists(err))
		}
		return nil
	})
}

// PairingCodeByHash returns one code row. Expiry and single-use are the caller's
// checks — WP-B3 needs to tell 410 (expired) apart from 400 (already used), so
// this returns the row rather than filtering. An unknown hash is core.ErrNotFound.
func (s *SQLite) PairingCodeByHash(ctx context.Context, codeHash string) (*PairingCode, error) {
	var (
		pc     PairingCode
		usedAt sql.NullInt64
	)
	err := s.read.QueryRowContext(ctx,
		`SELECT code_hash, expires_at, used_at, attempts FROM pairing_codes WHERE code_hash = ?`,
		codeHash).Scan(&pc.CodeHash, &pc.ExpiresAtMs, &usedAt, &pc.Attempts)
	if err != nil {
		return nil, notFound(err)
	}
	pc.UsedAtMs = usedAt.Int64
	return &pc, nil
}

// MarkPairingCodeUsed burns a code. Marking an already-used code is
// ErrAlreadyExists, so a race between two devices redeeming the same code has
// exactly one winner — the single-use guarantee of D-01 lives here, not in the
// caller's read-then-write.
func (s *SQLite) MarkPairingCodeUsed(ctx context.Context, codeHash string) error {
	now := s.now()
	return s.withTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`UPDATE pairing_codes SET used_at = ? WHERE code_hash = ? AND used_at IS NULL`,
			now, codeHash)
		if err != nil {
			return fmt.Errorf("store: mark pairing code used: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("store: mark pairing code used: %w", err)
		}
		if n == 0 {
			// Either the code does not exist or someone already used it.
			var used sql.NullInt64
			err := tx.QueryRowContext(ctx,
				`SELECT used_at FROM pairing_codes WHERE code_hash = ?`, codeHash).Scan(&used)
			if errors.Is(err, sql.ErrNoRows) {
				return notFound(err)
			}
			if err != nil {
				return fmt.Errorf("store: mark pairing code used: %w", err)
			}
			return ErrAlreadyExists
		}
		return nil
	})
}

// IncrementPairingCodeAttempts counts a wrong guess and returns the new total,
// which WP-B3 compares against the 5-attempt limit. An unknown hash is
// core.ErrNotFound.
func (s *SQLite) IncrementPairingCodeAttempts(ctx context.Context, codeHash string) (int, error) {
	var attempts int
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`UPDATE pairing_codes SET attempts = attempts + 1 WHERE code_hash = ?`, codeHash)
		if err != nil {
			return fmt.Errorf("store: increment pairing attempts: %w", err)
		}
		if err := requireOneRow(res, "pairing code"); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx,
			`SELECT attempts FROM pairing_codes WHERE code_hash = ?`, codeHash).Scan(&attempts)
	})
	if err != nil {
		return 0, err
	}
	return attempts, nil
}

// DeletePairingCodesBefore removes codes that expired before the given instant
// and returns how many went. Codes are worthless once expired, so the sweeper
// deletes them rather than keeping dead rows around.
func (s *SQLite) DeletePairingCodesBefore(ctx context.Context, expiresBeforeMs int64) (int, error) {
	var removed int
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`DELETE FROM pairing_codes WHERE expires_at < ?`, expiresBeforeMs)
		if err != nil {
			return fmt.Errorf("store: delete expired pairing codes: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("store: delete expired pairing codes: %w", err)
		}
		removed = int(n)
		return nil
	})
	if err != nil {
		return 0, err
	}
	return removed, nil
}

func scanDevice(sc scanner) (*Device, error) {
	var (
		d              Device
		appVersionCode sql.NullInt64
		lastSeenAt     sql.NullInt64
		revokedAt      sql.NullInt64
	)
	if err := sc.Scan(&d.DeviceID, &d.Name, &d.TokenHash, &d.Platform, &appVersionCode,
		&d.CreatedAt, &lastSeenAt, &revokedAt); err != nil {
		return nil, err
	}
	d.AppVersionCode = appVersionCode.Int64
	d.LastSeenAt = lastSeenAt.Int64
	d.RevokedAt = revokedAt.Int64
	return &d, nil
}

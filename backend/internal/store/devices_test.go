package store_test

import (
	"context"
	"testing"
)

// TestPairingCodeStartsUnusedWithZeroAttempts: a freshly issued code has to read
// as usable, since WP-B3 tells 410 (expired) from 400 (already used) by these
// two columns.
func TestPairingCodeStartsUnusedWithZeroAttempts(t *testing.T) {
	ctx := context.Background()
	s := newSQLiteStore(t)

	pc := samplePairingCode()
	if err := s.InsertPairingCode(ctx, pc); err != nil {
		t.Fatalf("insert pairing code: %v", err)
	}

	if got := rawScalar(t, s.Path(),
		`SELECT used_at IS NULL FROM pairing_codes WHERE code_hash = ?`, pc.CodeHash); got != int64(1) {
		t.Fatalf("pairing_codes.used_at is not NULL for a fresh code")
	}
	if got := rawScalar(t, s.Path(),
		`SELECT attempts FROM pairing_codes WHERE code_hash = ?`, pc.CodeHash); got != int64(0) {
		t.Fatalf("pairing_codes.attempts = %v, want 0", got)
	}
}

// TestNewDeviceHasNoLastSeenOrRevocation: last_seen_at is set by the first
// authenticated request, not by pairing, and NULL is what "never seen" means.
func TestNewDeviceHasNoLastSeenOrRevocation(t *testing.T) {
	ctx := context.Background()
	s := newSQLiteStore(t)

	d := sampleDevice("dev_1", "argon2id$fake")
	d.AppVersionCode = 0
	if err := s.InsertDevice(ctx, d); err != nil {
		t.Fatalf("insert device: %v", err)
	}

	for _, column := range []string{"last_seen_at", "revoked_at", "app_version_code"} {
		got := rawScalar(t, s.Path(),
			`SELECT `+column+` IS NULL FROM devices WHERE device_id = ?`, d.DeviceID)
		if got != int64(1) {
			t.Errorf("devices.%s is not NULL for a newly paired device", column)
		}
	}
}

// TestTouchDeviceLeavesTheIdentityColumnsAlone: the middleware calls TouchDevice
// on request traffic, and it must never be able to rename a device or move its
// creation time.
func TestTouchDeviceLeavesTheIdentityColumnsAlone(t *testing.T) {
	ctx := context.Background()
	s := newSQLiteStore(t)

	before := sampleDevice("dev_1", "argon2id$fake")
	before.CreatedAt = testNowMs - 5_000
	if err := s.InsertDevice(ctx, before); err != nil {
		t.Fatalf("insert device: %v", err)
	}
	if err := s.TouchDevice(ctx, before.DeviceID, 99); err != nil {
		t.Fatalf("touch: %v", err)
	}

	after, err := s.DeviceByID(ctx, before.DeviceID)
	if err != nil {
		t.Fatalf("device by id: %v", err)
	}
	if after.Name != before.Name || after.Platform != before.Platform ||
		after.TokenHash != before.TokenHash || after.CreatedAt != before.CreatedAt {
		t.Fatalf("touch changed identity columns\n got %+v\nwant %+v", *after, before)
	}
}

// TestRevokingADeviceKeepsItsToken hash on the row: WP-B3 needs the row to stay
// intact so a revoked phone shows up as revoked rather than as never having
// existed, and re-pairing issues a fresh token instead of reviving the old one.
func TestRevokingADeviceKeepsItsTokenHash(t *testing.T) {
	ctx := context.Background()
	s := newSQLiteStore(t)

	d := sampleDevice("dev_1", "argon2id$fake")
	if err := s.InsertDevice(ctx, d); err != nil {
		t.Fatalf("insert device: %v", err)
	}
	if err := s.RevokeDevice(ctx, d.DeviceID); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	got, err := s.DeviceByID(ctx, d.DeviceID)
	if err != nil {
		t.Fatalf("device by id: %v", err)
	}
	if got.TokenHash != d.TokenHash {
		t.Fatalf("token hash = %q, want %q kept", got.TokenHash, d.TokenHash)
	}
	if got.RevokedAt == 0 {
		t.Fatal("revoked_at was not set")
	}
}

package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FancyFunction/homesink/backend/internal/auth"
	"github.com/FancyFunction/homesink/backend/internal/config"
	"github.com/FancyFunction/homesink/backend/internal/store"
)

// pairEnv is a HOMESINK_DATA pointing at a scratch directory.
func pairEnv(t *testing.T) (config.Getenv, string) {
	t.Helper()
	dir := t.TempDir()
	env := map[string]string{"HOMESINK_DATA": dir, "HOMESINK_ADDR": "127.0.0.1:0", "HOMESINK_TLS": "off"}
	return func(k string) string { return env[k] }, dir
}

func TestRunPair_PrintsACodeThatIsStoredHashedAndRedeemable(t *testing.T) {
	getenv, dir := pairEnv(t)
	ctx := context.Background()

	var out bytes.Buffer
	if err := runPair(ctx, getenv, &out); err != nil {
		t.Fatalf("runPair: %v", err)
	}

	// The printed code is six digits and nothing else leaks onto stdout.
	printed := ""
	for _, field := range strings.Fields(out.String()) {
		if auth.ValidCodeFormat(field) {
			printed = field
			break
		}
	}
	if printed == "" {
		t.Fatalf("no six-digit code in the output:\n%s", out.String())
	}

	st, err := store.Open(ctx, store.Options{Path: filepath.Join(dir, ".homesink", "db", "homesink.db")})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() { _ = st.Close() }()

	if _, err := st.PairingCodeByHash(ctx, auth.HashPairingCode(printed)); err != nil {
		t.Fatalf("the printed code is not redeemable: %v", err)
	}

	res, err := auth.NewPairer(st, auth.PairerOptions{}).Pair(ctx, auth.PairRequest{
		Code: printed, DeviceName: "Pixel 8", Platform: "android", AppVersionCode: 14,
		ClientIP: "192.0.2.10",
	})
	if err != nil {
		t.Fatalf("pairing with the printed code: %v", err)
	}
	if !auth.ValidTokenFormat(res.Token) {
		t.Errorf("token = %q, want hs_ + 43 base64url characters", res.Token)
	}
}

func TestRunPair_FailsOnAnUnusableDataDir(t *testing.T) {
	env := map[string]string{"HOMESINK_DATA": filepath.Join(t.TempDir(), "missing")}
	err := runPair(context.Background(), func(k string) string { return env[k] }, &bytes.Buffer{})
	if err == nil {
		t.Fatal("runPair succeeded with a data directory that does not exist")
	}
}

package auth_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/FancyFunction/homesink/backend/internal/auth"
	"github.com/FancyFunction/homesink/backend/internal/store"
	"github.com/FancyFunction/homesink/backend/internal/testutil"
)

// openDB opens a migrated SQLite store at path, so a "restart" is a genuine
// close and reopen of the same file rather than a fake.
func openDB(t *testing.T, path string) *store.SQLite {
	t.Helper()
	st, err := store.Open(context.Background(), store.Options{Path: path})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	if _, err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// --- Acceptance: the certificate survives a restart and the SPKI is stable ---

func TestEnsureTLS_CertificateSurvivesRestartAndTheSPKIIsStable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "homesink.db")
	ctx := context.Background()
	opts := auth.EnsureTLSOptions{IPs: []net.IP{net.ParseIP("192.168.1.20")}}

	first := openDB(t, path)
	before, err := auth.EnsureTLS(ctx, first, opts)
	if err != nil {
		t.Fatalf("EnsureTLS: %v", err)
	}
	if !before.Generated {
		t.Error("the first call did not report generating the material")
	}
	mustNotBeEmpty(t, "SPKI fingerprint", before.SPKISHA256)
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Restart: a new process against the same database file.
	second := openDB(t, path)
	after, err := auth.EnsureTLS(ctx, second, opts)
	if err != nil {
		t.Fatalf("EnsureTLS after restart: %v", err)
	}
	if after.Generated {
		t.Error("the restart regenerated the material instead of loading it")
	}
	if after.SPKISHA256 != before.SPKISHA256 {
		t.Errorf("SPKI changed across a restart: %q then %q; every paired device would break (D-02)",
			before.SPKISHA256, after.SPKISHA256)
	}
	if !bytes.Equal(after.Leaf.Raw, before.Leaf.Raw) {
		t.Error("the certificate itself changed across a restart")
	}
	if !after.Leaf.Equal(before.Leaf) {
		t.Error("the reloaded certificate is not the persisted one")
	}

	spki, err := second.Meta(ctx, auth.MetaKeyTLSSPKI)
	if err != nil {
		t.Fatalf("Meta(%s): %v", auth.MetaKeyTLSSPKI, err)
	}
	if spki != before.SPKISHA256 {
		t.Errorf("server_meta holds SPKI %q, want %q", spki, before.SPKISHA256)
	}
}

func TestEnsureTLS_GeneratesAnECP256CertificateWithTheSpecifiedSANs(t *testing.T) {
	st := testutil.NewMemStore()
	ips := []net.IP{net.ParseIP("192.168.1.20"), net.ParseIP("10.0.0.5")}
	now := time.Date(2025, 8, 17, 14, 0, 0, 0, time.UTC)

	m, err := auth.EnsureTLS(context.Background(), st, auth.EnsureTLSOptions{
		Now: func() time.Time { return now },
		IPs: ips,
	})
	if err != nil {
		t.Fatalf("EnsureTLS: %v", err)
	}

	key, ok := m.Leaf.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		t.Fatalf("public key is %T, want *ecdsa.PublicKey", m.Leaf.PublicKey)
	}
	if key.Curve != elliptic.P256() {
		t.Errorf("curve = %s, want P-256", key.Curve.Params().Name)
	}
	for _, want := range []string{"localhost", "homesink.local"} {
		if !slices.Contains(m.Leaf.DNSNames, want) {
			t.Errorf("DNS SANs %v are missing %q", m.Leaf.DNSNames, want)
		}
	}
	for _, want := range ips {
		if !slices.ContainsFunc(m.Leaf.IPAddresses, func(ip net.IP) bool { return ip.Equal(want) }) {
			t.Errorf("IP SANs %v are missing %s", m.Leaf.IPAddresses, want)
		}
	}
	if got := m.Leaf.NotAfter.Sub(now); got < 10*365*24*time.Hour {
		t.Errorf("validity is %s, want at least ten years (D-02)", got)
	}
	if !m.Leaf.NotBefore.Before(now) {
		t.Error("NotBefore is not backdated; a slow clock on first boot would reject the certificate")
	}
	if !slices.Contains(m.Leaf.ExtKeyUsage, x509.ExtKeyUsageServerAuth) {
		t.Error("the certificate is not marked for server authentication")
	}
	if _, err := base64.StdEncoding.DecodeString(m.SPKISHA256); err != nil {
		t.Errorf("SPKI fingerprint %q is not base64: %v", m.SPKISHA256, err)
	}
}

func TestEnsureTLS_SPKIMatchesWhatAPinningClientComputes(t *testing.T) {
	st := testutil.NewMemStore()
	m, err := auth.EnsureTLS(context.Background(), st, auth.EnsureTLSOptions{
		IPs: []net.IP{net.ParseIP("127.0.0.1")},
	})
	if err != nil {
		t.Fatalf("EnsureTLS: %v", err)
	}

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	srv.TLS = m.TLSConfig()
	srv.StartTLS()
	defer srv.Close()

	roots := x509.NewCertPool()
	roots.AddCert(m.Leaf)
	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
	}}

	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("TLS request against the generated certificate: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if _, err := io.ReadAll(resp.Body); err != nil {
		t.Fatalf("reading the response: %v", err)
	}

	peer := resp.TLS.PeerCertificates[0]
	if got := auth.SPKIFingerprint(peer); got != m.SPKISHA256 {
		t.Errorf("a client pinning the presented certificate computes %q, but pairing hands out %q",
			got, m.SPKISHA256)
	}
}

func TestEnsureTLS_DiscoversTheBoxAddressesWhenNoneAreGiven(t *testing.T) {
	st := testutil.NewMemStore()
	m, err := auth.EnsureTLS(context.Background(), st, auth.EnsureTLSOptions{})
	if err != nil {
		t.Fatalf("EnsureTLS: %v", err)
	}
	for _, ip := range m.Leaf.IPAddresses {
		if ip.IsLoopback() {
			t.Errorf("SAN %s is a loopback address; D-02 asks for the non-loopback ones", ip)
		}
	}
}

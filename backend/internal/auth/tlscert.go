package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"time"

	"github.com/FancyFunction/homesink/backend/internal/core"
	"github.com/FancyFunction/homesink/backend/internal/store"
)

// server_meta rows holding the TLS material. The key is what the client pins
// through, so it is persisted with the database and backed up alongside it:
// rotating it breaks every paired device (D-02).
const (
	MetaKeyTLSKey  = "tls_key_pem"
	MetaKeyTLSCert = "tls_cert_pem"
	MetaKeyTLSSPKI = "tls_spki"
)

// certValidity is the 10-year lifetime of D-02. A self-signed certificate the
// client pins by key does not benefit from expiring, and an expiry inside the
// server's service life would strand every paired phone.
const certValidity = 10 * 365 * 24 * time.Hour

// Certificate SANs that are always present (D-02). Every non-loopback IP on the
// box is added to these.
var defaultDNSNames = []string{"localhost", "homesink.local"}

// TLSMaterial is the server's identity on the wire.
type TLSMaterial struct {
	// Certificate is the key pair to hand to crypto/tls.
	Certificate tls.Certificate
	// Leaf is the parsed certificate, for callers that want its SANs or dates.
	Leaf *x509.Certificate
	// SPKISHA256 is the base64 SHA-256 of the SubjectPublicKeyInfo — the value
	// the pairing response hands the client, which pins it and nothing else
	// (D-02). It is a property of the key, so it survives a certificate
	// regenerated for new SANs and it survives a restart.
	SPKISHA256 string
	// Generated reports whether this call created the material rather than
	// loading it from the database.
	Generated bool
}

// TLSConfig returns a server TLS configuration for this material, negotiating
// HTTP/2 first (D-02).
func (m *TLSMaterial) TLSConfig() *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{m.Certificate},
		MinVersion:   tls.VersionTLS12,
		NextProtos:   []string{"h2", "http/1.1"},
	}
}

// EnsureTLSOptions configures EnsureTLS; the zero value is usable.
type EnsureTLSOptions struct {
	// Now is the clock; nil means time.Now.
	Now func() time.Time
	// IPs overrides the box's own addresses, for tests. Nil means discover
	// every non-loopback IP on the machine.
	IPs []net.IP
	// Logger receives generation events; nil discards them.
	Logger *slog.Logger
}

// EnsureTLS loads the server's TLS material from server_meta, generating it on
// first run: an EC P-256 self-signed certificate valid for ten years, with
// localhost, homesink.local and every non-loopback IP on the box as SANs (D-02).
//
// Restarting reloads the persisted key and certificate untouched, so the SPKI
// the client pinned at pairing time keeps matching for the life of the database.
func EnsureTLS(ctx context.Context, st store.Store, opts EnsureTLSOptions) (*TLSMaterial, error) {
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	keyPEM, keyErr := readMeta(ctx, st, MetaKeyTLSKey)
	certPEM, certErr := readMeta(ctx, st, MetaKeyTLSCert)
	if keyErr != nil {
		return nil, keyErr
	}
	if certErr != nil {
		return nil, certErr
	}
	if keyPEM != "" && certPEM != "" {
		m, err := materialFromPEM([]byte(certPEM), []byte(keyPEM))
		if err != nil {
			return nil, fmt.Errorf("auth: load persisted TLS material: %w", err)
		}
		log.InfoContext(ctx, "TLS material loaded",
			"spkiSha256", m.SPKISHA256, "notAfter", m.Leaf.NotAfter.Format(time.RFC3339))
		return m, nil
	}

	now := time.Now
	if opts.Now != nil {
		now = opts.Now
	}
	ips := opts.IPs
	if ips == nil {
		var err error
		if ips, err = localIPs(); err != nil {
			return nil, err
		}
	}

	newCertPEM, newKeyPEM, err := generateSelfSigned(now(), ips)
	if err != nil {
		return nil, err
	}
	m, err := materialFromPEM(newCertPEM, newKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("auth: parse generated TLS material: %w", err)
	}

	if err := st.SetMeta(ctx, MetaKeyTLSKey, string(newKeyPEM)); err != nil {
		return nil, fmt.Errorf("auth: persist TLS key: %w", err)
	}
	if err := st.SetMeta(ctx, MetaKeyTLSCert, string(newCertPEM)); err != nil {
		return nil, fmt.Errorf("auth: persist TLS certificate: %w", err)
	}
	if err := st.SetMeta(ctx, MetaKeyTLSSPKI, m.SPKISHA256); err != nil {
		return nil, fmt.Errorf("auth: persist TLS SPKI: %w", err)
	}

	m.Generated = true
	log.InfoContext(ctx, "TLS material generated",
		"spkiSha256", m.SPKISHA256,
		"dnsNames", m.Leaf.DNSNames,
		"ipAddresses", len(m.Leaf.IPAddresses),
		"notAfter", m.Leaf.NotAfter.Format(time.RFC3339))
	return m, nil
}

// SPKIFingerprint is the base64 SHA-256 of a certificate's SubjectPublicKeyInfo,
// the value an OkHttp CertificatePinner compares against (D-02).
func SPKIFingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return base64.StdEncoding.EncodeToString(sum[:])
}

// generateSelfSigned returns a PEM certificate and PKCS#8 private key.
func generateSelfSigned(now time.Time, ips []net.IP) (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("auth: generate EC key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, fmt.Errorf("auth: draw certificate serial: %w", err)
	}

	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "Homesink", Organization: []string{"Homesink"}},
		NotBefore:             now.Add(-time.Hour), // tolerate a slow clock on first boot
		NotAfter:              now.Add(certValidity),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		// Self-signed and its own trust anchor: a client that chooses to add
		// this certificate to its trust store needs it to be a valid root.
		IsCA:        true,
		DNSNames:    append([]string(nil), defaultDNSNames...),
		IPAddresses: ips,
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, fmt.Errorf("auth: create certificate: %w", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, fmt.Errorf("auth: marshal private key: %w", err)
	}

	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM, nil
}

// materialFromPEM assembles a TLSMaterial from a PEM pair.
func materialFromPEM(certPEM, keyPEM []byte) (*TLSMaterial, error) {
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, err
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, err
	}
	pair.Leaf = leaf
	return &TLSMaterial{Certificate: pair, Leaf: leaf, SPKISHA256: SPKIFingerprint(leaf)}, nil
}

// localIPs returns every non-loopback unicast IP configured on the box, so the
// certificate covers whatever address the phone reaches the server on (D-02).
func localIPs() ([]net.IP, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, fmt.Errorf("auth: list interface addresses: %w", err)
	}
	var out []net.IP
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok || ipnet.IP.IsLoopback() || ipnet.IP.IsLinkLocalUnicast() {
			continue
		}
		out = append(out, ipnet.IP)
	}
	return out, nil
}

// readMeta returns "" for an absent key rather than an error, so a first run
// and a corrupted read stay distinguishable.
func readMeta(ctx context.Context, st store.Store, key string) (string, error) {
	v, err := st.Meta(ctx, key)
	if errors.Is(err, core.ErrNotFound()) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("auth: read %s: %w", key, err)
	}
	return v, nil
}

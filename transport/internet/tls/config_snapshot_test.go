package tls

import (
	gotls "crypto/tls"
	"crypto/x509"
	"sync"
	"testing"

	certlib "github.com/xtls/xray-core/common/protocol/tls/cert"
)

func TestReloadableCertificateStoreSnapshotsConfig(t *testing.T) {
	generated, _ := certlib.MustGenerate(
		nil,
		certlib.CommonName("snapshot.example"),
		certlib.DNSNames("snapshot.example"),
		certlib.KeyUsage(x509.KeyUsageDigitalSignature),
	)
	entry := ParseCertificate(generated)
	entry.OneTimeLoading = true
	config := &Config{Certificate: []*Certificate{entry}}

	first := config.buildReloadableCertificates()
	second := config.buildReloadableCertificates()
	if len(first.certs) != 1 || len(second.certs) != 1 {
		t.Fatalf("snapshot sizes = %d/%d, want 1/1", len(first.certs), len(second.certs))
	}
	if first.certs[0] == second.certs[0] {
		t.Fatal("separate TLS configs share a mutable certificate pointer")
	}

	// Mutating the source protobuf after construction must not affect either
	// handshake snapshot.
	entry.Certificate[0] ^= 0xff
	certificate, err := first.getCertificateFunc(true)(&gotls.ClientHelloInfo{
		ServerName: "snapshot.example",
	})
	if err != nil {
		t.Fatalf("GetCertificate() error = %v", err)
	}
	if got := certificate.Leaf.Subject.CommonName; got != "snapshot.example" {
		t.Fatalf("certificate common name = %q, want snapshot.example", got)
	}
}

func TestReloadableCertificateStoreSynchronizesSelection(t *testing.T) {
	first := testKeyPair(t, "one.example")
	second := testKeyPair(t, "two.example")
	store := &reloadableCertificateStore{certs: []*gotls.Certificate{first}}
	getCertificate := store.getCertificateFunc(false)

	var readers sync.WaitGroup
	for range 8 {
		readers.Go(func() {
			for range 1000 {
				certificate, err := getCertificate(&gotls.ClientHelloInfo{})
				if err != nil || certificate == nil || certificate.Leaf == nil {
					t.Errorf("concurrent GetCertificate() = %v, %v", certificate, err)
					return
				}
			}
		})
	}
	for i := range 1000 {
		store.access.Lock()
		if i%2 == 0 {
			store.certs[0] = second
		} else {
			store.certs[0] = first
		}
		store.access.Unlock()
	}
	readers.Wait()
}

func testKeyPair(t *testing.T, name string) *gotls.Certificate {
	t.Helper()
	generated, _ := certlib.MustGenerate(
		nil,
		certlib.CommonName(name),
		certlib.DNSNames(name),
		certlib.KeyUsage(x509.KeyUsageDigitalSignature),
	)
	pair := loadX509KeyPair(ParseCertificate(generated))
	if pair == nil {
		t.Fatalf("loadX509KeyPair(%q) returned nil", name)
	}
	return pair
}

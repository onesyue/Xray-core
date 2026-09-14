package reality_test

import (
	"context"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	gotls "crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"testing"
	"time"

	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/transport/internet/reality"
)

// TestRealityServerAcceptsClassicalX25519ClientHello pins the REALITY server
// behaviour this fork depends on: a ClientHello that carries only a classical
// X25519 key share (no X25519MLKEM768) must still authenticate.
//
// github.com/xtls/reality 20260908 ("Reject outdated/strange Client Hello that
// doesn't have X25519MLKEM768 before optional X25519") makes such a hello fall
// through to the camouflage destination instead. Measured 2026-09-14 against
// that revision: chrome/firefox/safari (auto) still authenticate, while
// hellochrome_120, hellofirefox_120, ios, edge and random are rejected; on the
// 20260322 revision every one of them authenticates. mihomo (the YueLink core)
// strips X25519MLKEM768 from its hello unless `support-x25519mlkem768: true`
// is set per proxy, which the panel's subscriptions do not emit, so adopting
// the newer reality revision would lock those clients out of every REALITY
// node. go.mod therefore keeps reality at 20260322 until the client side is
// proven; this test turns a silent bump into a red test.
func TestRealityServerAcceptsClassicalX25519ClientHello(t *testing.T) {
	const sni = "www.example.com"
	destLn, err := gotls.Listen("tcp", "127.0.0.1:0", &gotls.Config{
		Certificates: []gotls.Certificate{selfSignedCert(t, sni)},
		MinVersion:   gotls.VersionTLS13,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer destLn.Close()
	go serveAndDiscard(destLn)

	priv := make([]byte, 32)
	if _, err := rand.Read(priv); err != nil {
		t.Fatal(err)
	}
	key, err := ecdh.X25519().NewPrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	shortID := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	serverCfg := &reality.Config{
		Dest:        destLn.Addr().String(),
		Type:        "tcp",
		ServerNames: []string{sni},
		PrivateKey:  priv,
		ShortIds:    [][]byte{shortID},
	}
	srvLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer srvLn.Close()
	go func() {
		for {
			c, err := srvLn.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				rc, err := reality.Server(c, serverCfg.GetREALITYConfig())
				if err != nil {
					return
				}
				_ = rc.SetDeadline(time.Now().Add(5 * time.Second))
				_, _ = rc.Read(make([]byte, 16))
			}(c)
		}
	}()
	dest := xnet.TCPDestination(xnet.ParseAddress("127.0.0.1"), xnet.Port(srvLn.Addr().(*net.TCPAddr).Port))

	cases := []struct {
		fingerprint string
		mlkem       bool
	}{
		{"chrome", true},           // modern hello: X25519MLKEM768 then X25519
		{"hellochrome_120", false}, // classical only, same shape as mihomo without support-x25519mlkem768
		{"hellofirefox_120", false},
		{"ios", false},
	}
	for _, tc := range cases {
		t.Run(tc.fingerprint, func(t *testing.T) {
			raw, err := net.Dial("tcp", srvLn.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			conn, err := reality.UClient(raw, &reality.Config{
				ServerName:  sni,
				Fingerprint: tc.fingerprint,
				PublicKey:   key.PublicKey().Bytes(),
				ShortId:     shortID,
			}, ctx, dest)
			if err != nil {
				t.Fatalf("fingerprint %s (X25519MLKEM768 key share: %v) was not authenticated by the REALITY server and fell through to the destination: %v", tc.fingerprint, tc.mlkem, err)
			}
			conn.Close()
		})
	}
}

func selfSignedCert(t *testing.T, host string) gotls.Certificate {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: host},
		DNSNames:     []string{host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	return gotls.Certificate{Certificate: [][]byte{der}, PrivateKey: priv}
}

func serveAndDiscard(ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go func(c net.Conn) {
			defer c.Close()
			_ = c.SetDeadline(time.Now().Add(5 * time.Second))
			_, _ = c.Read(make([]byte, 4096))
		}(c)
	}
}

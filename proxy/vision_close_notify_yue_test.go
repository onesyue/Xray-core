package proxy

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	gotls "crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	gonet "net"
	"sync"
	"testing"
	"time"

	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/transport/internet/stat"
	"github.com/xtls/xray-core/transport/internet/tls"
)

// [YUE] Regression for upstream e5e85ca9 (#6834). Once Vision switches a
// direction to direct copy, plaintext goes straight onto the raw socket and the
// outer TLS layer's record sequence no longer matches what the peer has seen.
// Closing the outer TLS conn then wrote an encrypted close_notify alert under
// the stale keys, which the client's TLS stack rejected as bad_record_mac. The
// tests below run a real crypto/tls handshake over loopback TCP, wrap the
// server's TLS conn in the stats wrapper the VLESS inbound hands to Vision, flip
// one direction through the real VisionWriter / VisionReader switch, then
// close and count what the outer layer still wrote to the raw socket.

// rawWriteRecorder counts bytes written to the raw socket after mark().
type rawWriteRecorder struct {
	gonet.Conn
	mu      sync.Mutex
	marked  bool
	written int
}

func (r *rawWriteRecorder) Write(p []byte) (int, error) {
	n, err := r.Conn.Write(p)
	r.mu.Lock()
	if r.marked {
		r.written += n
	}
	r.mu.Unlock()
	return n, err
}

func (r *rawWriteRecorder) mark() {
	r.mu.Lock()
	r.marked, r.written = true, 0
	r.mu.Unlock()
}

func (r *rawWriteRecorder) bytesSinceMark() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.written
}

func visionTestCert(t *testing.T) gotls.Certificate {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "vision.test"},
		DNSNames:     []string{"vision.test"},
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

// handshakenServer returns the server side as Vision sees it (stats wrapper
// over the xray tls.Conn over a recorder over loopback TCP) plus the client.
func handshakenServer(t *testing.T) (gonet.Conn, *rawWriteRecorder, *gotls.Conn) {
	t.Helper()
	ln, err := gonet.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan gonet.Conn, 1)
	go func() {
		c, _ := ln.Accept()
		accepted <- c
	}()
	rawClient, err := gonet.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	rawServer := <-accepted
	if rawServer == nil {
		t.Fatal("accept failed")
	}
	rec := &rawWriteRecorder{Conn: rawServer}
	server := tls.Server(rec, &gotls.Config{
		Certificates: []gotls.Certificate{visionTestCert(t)},
		MinVersion:   gotls.VersionTLS13,
	})
	client := gotls.Client(rawClient, &gotls.Config{ServerName: "vision.test", InsecureSkipVerify: true})
	t.Cleanup(func() { client.Close(); server.Close() })
	deadline := time.Now().Add(5 * time.Second)
	_ = rawClient.SetDeadline(deadline)
	_ = rawServer.SetDeadline(deadline)
	errc := make(chan error, 1)
	go func() { errc <- server.(*tls.Conn).Handshake() }()
	if err := client.Handshake(); err != nil {
		t.Fatalf("client handshake: %v", err)
	}
	if err := <-errc; err != nil {
		t.Fatalf("server handshake: %v", err)
	}
	return &stat.CounterConnection{Connection: server}, rec, client
}

// Control: without a direct-copy switch the outer TLS layer does write a
// close_notify record, so a zero below really means "suppressed".
func TestVisionOuterTLSCloseWritesCloseNotifyWithoutDirectCopy(t *testing.T) {
	conn, rec, _ := handshakenServer(t)
	rec.mark()
	_ = conn.Close()
	if rec.bytesSinceMark() == 0 {
		t.Fatal("outer TLS close wrote nothing without direct copy; the recorder is not observing the raw socket")
	}
}

func TestVisionWriterDirectCopySuppressesOuterCloseNotify(t *testing.T) {
	conn, rec, _ := handshakenServer(t)
	ts := NewTrafficState([]byte("0123456789abcdef"))
	ts.Inbound.IsPadding = false
	ts.Inbound.DownlinkWriterDirectCopy = true
	w := NewVisionWriter(buf.Discard, ts, false, context.Background(), conn, nil, nil)
	if err := w.WriteMultiBuffer(buf.MultiBuffer{}); err != nil {
		t.Fatalf("VisionWriter switch: %v", err)
	}
	if ts.Inbound.DownlinkWriterDirectCopy {
		t.Fatal("VisionWriter did not take the direct-copy switch")
	}
	rec.mark()
	_ = conn.Close()
	if n := rec.bytesSinceMark(); n != 0 {
		t.Fatalf("outer TLS close wrote %d bytes to the raw socket after Vision switched the downlink to direct copy; the client sees bad_record_mac", n)
	}
}

func TestVisionReaderDirectCopySuppressesOuterCloseNotify(t *testing.T) {
	conn, rec, _ := handshakenServer(t)
	uuid := []byte("0123456789abcdef")
	ts := NewTrafficState(uuid)
	ts.NumberOfPacketToFilter = 0

	// What a Vision client sends when it switches its uplink: the UUID-led
	// padding block carrying CommandPaddingDirect.
	writerUUID := append([]byte(nil), uuid...)
	payload := buf.New()
	payload.Write([]byte{0x17, 0x03, 0x03, 0x00, 0x01, 0x00})
	padded := XtlsPadding(payload, CommandPaddingDirect, &writerUUID, false, context.Background(), []uint32{0, 0, 0, 0})

	r := NewVisionReader(&singleMultiBufferReader{mb: buf.MultiBuffer{padded}}, ts, true,
		context.Background(), conn, &bytes.Reader{}, &bytes.Buffer{}, nil)
	mb, err := r.ReadMultiBuffer()
	buf.ReleaseMulti(mb)
	if err != nil {
		t.Fatalf("VisionReader: %v", err)
	}
	if !ts.Inbound.UplinkReaderDirectCopy {
		t.Fatal("VisionReader did not take the direct-copy switch")
	}
	rec.mark()
	_ = conn.Close()
	if n := rec.bytesSinceMark(); n != 0 {
		t.Fatalf("outer TLS close wrote %d bytes to the raw socket after Vision switched the uplink to direct copy; the client sees bad_record_mac", n)
	}
}

type singleMultiBufferReader struct{ mb buf.MultiBuffer }

func (r *singleMultiBufferReader) ReadMultiBuffer() (buf.MultiBuffer, error) {
	mb := r.mb
	r.mb = nil
	return mb, nil
}

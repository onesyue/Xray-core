package reality_test

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	gotls "crypto/tls"
	"net"
	"sync"
	"testing"
	"time"

	goreality "github.com/xtls/reality"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/proxy"
	"github.com/xtls/xray-core/transport/internet/reality"
	"github.com/xtls/xray-core/transport/internet/stat"
)

// [YUE] Regression for upstream e5e85ca9 (#6834) on the REALITY wrappers the
// production VLESS role actually uses: reality.Conn (server, yue-node inbound)
// and reality.UConn (client, xray-probe / VLESS outbound). After Vision has
// switched to direct copy, proxy.SuppressOuterCloseNotify must make Close go
// straight to the raw socket instead of writing a close_notify record under
// keys the peer has already abandoned (client symptom: bad_record_mac). The
// "control" subtests prove the recorder does see the alert when nothing is
// suppressed, so a zero is meaningful.

type closeRecorder struct {
	net.Conn
	mu      sync.Mutex
	marked  bool
	written int
}

func (r *closeRecorder) Write(p []byte) (int, error) {
	n, err := r.Conn.Write(p)
	r.mu.Lock()
	if r.marked {
		r.written += n
	}
	r.mu.Unlock()
	return n, err
}

// CloseWrite is required by reality.Server (it half-closes toward the dest on
// fallback); the raw conn is always a *net.TCPConn here.
func (r *closeRecorder) CloseWrite() error {
	return r.Conn.(*net.TCPConn).CloseWrite()
}

func (r *closeRecorder) mark() {
	r.mu.Lock()
	r.marked, r.written = true, 0
	r.mu.Unlock()
}

func (r *closeRecorder) bytesSinceMark() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.written
}

type realityPair struct {
	server    net.Conn // *reality.Conn behind a stats wrapper, as Vision sees it
	serverRaw *closeRecorder
	client    net.Conn // *reality.UConn behind a stats wrapper
	clientRaw *closeRecorder
}

func newRealityPair(t *testing.T) realityPair {
	t.Helper()
	const sni = "www.example.com"
	destLn, err := gotls.Listen("tcp", "127.0.0.1:0", &gotls.Config{
		Certificates: []gotls.Certificate{selfSignedCert(t, sni)},
		MinVersion:   gotls.VersionTLS13,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { destLn.Close() })
	go serveAndDiscard(destLn)

	priv := make([]byte, 32)
	if _, err := rand.Read(priv); err != nil {
		t.Fatal(err)
	}
	key, err := ecdh.X25519().NewPrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	// reality.Server only marks the handshake complete once it knows the
	// dest's post-handshake record lengths (normally learned by a background
	// probe, polled every 5s). An empty list means "none": the loopback dest
	// sends no session tickets worth mimicking here.
	for _, alpn := range []string{" 0", " 1", " 2"} {
		goreality.GlobalPostHandshakeRecordsLens.Store(destLn.Addr().String()+" "+sni+alpn, []int{})
	}
	shortID := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	serverCfg := (&reality.Config{
		Dest:        destLn.Addr().String(),
		Type:        "tcp",
		ServerNames: []string{sni},
		PrivateKey:  priv,
		ShortIds:    [][]byte{shortID},
	}).GetREALITYConfig()

	srvLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srvLn.Close() })
	type result struct {
		conn net.Conn
		raw  *closeRecorder
		err  error
	}
	served := make(chan result, 1)
	go func() {
		c, err := srvLn.Accept()
		if err != nil {
			served <- result{err: err}
			return
		}
		_ = c.SetDeadline(time.Now().Add(5 * time.Second))
		rec := &closeRecorder{Conn: c}
		rc, err := reality.Server(rec, serverCfg)
		served <- result{conn: rc, raw: rec, err: err}
	}()

	raw, err := net.Dial("tcp", srvLn.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	_ = raw.SetDeadline(time.Now().Add(5 * time.Second))
	clientRaw := &closeRecorder{Conn: raw}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	dest := xnet.TCPDestination(xnet.ParseAddress("127.0.0.1"), xnet.Port(srvLn.Addr().(*net.TCPAddr).Port))
	client, err := reality.UClient(clientRaw, &reality.Config{
		ServerName:  sni,
		Fingerprint: "chrome",
		PublicKey:   key.PublicKey().Bytes(),
		ShortId:     shortID,
	}, ctx, dest)
	if err != nil {
		t.Fatalf("REALITY client handshake: %v", err)
	}
	srv := <-served
	if srv.err != nil {
		t.Fatalf("REALITY server handshake: %v", srv.err)
	}
	if _, ok := srv.conn.(*reality.Conn); !ok {
		t.Fatalf("reality.Server returned %T, want *reality.Conn", srv.conn)
	}
	if _, ok := client.(*reality.UConn); !ok {
		t.Fatalf("reality.UClient returned %T, want *reality.UConn", client)
	}
	t.Cleanup(func() { client.Close(); srv.conn.Close() })
	return realityPair{
		server:    &stat.CounterConnection{Connection: srv.conn},
		serverRaw: srv.raw,
		client:    &stat.CounterConnection{Connection: client},
		clientRaw: clientRaw,
	}
}

func TestRealityCloseAfterDirectCopyWritesNothingToRawSocket(t *testing.T) {
	sides := []struct {
		name string
		pick func(realityPair) (net.Conn, *closeRecorder)
	}{
		{"server reality.Conn", func(p realityPair) (net.Conn, *closeRecorder) { return p.server, p.serverRaw }},
		{"client reality.UConn", func(p realityPair) (net.Conn, *closeRecorder) { return p.client, p.clientRaw }},
	}
	for _, side := range sides {
		t.Run(side.name+"/control", func(t *testing.T) {
			conn, rec := side.pick(newRealityPair(t))
			rec.mark()
			_ = conn.Close()
			if rec.bytesSinceMark() == 0 {
				t.Fatal("close without suppression wrote nothing; the recorder is not observing the raw socket")
			}
		})
		t.Run(side.name+"/suppressed", func(t *testing.T) {
			conn, rec := side.pick(newRealityPair(t))
			proxy.SuppressOuterCloseNotify(conn)
			rec.mark()
			_ = conn.Close()
			if n := rec.bytesSinceMark(); n != 0 {
				t.Fatalf("close after direct-copy suppression wrote %d bytes (a close_notify under stale keys) to the raw socket", n)
			}
		})
	}
}

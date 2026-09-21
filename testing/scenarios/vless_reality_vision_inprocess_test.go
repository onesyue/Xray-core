package scenarios

import (
	"bytes"
	gotls "crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"io"
	gonet "net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xtls/xray-core/app/proxyman"
	"github.com/xtls/xray-core/common"
	clog "github.com/xtls/xray-core/common/log"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/protocol/tls/cert"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/common/uuid"
	core "github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/proxy/dokodemo"
	"github.com/xtls/xray-core/proxy/freedom"
	"github.com/xtls/xray-core/proxy/vless"
	"github.com/xtls/xray-core/proxy/vless/inbound"
	"github.com/xtls/xray-core/proxy/vless/outbound"
	"github.com/xtls/xray-core/testing/servers/tcp"
	"github.com/xtls/xray-core/transport/internet"
	"github.com/xtls/xray-core/transport/internet/reality"
	transtcp "github.com/xtls/xray-core/transport/internet/tcp"
)

// [YUE] TestVlessVisionRealityInProcessDirectCopy is the end-to-end test whose
// absence let yue-node 743c0876 reach the canary: onesyue/REALITY
// v0.0.0-yue.2 changed reality.Conn.rawInput to *bytes.Buffer, and the VLESS
// Vision path (proxy/vless/{inbound,outbound}) reads Conn.input/rawInput
// through reflect field offsets + unsafe.Pointer. The canary Reality slot died
// with "fatal error: fault" ~10s after start.
//
// Unlike TestVlessXtlsVisionReality it needs no Internet (the REALITY target
// is a local TLS 1.3 server) and runs both Xray instances in this process, so
// a memory fault fails `go test` directly. The proxied payload is itself TLS
// 1.3, which is what makes Vision issue its direct-copy command and actually
// drain the outer connection's input/rawInput through the unsafe pointers on
// BOTH the server (reality.Conn) and the client (utls.Conn) side.
func TestVlessVisionRealityInProcessDirectCopy(t *testing.T) {
	// REALITY "dest": a local TLS 1.3 server the REALITY server borrows its
	// handshake shape from.
	realityDest := startTLSServer(t, "reality-dest.test", func(c gonet.Conn) { io.Copy(io.Discard, c) })

	// The proxied service: a TLS 1.3 echo server behind the tunnel.
	echo := startTLSServer(t, "echo.test", func(c gonet.Conn) { io.Copy(c, c) })

	userID := protocol.NewID(uuid.New())
	serverPort := tcp.PickPort()
	privateKey, _ := base64.RawURLEncoding.DecodeString("aGSYystUbf59_9_6LKRxD27rmSW_-2_nyd9YG_Gwbks")
	publicKey, _ := base64.RawURLEncoding.DecodeString("E59WjnvZcQMu7tR7_BgyhycuEdBS-CtKxfImRCdAvFM")
	shortID := make([]byte, 8)
	hex.Decode(shortID, []byte("0123456789abcdef"))

	serverConfig := &core.Config{
		Inbound: []*core.InboundHandlerConfig{{
			ReceiverSettings: serial.ToTypedMessage(&proxyman.ReceiverConfig{
				PortList: &net.PortList{Range: []*net.PortRange{net.SinglePortRange(serverPort)}},
				Listen:   net.NewIPOrDomain(net.LocalHostIP),
				StreamSettings: &internet.StreamConfig{
					ProtocolName: "tcp",
					SecurityType: serial.GetMessageType(&reality.Config{}),
					SecuritySettings: []*serial.TypedMessage{serial.ToTypedMessage(&reality.Config{
						Dest:        realityDest.String(),
						Type:        "tcp",
						ServerNames: []string{"reality-dest.test"},
						PrivateKey:  privateKey,
						ShortIds:    [][]byte{shortID},
					})},
				},
			}),
			ProxySettings: serial.ToTypedMessage(&inbound.Config{
				Users: []*protocol.User{{Account: serial.ToTypedMessage(&vless.Account{Id: userID.String(), Flow: vless.XRV})}},
			}),
		}},
		Outbound: []*core.OutboundHandlerConfig{{
			ProxySettings: serial.ToTypedMessage(&freedom.Config{
				FinalRules: []*freedom.FinalRuleConfig{{Action: freedom.RuleAction_Allow}},
			}),
		}},
	}

	clientPort := tcp.PickPort()
	clientConfig := &core.Config{
		Inbound: []*core.InboundHandlerConfig{{
			ReceiverSettings: serial.ToTypedMessage(&proxyman.ReceiverConfig{
				PortList: &net.PortList{Range: []*net.PortRange{net.SinglePortRange(clientPort)}},
				Listen:   net.NewIPOrDomain(net.LocalHostIP),
			}),
			ProxySettings: serial.ToTypedMessage(&dokodemo.Config{
				RewriteAddress:  net.NewIPOrDomain(net.LocalHostIP),
				RewritePort:     uint32(echo.Port),
				AllowedNetworks: []net.Network{net.Network_TCP},
			}),
		}},
		Outbound: []*core.OutboundHandlerConfig{{
			ProxySettings: serial.ToTypedMessage(&outbound.Config{
				Vnext: &protocol.ServerEndpoint{
					Address: net.NewIPOrDomain(net.LocalHostIP),
					Port:    uint32(serverPort),
					User:    &protocol.User{Account: serial.ToTypedMessage(&vless.Account{Id: userID.String(), Flow: vless.XRV})},
				},
			}),
			SenderSettings: serial.ToTypedMessage(&proxyman.SenderConfig{
				StreamSettings: &internet.StreamConfig{
					ProtocolName:      "tcp",
					TransportSettings: []*internet.TransportConfig{{ProtocolName: "tcp", Settings: serial.ToTypedMessage(&transtcp.Config{})}},
					SecurityType:      serial.GetMessageType(&reality.Config{}),
					SecuritySettings: []*serial.TypedMessage{serial.ToTypedMessage(&reality.Config{
						Fingerprint: "chrome",
						ServerName:  "reality-dest.test",
						PublicKey:   publicKey,
						ShortId:     shortID,
						SpiderX:     "/",
					})},
				},
			}),
		}},
	}

	// No app/log in either config, so this handler keeps receiving every
	// record: it is how the test proves Vision really switched to direct
	// copy (and therefore really drained input/rawInput via unsafe) instead
	// of passing without ever reaching the path it exists for.
	directCopies := &countingLogHandler{needle: "CopyRawConn"}
	clog.RegisterHandler(directCopies)

	for _, cfg := range []*core.Config{serverConfig, clientConfig} {
		instance, err := core.New(withDefaultApps(cfg))
		common.Must(err)
		common.Must(instance.Start())
		defer instance.Close()
	}

	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- tlsEchoRoundTrips(clientPort, 64, 16*1024)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	// 4 connections x 2 directions switch to raw copy; require at least one
	// per connection so a Vision change that stops switching cannot turn
	// this test into a plain echo test.
	if n := directCopies.n.Load(); n < 4 {
		t.Fatalf("Vision switched to direct copy %d times, want >= 4: the input/rawInput path was not exercised", n)
	}
}

type countingLogHandler struct {
	needle string
	n      atomic.Int64
}

func (h *countingLogHandler) Handle(msg clog.Message) {
	if strings.Contains(msg.String(), h.needle) {
		h.n.Add(1)
	}
}

func startTLSServer(t *testing.T, name string, handle func(gonet.Conn)) *gonet.TCPAddr {
	t.Helper()
	c, _ := cert.MustGenerate(nil, cert.DNSNames(name), cert.CommonName(name))
	certPEM, keyPEM := c.ToPEM()
	pair, err := gotls.X509KeyPair(certPEM, keyPEM)
	common.Must(err)
	ln, err := gotls.Listen("tcp", "127.0.0.1:0", &gotls.Config{
		Certificates: []gotls.Certificate{pair},
		MinVersion:   gotls.VersionTLS13,
	})
	common.Must(err)
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				handle(conn)
			}()
		}
	}()
	return ln.Addr().(*gonet.TCPAddr)
}

// tlsEchoRoundTrips speaks TLS 1.3 through the tunnel and verifies rounds
// echoes of size bytes each.
func tlsEchoRoundTrips(port net.Port, rounds, size int) error {
	raw, err := gonet.DialTimeout("tcp", gonet.JoinHostPort("127.0.0.1", port.String()), 5*time.Second)
	if err != nil {
		return err
	}
	defer raw.Close()
	raw.SetDeadline(time.Now().Add(20 * time.Second))
	conn := gotls.Client(raw, &gotls.Config{ServerName: "echo.test", InsecureSkipVerify: true, MinVersion: gotls.VersionTLS13})
	if err := conn.Handshake(); err != nil {
		return err
	}
	payload := make([]byte, size)
	got := make([]byte, size)
	for i := range rounds {
		for j := range payload {
			payload[j] = byte(i*31 + j)
		}
		if _, err := conn.Write(payload); err != nil {
			return err
		}
		if _, err := io.ReadFull(conn, got); err != nil {
			return err
		}
		if !bytes.Equal(got, payload) {
			return io.ErrShortBuffer
		}
	}
	return nil
}

package inbound

import (
	"context"
	"io"
	stdnet "net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	xraynet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/serial"
	featureinbound "github.com/xtls/xray-core/features/inbound"
	"github.com/xtls/xray-core/features/routing"
	"github.com/xtls/xray-core/proxy"
	"github.com/xtls/xray-core/transport/internet/stat"
)

type drainEchoInbound struct {
	started chan struct{}
	once    sync.Once
	closed  atomic.Bool
}

func (p *drainEchoInbound) Network() []xraynet.Network {
	return []xraynet.Network{xraynet.Network_TCP}
}

func (p *drainEchoInbound) Process(_ context.Context, _ xraynet.Network, conn stat.Connection, _ routing.Dispatcher) error {
	p.once.Do(func() { close(p.started) })
	var payload [64]byte
	for {
		n, err := conn.Read(payload[:])
		if n > 0 {
			if _, writeErr := conn.Write(payload[:n]); writeErr != nil {
				return writeErr
			}
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

func (p *drainEchoInbound) Close() error {
	p.closed.Store(true)
	return nil
}

var _ proxy.Inbound = (*drainEchoInbound)(nil)

func TestTCPWorkerStopAcceptingKeepsAcceptedConnectionAndProtocolState(t *testing.T) {
	probe, err := stdnet.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve loopback port: %v", err)
	}
	port := probe.Addr().(*stdnet.TCPAddr).Port
	if err := probe.Close(); err != nil {
		t.Fatalf("release loopback port: %v", err)
	}

	protocol := &drainEchoInbound{started: make(chan struct{})}
	worker := &tcpWorker{
		address: xraynet.LocalHostIP,
		port:    xraynet.Port(port),
		proxy:   protocol,
		ctx:     context.Background(),
	}
	if err := worker.Start(); err != nil {
		t.Fatalf("tcpWorker.Start() error = %v", err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = worker.Close()
		}
	}()
	address := worker.hub.Addr().String()

	connection, err := stdnet.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatalf("dial listener: %v", err)
	}
	defer connection.Close()
	select {
	case <-protocol.started:
	case <-time.After(time.Second):
		t.Fatal("accepted connection did not reach inbound protocol")
	}
	assertEcho(t, connection, "before-drain")

	if err := worker.StopAccepting(); err != nil {
		t.Fatalf("first StopAccepting() error = %v", err)
	}
	if err := worker.StopAccepting(); err != nil {
		t.Fatalf("idempotent StopAccepting() error = %v", err)
	}
	if protocol.closed.Load() {
		t.Fatal("StopAccepting closed protocol/validator state")
	}
	assertEcho(t, connection, "after-drain")
	if newConnection, err := stdnet.DialTimeout("tcp", address, 100*time.Millisecond); err == nil {
		newConnection.Close()
		t.Fatal("retired listener accepted a new connection")
	}

	if err := connection.Close(); err != nil {
		t.Fatalf("close accepted connection: %v", err)
	}
	if err := worker.Close(); err != nil {
		t.Fatalf("final worker.Close() error = %v", err)
	}
	closed = true
	if !protocol.closed.Load() {
		t.Fatal("final Close did not release protocol/validator state")
	}
}

func assertEcho(t *testing.T, connection stdnet.Conn, payload string) {
	t.Helper()
	if err := connection.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("set connection deadline: %v", err)
	}
	if _, err := connection.Write([]byte(payload)); err != nil {
		t.Fatalf("write %q: %v", payload, err)
	}
	response := make([]byte, len(payload))
	if _, err := io.ReadFull(connection, response); err != nil {
		t.Fatalf("read %q: %v", payload, err)
	}
	if string(response) != payload {
		t.Fatalf("echo response = %q, want %q", response, payload)
	}
}

type drainTestHandler struct {
	tag     string
	started atomic.Int32
	drained atomic.Int32
	closed  atomic.Int32
}

func (h *drainTestHandler) Start() error {
	h.started.Add(1)
	return nil
}

func (h *drainTestHandler) Close() error {
	h.closed.Add(1)
	return nil
}

func (h *drainTestHandler) StopAccepting() error {
	h.drained.Add(1)
	return nil
}
func (h *drainTestHandler) Tag() string                          { return h.tag }
func (*drainTestHandler) ReceiverSettings() *serial.TypedMessage { return nil }
func (*drainTestHandler) ProxySettings() *serial.TypedMessage    { return nil }

func TestManagerRetainsDrainedHandlerUntilFinalClose(t *testing.T) {
	ctx := context.Background()
	manager, err := New(ctx, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	var _ featureinbound.DrainingManager = manager

	handler := &drainTestHandler{tag: "vless"}
	if err := manager.AddHandler(ctx, handler); err != nil {
		t.Fatalf("AddHandler() error = %v", err)
	}
	if err := manager.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := manager.StopAccepting(ctx, handler.tag); err != nil {
		t.Fatalf("StopAccepting() error = %v", err)
	}
	if err := manager.StopAccepting(ctx, handler.tag); err != nil {
		t.Fatalf("idempotent StopAccepting() error = %v", err)
	}
	if got := handler.drained.Load(); got != 1 {
		t.Fatalf("StopAccepting calls = %d, want 1", got)
	}
	if got := handler.closed.Load(); got != 0 {
		t.Fatalf("Close calls before final close = %d, want 0", got)
	}
	if _, err := manager.GetHandler(ctx, handler.tag); err == nil {
		t.Fatal("retired handler remained discoverable")
	}
	if got := len(manager.ListHandlers(ctx)); got != 0 {
		t.Fatalf("ListHandlers length after retirement = %d, want 0", got)
	}
	if err := manager.AddHandler(ctx, &drainTestHandler{tag: handler.tag}); err == nil {
		t.Fatal("manager allowed tag reuse while old handler was draining")
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if got := handler.closed.Load(); got != 1 {
		t.Fatalf("final Close calls = %d, want 1", got)
	}
}

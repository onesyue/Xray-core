package wireguard

import (
	"net/netip"
	"testing"
	"time"
)

// Closing the gVisor-backed TUN while the stack is delivering an outbound packet
// through WriteNotify must not panic. Before dbb1ea30 Close() closed
// incomingPacket while WriteNotify could still be blocked sending on it (nobody
// was reading the device), which is a "send on closed channel" panic that takes
// the whole process down — reachable on every Xray hot reload of a node that has
// a WireGuard outbound with an in-flight DNS lookup.
func TestNetTunCloseWithWriteInFlightDoesNotPanic(t *testing.T) {
	dev, tnet, _, err := CreateNetTUN([]netip.Addr{netip.MustParseAddr("10.9.0.2")}, nil, 1420, false)
	if err != nil {
		t.Fatalf("CreateNetTUN: %v", err)
	}
	conn, err := tnet.DialUDPAddrPort(netip.AddrPort{}, netip.MustParseAddrPort("10.9.0.1:53"))
	if err != nil {
		t.Fatalf("DialUDPAddrPort: %v", err)
	}
	defer conn.Close()

	// No reader drains dev.Read, so the stack's WriteNotify blocks on the
	// unbuffered incomingPacket channel inside this Write.
	writeDone := make(chan struct{})
	go func() {
		defer close(writeDone)
		_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		_, _ = conn.Write([]byte("in-flight"))
	}()

	// The write must be parked in WriteNotify; if it already returned, the
	// scenario was not exercised and the test proves nothing.
	select {
	case <-writeDone:
		t.Fatal("write returned before Close; WriteNotify never blocked")
	case <-time.After(200 * time.Millisecond):
	}

	if err := dev.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case <-writeDone:
	case <-time.After(5 * time.Second):
		t.Fatal("in-flight write did not return after Close")
	}

	// Read after Close reports closure instead of blocking or panicking.
	bufs := [][]byte{make([]byte, 1500)}
	sizes := []int{0}
	if _, err := dev.Read(bufs, sizes, 0); err == nil {
		t.Fatal("Read after Close must fail")
	}
}

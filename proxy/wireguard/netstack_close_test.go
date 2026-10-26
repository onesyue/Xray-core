package wireguard

import (
	"net/netip"
	"testing"
	"time"
)

// Closing the userspace TUN while a packet is queued for the device must not
// panic. Upstream v26.10.21 replaced the gVisor stack (#7130, #7156), so the
// old WriteNotify/incomingPacket panic (dbb1ea30) is gone with that stack.
// The replacement blocks in WritePacket only when netTunQueue is full, and
// Close must wake that sender via the closed channel instead of sending on a
// closed channel. That is the hot-reload path for a WireGuard outbound.
func TestNetTunCloseWithWriteInFlightDoesNotPanic(t *testing.T) {
	dev, _, err := CreateNetTUN([]netip.Addr{netip.MustParseAddr("10.9.0.2")}, nil, 1420, nil)
	if err != nil {
		t.Fatalf("CreateNetTUN: %v", err)
	}
	nt := dev.(*netTun)

	for i := 0; i < netTunQueue; i++ {
		if err := nt.WritePacket(nil, []byte("queued")); err != nil {
			t.Fatalf("fill queue: %v", err)
		}
	}

	writeDone := make(chan error, 1)
	go func() {
		writeDone <- nt.WritePacket(nil, []byte("in-flight"))
	}()

	// The extra write must be parked on the full queue. If it already
	// returned, Close did not race a blocked sender and the test proves nothing.
	select {
	case err := <-writeDone:
		t.Fatalf("write returned before Close (%v); WritePacket never blocked", err)
	case <-time.After(200 * time.Millisecond):
	}

	if err := dev.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case err := <-writeDone:
		if err == nil {
			t.Fatal("blocked WritePacket succeeded after Close")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("in-flight write did not return after Close")
	}

	// Queued packets may still be readable. Once they are drained, Read
	// reports closure instead of blocking or panicking.
	bufs := [][]byte{make([]byte, 1500)}
	sizes := []int{0}
	for {
		if _, err := dev.Read(bufs, sizes, 0); err != nil {
			break
		}
	}
}

//go:build linux

package proxy

import (
	"bytes"
	"context"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xtls/xray-core/app/dispatcher"
	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/common/signal"
)

type spliceTestCounter struct {
	value atomic.Int64
}

func (c *spliceTestCounter) Value() int64      { return c.value.Load() }
func (c *spliceTestCounter) Set(v int64) int64 { return c.value.Swap(v) }
func (c *spliceTestCounter) Add(v int64) int64 { return c.value.Add(v) }

// TestCopyRawConnIfExistAccountsDirectionalCounterBeforeEOF exercises real
// Linux TCP pairs in both logical directions. The source remains open after a
// full chunk, proving accounting is visible before a long-lived Vision stream
// reaches EOF.
func TestCopyRawConnIfExistAccountsDirectionalCounterBeforeEOF(t *testing.T) {
	t.Run("downlink", func(t *testing.T) {
		downlink := &spliceTestCounter{}
		uplink := &spliceTestCounter{}
		testRawCopyCounterBeforeEOF(t, downlink, downlink, uplink)
	})
	t.Run("uplink", func(t *testing.T) {
		uplink := &spliceTestCounter{}
		downlink := &spliceTestCounter{}
		testRawCopyCounterBeforeEOF(t, uplink, downlink, downlink)
	})
}

func testRawCopyCounterBeforeEOF(t *testing.T, writerCounter, sessionDownlinkCounter, untouchedCounter *spliceTestCounter) {
	t.Helper()
	sourceWriter, sourceReader := tcpPair(t)
	destinationWriter, destinationReader := tcpPair(t)
	defer sourceWriter.Close()
	defer sourceReader.Close()
	defer destinationWriter.Close()
	defer destinationReader.Close()

	legacyCounter := &spliceTestCounter{}
	writer := &dispatcher.AccountingWriter{
		Counter: writerCounter,
		Writer: &dispatcher.SizeStatWriter{
			Counter: legacyCounter,
			Writer:  buf.Discard,
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	inbound := &session.Inbound{
		Conn:                destinationWriter,
		CanSpliceCopy:       1,
		UserDownlinkCounter: sessionDownlinkCounter,
	}
	ctx = session.ContextWithInbound(ctx, inbound)
	ctx = session.ContextWithOutbounds(ctx, []*session.Outbound{{CanSpliceCopy: 1}})
	timer := signal.CancelAfterInactivity(ctx, cancel, time.Minute)
	defer timer.SetTimeout(0)

	drainDone := make(chan error, 1)
	go func() {
		_, err := io.Copy(io.Discard, destinationReader)
		drainDone <- err
	}()
	copyDone := make(chan error, 1)
	go func() {
		copyDone <- CopyRawConnIfExist(ctx, sourceReader, destinationWriter, writer, timer, nil)
	}()

	payload := bytes.Repeat([]byte{0x7b}, rawCopyAccountingChunk)
	if _, err := io.Copy(sourceWriter, bytes.NewReader(payload)); err != nil {
		t.Fatalf("write source payload: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for (writerCounter.Value() != int64(len(payload)) || legacyCounter.Value() != int64(len(payload))) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := writerCounter.Value(); got != int64(len(payload)) {
		t.Fatalf("writer-direction counter before source EOF = %d, want %d", got, len(payload))
	}
	if got := legacyCounter.Value(); got != int64(len(payload)) {
		t.Fatalf("legacy counter before source EOF = %d, want %d", got, len(payload))
	}
	if got := untouchedCounter.Value(); got != 0 {
		t.Fatalf("counter from the opposite direction = %d, want 0", got)
	}

	if err := sourceWriter.Close(); err != nil {
		t.Fatalf("close source writer: %v", err)
	}
	select {
	case err := <-copyDone:
		if err != nil {
			t.Fatalf("CopyRawConnIfExist() error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("CopyRawConnIfExist did not return after source EOF")
	}
	if err := destinationWriter.Close(); err != nil {
		t.Fatalf("close destination writer: %v", err)
	}
	select {
	case err := <-drainDone:
		if err != nil {
			t.Fatalf("drain destination: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("destination drain did not return")
	}
}

func tcpPair(t *testing.T) (*net.TCPConn, *net.TCPConn) {
	t.Helper()
	listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("listen TCP: %v", err)
	}
	defer listener.Close()

	dialDone := make(chan struct {
		conn *net.TCPConn
		err  error
	}, 1)
	go func() {
		conn, dialErr := net.DialTCP("tcp4", nil, listener.Addr().(*net.TCPAddr))
		dialDone <- struct {
			conn *net.TCPConn
			err  error
		}{conn: conn, err: dialErr}
	}()
	accepted, err := listener.AcceptTCP()
	if err != nil {
		t.Fatalf("accept TCP: %v", err)
	}
	dialed := <-dialDone
	if dialed.err != nil {
		accepted.Close()
		t.Fatalf("dial TCP: %v", dialed.err)
	}
	return dialed.conn, accepted
}

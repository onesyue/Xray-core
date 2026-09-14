//go:build linux

package proxy

import (
	"bytes"
	"context"
	stderrors "errors"
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

// spliceTestPacer is the embedder-side bucket as the splice loop sees it. It
// records charges and can refuse one, which must tear the copy down.
type spliceTestPacer struct {
	chunk   int
	charged atomic.Int64
	refuse  error
}

func (p *spliceTestPacer) SpliceChunkBytes() int { return p.chunk }
func (p *spliceTestPacer) ChargeSplice(_ context.Context, n int) error {
	p.charged.Add(int64(n))
	return p.refuse
}

// spliceTestPacerWriter is the transparent wrapper an embedder's limiter takes
// in the chain: it owns the pacer and unwraps to the rest of the writers.
type spliceTestPacerWriter struct {
	buf.Writer
	pacer dispatcher.SplicePacer
}

func (w *spliceTestPacerWriter) SplicePacer() dispatcher.SplicePacer { return w.pacer }
func (w *spliceTestPacerWriter) UnwrapWriter() buf.Writer            { return w.Writer }

// bufferedSink counts what reaches the buffered chain. Under splice it must
// stay at zero; on the fail-closed fallback it must see every byte.
type bufferedSink struct{ bytes atomic.Int64 }

func (s *bufferedSink) WriteMultiBuffer(mb buf.MultiBuffer) error {
	s.bytes.Add(int64(mb.Len()))
	buf.ReleaseMulti(mb)
	return nil
}

type rawCopyResult struct {
	err      error
	drained  int64
	buffered int64
	counted  int64
}

// runRawCopyThroughSession drives one download through CopyRawConnIfExist over
// real loopback TCP pairs with the given session flag and writer chain.
func runRawCopyThroughSession(t *testing.T, requirePacing bool, pacer dispatcher.SplicePacer, payload []byte) rawCopyResult {
	t.Helper()
	targetRemote, targetLocal := tcpPair(t)
	clientLocal, clientRemote := tcpPair(t)
	defer targetRemote.Close()
	defer targetLocal.Close()
	defer clientLocal.Close()
	defer clientRemote.Close()

	go func() {
		_, _ = targetRemote.Write(payload)
		_ = targetRemote.CloseWrite()
	}()
	drainDone := make(chan int64, 1)
	go func() {
		n, _ := io.Copy(io.Discard, clientRemote)
		drainDone <- n
	}()

	sink := &bufferedSink{}
	counter := &spliceTestCounter{}
	var writer buf.Writer = sink
	if pacer != nil {
		writer = &spliceTestPacerWriter{Writer: writer, pacer: pacer}
	}
	writer = &dispatcher.AccountingWriter{Counter: counter, Writer: writer}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx = session.ContextWithInbound(ctx, &session.Inbound{
		Conn:                 clientLocal,
		CanSpliceCopy:        1,
		RequiresSplicePacing: requirePacing,
	})
	ctx = session.ContextWithOutbounds(ctx, []*session.Outbound{{CanSpliceCopy: 1}})
	timer := signal.CancelAfterInactivity(ctx, cancel, 30*time.Second)
	defer timer.SetTimeout(0)

	err := CopyRawConnIfExist(ctx, targetLocal, clientLocal, writer, timer, nil)
	_ = clientLocal.Close()
	var drained int64
	select {
	case drained = <-drainDone:
	case <-time.After(5 * time.Second):
		t.Fatal("client drain did not return")
	}
	return rawCopyResult{err: err, drained: drained, buffered: sink.bytes.Load(), counted: counter.Value()}
}

func TestCopyRawConnIfExistPacesSpliceThroughTheWrapperChain(t *testing.T) {
	payload := bytes.Repeat([]byte{0x6c}, 2*rawCopyAccountingChunk+4096)
	pacer := &spliceTestPacer{chunk: 256 << 10}
	got := runRawCopyThroughSession(t, true, pacer, payload)
	if got.err != nil {
		t.Fatalf("CopyRawConnIfExist() error = %v", got.err)
	}
	if got.buffered != 0 {
		t.Fatalf("buffered chain saw %d bytes; a capped session with a reachable pacer must keep the splice path", got.buffered)
	}
	if got.drained != int64(len(payload)) || got.counted != int64(len(payload)) {
		t.Fatalf("drained=%d counted=%d, want %d/%d", got.drained, got.counted, len(payload), len(payload))
	}
	if charged := pacer.charged.Load(); charged != int64(len(payload)) {
		t.Fatalf("pacer charged %d bytes, want %d: every spliced byte must be paid for", charged, len(payload))
	}
}

func TestCopyRawConnIfExistFallsBackToBufferedWhenNoPacerIsReachable(t *testing.T) {
	payload := bytes.Repeat([]byte{0x6d}, 512<<10)
	got := runRawCopyThroughSession(t, true, nil, payload)
	if got.err != nil {
		t.Fatalf("CopyRawConnIfExist() error = %v", got.err)
	}
	if got.buffered != int64(len(payload)) {
		t.Fatalf("buffered chain saw %d bytes, want %d: a capped session without a pacer must not be spliced unmetered", got.buffered, len(payload))
	}
	if got.counted != int64(len(payload)) {
		t.Fatalf("accounting counted %d bytes on the fallback, want %d", got.counted, len(payload))
	}
}

func TestCopyRawConnIfExistKeepsSpliceForUncappedSessions(t *testing.T) {
	payload := bytes.Repeat([]byte{0x6e}, 512<<10)
	got := runRawCopyThroughSession(t, false, nil, payload)
	if got.err != nil {
		t.Fatalf("CopyRawConnIfExist() error = %v", got.err)
	}
	if got.buffered != 0 {
		t.Fatalf("an uncapped session took the buffered path (%d bytes); the pacing check must not cost unlimited users their zero-copy", got.buffered)
	}
	if got.drained != int64(len(payload)) || got.counted != int64(len(payload)) {
		t.Fatalf("drained=%d counted=%d, want %d", got.drained, got.counted, len(payload))
	}
}

func TestCopyRawConnIfExistTearsDownWhenThePacerRefuses(t *testing.T) {
	payload := bytes.Repeat([]byte{0x6f}, 4<<20)
	refused := stderrors.New("ceiling refused")
	pacer := &spliceTestPacer{chunk: 64 << 10, refuse: refused}
	got := runRawCopyThroughSession(t, true, pacer, payload)
	if !stderrors.Is(got.err, refused) {
		t.Fatalf("CopyRawConnIfExist() error = %v, want the pacer's refusal to surface", got.err)
	}
	if got.buffered != 0 {
		t.Fatalf("buffered chain saw %d bytes after a refused charge; the copy must stop, not switch paths", got.buffered)
	}
	if got.drained >= int64(len(payload)) {
		t.Fatalf("client received the whole %d-byte payload despite the refusal; the connection was not torn down", len(payload))
	}
}

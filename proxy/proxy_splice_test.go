package proxy

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func TestCopyRawConnCountedReportsEveryChunk(t *testing.T) {
	payload := bytes.Repeat([]byte{0x5a}, 2*rawCopyAccountingChunk+137)
	var dst bytes.Buffer
	var chunks []int64

	if err := copyRawConnCounted(context.Background(), &dst, bytes.NewReader(payload), nil, func(n int64) {
		chunks = append(chunks, n)
	}); err != nil {
		t.Fatalf("copyRawConnCounted() error = %v", err)
	}

	wantChunks := []int64{rawCopyAccountingChunk, rawCopyAccountingChunk, 137}
	if len(chunks) != len(wantChunks) {
		t.Fatalf("reported chunks = %v, want %v", chunks, wantChunks)
	}
	for i, want := range wantChunks {
		if chunks[i] != want {
			t.Fatalf("reported chunks[%d] = %d, want %d", i, chunks[i], want)
		}
	}
	if !bytes.Equal(dst.Bytes(), payload) {
		t.Fatal("chunked raw copy changed payload bytes")
	}
}

func TestCopyRawConnCountedAcceptsEmptySource(t *testing.T) {
	var dst bytes.Buffer
	if err := copyRawConnCounted(context.Background(), &dst, bytes.NewReader(nil), nil, nil); err != nil {
		t.Fatalf("copyRawConnCounted() empty source error = %v", err)
	}
}

// recordingPacer records every charge together with how many bytes the
// destination already held when the charge arrived, which is what proves the
// "pay after copy" contract rather than a budget reserved up front.
type recordingPacer struct {
	chunk   int
	dst     *bytes.Buffer
	charges []int
	dstAt   []int
	failAt  int // charge index that returns failErr; -1 never fails
	failErr error
}

func (p *recordingPacer) SpliceChunkBytes() int { return p.chunk }

func (p *recordingPacer) ChargeSplice(_ context.Context, n int) error {
	p.charges = append(p.charges, n)
	p.dstAt = append(p.dstAt, p.dst.Len())
	if p.failAt >= 0 && len(p.charges)-1 == p.failAt {
		return p.failErr
	}
	return nil
}

func TestCopyRawConnCountedChargesThePacerPerChunkAfterTheCopy(t *testing.T) {
	const chunk = 64 << 10
	payload := bytes.Repeat([]byte{0x33}, 3*chunk+5)
	var dst bytes.Buffer
	pacer := &recordingPacer{chunk: chunk, dst: &dst, failAt: -1}
	var reported []int64

	if err := copyRawConnCounted(context.Background(), &dst, bytes.NewReader(payload), pacer, func(n int64) {
		reported = append(reported, n)
	}); err != nil {
		t.Fatalf("copyRawConnCounted() error = %v", err)
	}

	// The pacer's chunk bounds the loop, not the 1 MiB accounting default:
	// that is what keeps one charge inside the pacer's wait budget.
	wantCharges := []int{chunk, chunk, chunk, 5}
	if len(pacer.charges) != len(wantCharges) {
		t.Fatalf("charges = %v, want %v", pacer.charges, wantCharges)
	}
	total := 0
	for i, want := range wantCharges {
		if pacer.charges[i] != want {
			t.Fatalf("charges[%d] = %d, want %d", i, pacer.charges[i], want)
		}
		total += want
		if pacer.dstAt[i] != total {
			t.Fatalf("charge %d arrived with %d bytes copied, want %d: the pacer must be charged after the chunk moved, not before", i, pacer.dstAt[i], total)
		}
		if reported[i] != int64(want) {
			t.Fatalf("accounting chunk[%d] = %d, want %d: accounting and pacing must see the same chunks", i, reported[i], want)
		}
	}
	if total != len(payload) {
		t.Fatalf("charged %d bytes in total, want %d", total, len(payload))
	}
	if !bytes.Equal(dst.Bytes(), payload) {
		t.Fatal("paced raw copy changed payload bytes")
	}
}

func TestCopyRawConnCountedClampsThePacerChunkToTheAccountingChunk(t *testing.T) {
	for name, chunk := range map[string]int{
		"larger-than-accounting": 4 * rawCopyAccountingChunk,
		"zero-means-default":     0,
		"negative-means-default": -1,
	} {
		t.Run(name, func(t *testing.T) {
			payload := bytes.Repeat([]byte{0x44}, rawCopyAccountingChunk+1)
			var dst bytes.Buffer
			pacer := &recordingPacer{chunk: chunk, dst: &dst, failAt: -1}
			if err := copyRawConnCounted(context.Background(), &dst, bytes.NewReader(payload), pacer, nil); err != nil {
				t.Fatalf("copyRawConnCounted() error = %v", err)
			}
			want := []int{rawCopyAccountingChunk, 1}
			if len(pacer.charges) != len(want) || pacer.charges[0] != want[0] || pacer.charges[1] != want[1] {
				t.Fatalf("charges = %v, want %v: a pacer may shrink the chunk but never grow it past the accounting bound", pacer.charges, want)
			}
		})
	}
}

// A pacing failure is the same event as a buffered-path limiter failure: the
// copy stops and the error reaches the caller, so the connection is torn down
// instead of continuing unmetered.
func TestCopyRawConnCountedFailsClosedWhenTheChargeIsRefused(t *testing.T) {
	const chunk = 16 << 10
	payload := bytes.Repeat([]byte{0x55}, 4*chunk)
	var dst bytes.Buffer
	refused := errors.New("ceiling refused")
	pacer := &recordingPacer{chunk: chunk, dst: &dst, failAt: 1, failErr: refused}
	var reported int64

	err := copyRawConnCounted(context.Background(), &dst, bytes.NewReader(payload), pacer, func(n int64) {
		reported += n
	})
	if !errors.Is(err, refused) {
		t.Fatalf("copyRawConnCounted() error = %v, want the pacer's error", err)
	}
	if len(pacer.charges) != 2 {
		t.Fatalf("pacer saw %d charges after refusing the second, want exactly 2 (the loop must stop)", len(pacer.charges))
	}
	if dst.Len() != 2*chunk {
		t.Fatalf("destination holds %d bytes after the refused charge, want %d: no further chunk may be copied", dst.Len(), 2*chunk)
	}
	if reported != int64(dst.Len()) {
		t.Fatalf("accounting reported %d bytes, destination holds %d: every copied byte is still billed even when the copy is torn down", reported, dst.Len())
	}
}

package proxy

import (
	"bytes"
	"testing"
)

func TestCopyRawConnCountedReportsEveryChunk(t *testing.T) {
	payload := bytes.Repeat([]byte{0x5a}, 2*rawCopyAccountingChunk+137)
	var dst bytes.Buffer
	var chunks []int64

	if err := copyRawConnCounted(&dst, bytes.NewReader(payload), func(n int64) {
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
	if err := copyRawConnCounted(&dst, bytes.NewReader(nil), nil); err != nil {
		t.Fatalf("copyRawConnCounted() empty source error = %v", err)
	}
}

package proxy

import (
	"bytes"
	"context"
	"testing"

	"github.com/xtls/xray-core/common/buf"
)

func TestXtlsPaddingPreservesFullBufferWithoutPanicking(t *testing.T) {
	payload := bytes.Repeat([]byte{0x5a}, buf.Size)
	input := buf.New()
	if n, err := input.Write(payload); err != nil || n != len(payload) {
		t.Fatalf("fill input: n=%d err=%v", n, err)
	}

	// A zero upper bound also used to panic in crypto/rand.Int. Both values are
	// legal uint32s at the protobuf boundary, so hardening belongs here.
	output := XtlsPadding(input, CommandPaddingContinue, nil, false, context.Background(), []uint32{0, 0, 0, 0})
	defer output.Release()
	if got, want := output.Len(), int32(buf.Size+5); got != want {
		t.Fatalf("padded length = %d, want %d", got, want)
	}
	data := output.Bytes()
	if got := int(data[1])<<8 | int(data[2]); got != len(payload) {
		t.Fatalf("encoded content length = %d, want %d", got, len(payload))
	}
	if data[3] != 0 || data[4] != 0 {
		t.Fatalf("oversized block padding = %x%x, want 0", data[3], data[4])
	}
	if !bytes.Equal(data[5:], payload) {
		t.Fatal("full input payload was truncated or changed")
	}
}

func TestXtlsPaddingPreservesFullBufferWithUUID(t *testing.T) {
	payload := bytes.Repeat([]byte{0x7b}, buf.Size)
	input := buf.New()
	_, _ = input.Write(payload)
	uuid := bytes.Repeat([]byte{0x11}, 16)

	output := XtlsPadding(input, CommandPaddingEnd, &uuid, true, context.Background(), []uint32{1, 0, 0, 0})
	defer output.Release()
	if uuid != nil {
		t.Fatal("UUID was not consumed")
	}
	if got, want := output.Len(), int32(buf.Size+21); got != want {
		t.Fatalf("padded length = %d, want %d", got, want)
	}
	data := output.Bytes()
	if !bytes.Equal(data[:16], bytes.Repeat([]byte{0x11}, 16)) {
		t.Fatal("UUID prefix changed")
	}
	if !bytes.Equal(data[21:], payload) {
		t.Fatal("full input payload with UUID was truncated or changed")
	}
}

func TestXtlsPaddingClampsNegativeLongPadding(t *testing.T) {
	input := buf.New()
	_, _ = input.Write(bytes.Repeat([]byte{0x33}, 100))
	output := XtlsPadding(input, CommandPaddingContinue, nil, true, context.Background(), []uint32{200, 0, 1, 0})
	defer output.Release()
	if got := output.Len(); got != 105 {
		t.Fatalf("negative padding was not clamped: output length = %d, want 105", got)
	}
}

func TestXtlsPaddingUsesSafeDefaultsForShortSeed(t *testing.T) {
	input := buf.New()
	_, _ = input.Write([]byte("payload"))
	output := XtlsPadding(input, CommandPaddingContinue, nil, false, context.Background(), nil)
	defer output.Release()
	if output.Len() < int32(len("payload")+5) {
		t.Fatalf("short seed truncated output: length = %d", output.Len())
	}
}

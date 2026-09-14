package dispatcher_test

import (
	"context"
	"testing"

	. "github.com/xtls/xray-core/app/dispatcher"
	"github.com/xtls/xray-core/common/buf"
)

type testPacer struct{ chunk int }

func (p *testPacer) SpliceChunkBytes() int                   { return p.chunk }
func (p *testPacer) ChargeSplice(context.Context, int) error { return nil }

// pacerWriter is the shape an embedder's rate limiter takes: a transparent
// wrapper that owns a pacer and unwraps to the rest of the chain.
type pacerWriter struct {
	buf.Writer
	pacer SplicePacer
}

func (w *pacerWriter) SplicePacer() SplicePacer { return w.pacer }
func (w *pacerWriter) UnwrapWriter() buf.Writer { return w.Writer }

type selfUnwrappingWriter struct{ buf.Writer }

func (w *selfUnwrappingWriter) UnwrapWriter() buf.Writer { return w }

func TestFindSplicePacerWalksTransparentWrappers(t *testing.T) {
	pacer := &testPacer{chunk: 64 << 10}
	var legacy TestCounter
	// Production order: accounting outermost, the limiter inside it, Xray's
	// own stats wrapper below that.
	writer := &AccountingWriter{
		Writer: &pacerWriter{
			Writer: &SizeStatWriter{Counter: &legacy, Writer: buf.Discard},
			pacer:  pacer,
		},
	}
	if got := FindSplicePacer(writer); got != pacer {
		t.Fatalf("FindSplicePacer() = %v, want the wrapped pacer", got)
	}
	// Discovery must not depend on the pacer being outermost, and the counter
	// walk must keep working through the pacer wrapper.
	if got := FindSizeStatCounter(writer); got != &legacy {
		t.Fatalf("FindSizeStatCounter() through the pacer wrapper = %v, want legacy counter", got)
	}
}

func TestFindSplicePacerSkipsSourcesWithoutAPacer(t *testing.T) {
	inner := &testPacer{chunk: 1}
	writer := &pacerWriter{
		pacer:  nil, // a wrapper that exists but is not limiting this direction
		Writer: &pacerWriter{pacer: inner, Writer: buf.Discard},
	}
	if got := FindSplicePacer(writer); got != inner {
		t.Fatalf("FindSplicePacer() = %v, want the inner pacer past a nil source", got)
	}
}

func TestFindSplicePacerReturnsNilWhenTheChainCarriesNone(t *testing.T) {
	var legacy TestCounter
	for name, writer := range map[string]buf.Writer{
		"plain":            buf.Discard,
		"counters-only":    &AccountingWriter{Writer: &SizeStatWriter{Counter: &legacy, Writer: buf.Discard}},
		"cyclic-wrapper":   &selfUnwrappingWriter{Writer: buf.Discard},
		"nil-pacer-source": &pacerWriter{pacer: nil, Writer: buf.Discard},
	} {
		if got := FindSplicePacer(writer); got != nil {
			t.Fatalf("%s: FindSplicePacer() = %v, want nil", name, got)
		}
	}
}

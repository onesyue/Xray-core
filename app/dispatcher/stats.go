package dispatcher

import (
	"context"
	"time"

	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/features/stats"
)

// WriterUnwrapper is implemented by transparent dispatcher wrappers. Raw
// splice code must not depend on a counter being the outermost concrete writer.
type WriterUnwrapper interface {
	UnwrapWriter() buf.Writer
}

type SizeStatWriter struct {
	Counter stats.Counter
	Writer  buf.Writer
}

func (w *SizeStatWriter) WriteMultiBuffer(mb buf.MultiBuffer) error {
	w.Counter.Add(int64(mb.Len()))
	return w.Writer.WriteMultiBuffer(mb)
}

func (w *SizeStatWriter) Close() error {
	return common.Close(w.Writer)
}

func (w *SizeStatWriter) Interrupt() {
	common.Interrupt(w.Writer)
}

func (w *SizeStatWriter) UnwrapWriter() buf.Writer { return w.Writer }

// FindSizeStatCounter returns Xray's built-in per-user counter through any
// transparent writer wrappers. The bounded walk also protects against an
// accidentally cyclic wrapper implementation.
func FindSizeStatCounter(writer buf.Writer) stats.Counter {
	for range 16 {
		if w, ok := writer.(*SizeStatWriter); ok {
			return w.Counter
		}
		w, ok := writer.(WriterUnwrapper)
		if !ok {
			return nil
		}
		next := w.UnwrapWriter()
		if next == nil || next == writer {
			return nil
		}
		writer = next
	}
	return nil
}

// FindAccountingCounter returns the embedder-owned counter associated with the
// actual writer direction. Raw copy can serve more than one VLESS direction,
// so selecting a fixed session uplink/downlink field is unsafe.
func FindAccountingCounter(writer buf.Writer) stats.Counter {
	for range 16 {
		if w, ok := writer.(*AccountingWriter); ok {
			return w.Counter
		}
		w, ok := writer.(WriterUnwrapper)
		if !ok {
			return nil
		}
		next := w.UnwrapWriter()
		if next == nil || next == writer {
			return nil
		}
		writer = next
	}
	return nil
}

// AccountingWriter feeds an application-injected counter on buffered paths.
// Raw splice feeds the same counter explicitly from proxy.CopyRawConnIfExist.
type AccountingWriter struct {
	Counter stats.Counter
	Writer  buf.Writer
}

func (w *AccountingWriter) WriteMultiBuffer(mb buf.MultiBuffer) error {
	if w.Counter != nil {
		w.Counter.Add(int64(mb.Len()))
	}
	return w.Writer.WriteMultiBuffer(mb)
}

func (w *AccountingWriter) Close() error { return common.Close(w.Writer) }
func (w *AccountingWriter) Interrupt()   { common.Interrupt(w.Writer) }
func (w *AccountingWriter) UnwrapWriter() buf.Writer {
	return w.Writer
}

// AccountingReader feeds an application-injected counter on buffered uplink
// paths while preserving TimeoutReader and mux interruption semantics.
type AccountingReader struct {
	Counter stats.Counter
	Reader  buf.TimeoutReader
}

func (r *AccountingReader) ReadMultiBuffer() (buf.MultiBuffer, error) {
	mb, err := r.Reader.ReadMultiBuffer()
	if r.Counter != nil {
		r.Counter.Add(int64(mb.Len()))
	}
	return mb, err
}

func (r *AccountingReader) ReadMultiBufferTimeout(d time.Duration) (buf.MultiBuffer, error) {
	mb, err := r.Reader.ReadMultiBufferTimeout(d)
	if r.Counter != nil {
		r.Counter.Add(int64(mb.Len()))
	}
	return mb, err
}

func (r *AccountingReader) ReturnAnError(err error) {
	if v, ok := r.Reader.(interface{ ReturnAnError(error) }); ok {
		v.ReturnAnError(err)
	}
}

func (r *AccountingReader) Recover() error {
	if v, ok := r.Reader.(interface{ Recover() error }); ok {
		return v.Recover()
	}
	return nil
}

func (r *AccountingReader) Close() error { return common.Close(r.Reader) }
func (r *AccountingReader) Interrupt()   { common.Interrupt(r.Reader) }

// SplicePacer is implemented by an embedder-owned writer that enforces a
// per-credential byte rate on the buffered path. Raw splice bypasses every
// buf.Writer in the chain, so without this seam a rate-limited credential
// could only be enforced by refusing splice outright (CanSpliceCopy = 3) and
// paying userspace AEAD for every byte.
//
// The contract is deliberately "pay after copy": the pacer is charged for the
// bytes a chunk actually moved, never for a budget that may go unused. Charging
// up front would make a trickling connection slower than its configured rate.
type SplicePacer interface {
	// SpliceChunkBytes reports the largest chunk the pacer wants copied between
	// two charges. It bounds how far a single chunk may overshoot the rate, and
	// keeps one charge inside whatever wait budget the pacer enforces.
	SpliceChunkBytes() int
	// ChargeSplice blocks until n copied bytes are paid for. A returned error
	// tears the connection down, exactly as a buffered-path limiter error does.
	ChargeSplice(ctx context.Context, n int) error
}

// SplicePacerSource is implemented by transparent wrappers that own a pacer.
type SplicePacerSource interface {
	SplicePacer() SplicePacer
}

// FindSplicePacer returns the embedder-owned pacer for this writer direction,
// walking the same transparent wrapper chain as FindAccountingCounter. A nil
// result means "this direction is not rate limited"; callers that require
// enforcement must decide that from the session, never from a nil here.
func FindSplicePacer(writer buf.Writer) SplicePacer {
	for range 16 {
		if w, ok := writer.(SplicePacerSource); ok {
			if p := w.SplicePacer(); p != nil {
				return p
			}
		}
		w, ok := writer.(WriterUnwrapper)
		if !ok {
			return nil
		}
		next := w.UnwrapWriter()
		if next == nil || next == writer {
			return nil
		}
		writer = next
	}
	return nil
}

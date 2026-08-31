package dispatcher_test

import (
	"io"
	"testing"
	"time"

	. "github.com/xtls/xray-core/app/dispatcher"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
)

type TestCounter int64

func (c *TestCounter) Value() int64 {
	return int64(*c)
}

type staticTimeoutReader struct {
	payload []byte
}

func (r *staticTimeoutReader) ReadMultiBuffer() (buf.MultiBuffer, error) {
	return buf.MergeBytes(nil, r.payload), io.EOF
}

func (r *staticTimeoutReader) ReadMultiBufferTimeout(time.Duration) (buf.MultiBuffer, error) {
	return r.ReadMultiBuffer()
}

func (c *TestCounter) Add(v int64) int64 {
	x := int64(*c) + v
	*c = TestCounter(x)
	return x
}

func TestAccountingWrappersAndCounterDiscovery(t *testing.T) {
	var native, legacy TestCounter
	writer := &AccountingWriter{
		Counter: &native,
		Writer: &SizeStatWriter{
			Counter: &legacy,
			Writer:  buf.Discard,
		},
	}
	common.Must(writer.WriteMultiBuffer(buf.MergeBytes(nil, []byte("abcd"))))
	if native.Value() != 4 || legacy.Value() != 4 {
		t.Fatalf("buffered counters = native:%d legacy:%d, want 4/4", native.Value(), legacy.Value())
	}
	if got := FindAccountingCounter(writer); got != &native {
		t.Fatalf("FindAccountingCounter() = %T, want native counter", got)
	}
	if got := FindSizeStatCounter(writer); got != &legacy {
		t.Fatalf("FindSizeStatCounter() = %T, want legacy counter", got)
	}

	var uplink TestCounter
	reader := &AccountingReader{
		Counter: &uplink,
		Reader:  &staticTimeoutReader{payload: []byte("uplink")},
	}
	mb, err := reader.ReadMultiBufferTimeout(time.Second)
	buf.ReleaseMulti(mb)
	if err != io.EOF {
		t.Fatalf("ReadMultiBufferTimeout() error = %v, want EOF", err)
	}
	if uplink.Value() != 6 {
		t.Fatalf("uplink counter = %d, want 6", uplink.Value())
	}
}

func (c *TestCounter) Set(v int64) int64 {
	*c = TestCounter(v)
	return v
}

func TestStatsWriter(t *testing.T) {
	var c TestCounter
	writer := &SizeStatWriter{
		Counter: &c,
		Writer:  buf.Discard,
	}

	mb := buf.MergeBytes(nil, []byte("abcd"))
	common.Must(writer.WriteMultiBuffer(mb))

	mb = buf.MergeBytes(nil, []byte("efg"))
	common.Must(writer.WriteMultiBuffer(mb))

	if c.Value() != 7 {
		t.Fatal("unexpected counter value. want 7, but got ", c.Value())
	}
}

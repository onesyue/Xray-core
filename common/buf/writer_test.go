package buf_test

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"io"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/xtls/xray-core/common"
	. "github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/transport/pipe"
)

func TestWriter(t *testing.T) {
	lb := New()
	common.Must2(lb.ReadFrom(rand.Reader))

	expectedBytes := append([]byte(nil), lb.Bytes()...)

	writeBuffer := bytes.NewBuffer(make([]byte, 0, 1024*1024))

	writer := NewBufferedWriter(NewWriter(writeBuffer))
	writer.SetBuffered(false)
	common.Must(writer.WriteMultiBuffer(MultiBuffer{lb}))
	common.Must(writer.Flush())

	if r := cmp.Diff(expectedBytes, writeBuffer.Bytes()); r != "" {
		t.Error(r)
	}
}

// A payload crossing an already partly filled buffer must preserve all bytes.
// Upstream #6723 fixes ErrBufferFull being reported as a terminal write error.
func TestBufferedWriterCrossesPartialBuffer(t *testing.T) {
	var destination bytes.Buffer
	writer := NewBufferedWriter(NewWriter(&destination))
	prefix := bytes.Repeat([]byte("p"), Size-7)
	payload := bytes.Repeat([]byte("payload"), Size)
	for _, input := range [][]byte{prefix, payload} {
		n, err := writer.Write(input)
		if err != nil || n != len(input) {
			t.Fatalf("Write(%d bytes) = %d, %v", len(input), n, err)
		}
	}
	if err := writer.Flush(); err != nil {
		t.Fatal(err)
	}
	if want := append(prefix, payload...); !bytes.Equal(destination.Bytes(), want) {
		t.Fatalf("buffered write changed payload: got %d bytes, want %d", destination.Len(), len(want))
	}
}

func TestBytesWriterReadFrom(t *testing.T) {
	const size = 50000
	pReader, pWriter := pipe.New(pipe.WithSizeLimit(size))
	reader := bufio.NewReader(io.LimitReader(rand.Reader, size))
	writer := NewBufferedWriter(pWriter)
	writer.SetBuffered(false)
	nBytes, err := reader.WriteTo(writer)
	if nBytes != size {
		t.Fatal("unexpected size of bytes written: ", nBytes)
	}
	if err != nil {
		t.Fatal("expect success, but actually error: ", err.Error())
	}

	mb, err := pReader.ReadMultiBuffer()
	common.Must(err)
	if mb.Len() != size {
		t.Fatal("unexpected size read: ", mb.Len())
	}
}

func TestDiscardBytes(t *testing.T) {
	b := New()
	common.Must2(b.ReadFullFrom(rand.Reader, Size))

	nBytes, err := io.Copy(DiscardBytes, b)
	common.Must(err)
	if nBytes != Size {
		t.Error("copy size: ", nBytes)
	}
}

func TestDiscardBytesMultiBuffer(t *testing.T) {
	const size = 10240*1024 + 1
	buffer := bytes.NewBuffer(make([]byte, 0, size))
	common.Must2(buffer.ReadFrom(io.LimitReader(rand.Reader, size)))

	r := NewReader(buffer)
	nBytes, err := io.Copy(DiscardBytes, &BufferedReader{Reader: r})
	common.Must(err)
	if nBytes != size {
		t.Error("copy size: ", nBytes)
	}
}

func TestWriterInterface(t *testing.T) {
	{
		var writer interface{} = (*BufferToBytesWriter)(nil)
		switch writer.(type) {
		case Writer, io.Writer, io.ReaderFrom:
		default:
			t.Error("BufferToBytesWriter is not Writer, io.Writer or io.ReaderFrom")
		}
	}

	{
		var writer interface{} = (*BufferedWriter)(nil)
		switch writer.(type) {
		case Writer, io.Writer, io.ReaderFrom, io.ByteWriter:
		default:
			t.Error("BufferedWriter is not Writer, io.Writer, io.ReaderFrom or io.ByteWriter")
		}
	}
}

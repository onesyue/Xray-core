package vless

import (
	"bytes"
	"net"
	"testing"
	"time"
)

// valueRawConn and pointerRawConn are the two rawInput shapes AccessTLSBuffers
// has to drain: Go 1.27.1 / REALITY v0.0.0-yue.3 store the buffer by value,
// Go 1.27.3+ crypto/tls stores a pointer (upstream #7176).
type valueRawConn struct {
	stubConn
	input    bytes.Reader
	rawInput bytes.Buffer
}

type pointerRawConn struct {
	stubConn
	input    bytes.Reader
	rawInput *bytes.Buffer
}

type stubConn struct{}

func (stubConn) Read([]byte) (int, error)         { return 0, nil }
func (stubConn) Write([]byte) (int, error)        { return 0, nil }
func (stubConn) Close() error                     { return nil }
func (stubConn) LocalAddr() net.Addr              { return nil }
func (stubConn) RemoteAddr() net.Addr             { return nil }
func (stubConn) SetDeadline(time.Time) error      { return nil }
func (stubConn) SetReadDeadline(time.Time) error  { return nil }
func (stubConn) SetWriteDeadline(time.Time) error { return nil }

func TestAccessTLSBuffersValueAndPointerRawInput(t *testing.T) {
	value := &valueRawConn{}
	value.rawInput.WriteString("value")
	input, raw, err := AccessTLSBuffers(value)
	if err != nil {
		t.Fatalf("value layout: %v", err)
	}
	if input != &value.input {
		t.Fatal("value layout: input does not alias the connection field")
	}
	if raw == nil || *raw != &value.rawInput || (*raw).String() != "value" {
		t.Fatalf("value layout: rawInput = %v", raw)
	}

	pointer := &pointerRawConn{rawInput: bytes.NewBufferString("pointer")}
	input, raw, err = AccessTLSBuffers(pointer)
	if err != nil {
		t.Fatalf("pointer layout: %v", err)
	}
	if input != &pointer.input {
		t.Fatal("pointer layout: input does not alias the connection field")
	}
	if raw == nil || *raw != pointer.rawInput || (*raw).String() != "pointer" {
		t.Fatalf("pointer layout: rawInput = %v", raw)
	}

	if _, _, err := AccessTLSBuffers(&stubConn{}); err == nil {
		t.Fatal("missing fields returned a buffer instead of an error")
	}
}

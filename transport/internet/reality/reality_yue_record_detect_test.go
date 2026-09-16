package reality_test

import (
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	goreality "github.com/xtls/reality"
)

// These tests pin the three DetectPostHandshakeRecordsLens bugs fixed upstream
// in xtls/reality e1986a4d ("fix: DetectPostHandshakeRecordsLens background
// probe bugs (panic, leak, race)", #36), which this fork consumes while still
// holding the module below 20260908062103 (see reality_yue_keyshare_test.go and
// YUE_FORK.md).
//
// transport/internet/tcp/hub.go starts the probe with a bare `go`, so a panic
// inside it has no recover and takes the whole process down; a leaked probe
// connection is one file descriptor per (dest, SNI, ALPN) that never closes.
// Both are driven by bytes the camouflage destination returns, not by clients.

// TestPostHandshakeRecordDetectSurvivesTruncatedRecord feeds the detector an
// application-data record header whose declared length exceeds what the
// destination actually sent. Before e1986a4d `data = data[length:]` sliced out
// of range and panicked.
func TestPostHandshakeRecordDetectSurvivesTruncatedRecord(t *testing.T) {
	cases := []struct {
		name string
		wire []byte
		want []int
	}{
		{
			name: "truncated",
			wire: []byte{23, 3, 3, 0x40, 0x00, 1, 2, 3},
			want: nil,
		},
		{
			name: "whole records then truncated tail",
			wire: []byte{23, 3, 3, 0, 2, 'a', 'b', 23, 3, 3, 0, 1, 'c', 23, 3, 3, 0xff, 0xff},
			want: []int{7, 6},
		},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, dest := net.Pipe()
			defer client.Close()
			go func() {
				dest.Write(tc.wire)
				dest.Close()
			}()
			key := fmt.Sprintf("yue-truncated-record-%d", i)
			conn := &goreality.PostHandshakeRecordDetectConn{Conn: client, Key: key, CcsSent: true}

			var (
				n   int
				err error
			)
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("detector panicked on destination bytes %x: %v", tc.wire, r)
					}
				}()
				n, err = conn.Read(make([]byte, 64))
			}()
			if n != 0 || !errors.Is(err, io.EOF) {
				t.Fatalf("Read = (%d, %v), want (0, EOF)", n, err)
			}
			stored, ok := goreality.GlobalPostHandshakeRecordsLens.Load(key)
			if !ok {
				t.Fatal("detector stored no result")
			}
			got, ok := stored.([]int)
			if !ok {
				t.Fatalf("stored result has type %T, want []int", stored)
			}
			if fmt.Sprint(got) != fmt.Sprint(tc.want) {
				t.Fatalf("record lengths = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestDetectPostHandshakeRecordsLensClosesProbeConnections points the probe at a
// destination that answers with something that is not TLS, so every probe
// handshake fails. Before e1986a4d both probe goroutines returned on that path
// without closing their connection; the destination then never sees EOF.
func TestDetectPostHandshakeRecordsLensClosesProbeConnections(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	// One SNI x three ALPN variants x two probes (records + CCS).
	const expected = 6
	type outcome struct {
		closed bool
		err    error
	}
	outcomes := make(chan outcome, expected)
	go func() {
		for range expected {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				c.SetDeadline(time.Now().Add(5 * time.Second))
				c.Read(make([]byte, 4096)) // the ClientHello
				c.Write([]byte("HTTP/1.1 400 Bad Request\r\nContent-Length: 0\r\n\r\n"))
				_, err := io.Copy(io.Discard, c)
				if err == nil {
					outcomes <- outcome{closed: true}
					return
				}
				outcomes <- outcome{err: err}
			}()
		}
	}()

	goreality.DetectPostHandshakeRecordsLens(&goreality.Config{
		Type:        "tcp",
		Dest:        ln.Addr().String(),
		ServerNames: map[string]bool{"yue-probe-leak.example": true},
	})

	var leaked []error
	for range expected {
		select {
		case o := <-outcomes:
			if !o.closed {
				leaked = append(leaked, o.err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("the probe never connected to the destination")
		}
	}
	if len(leaked) != 0 {
		t.Fatalf("%d of %d failed probe connections were never closed by the prober: %v", len(leaked), expected, leaked)
	}
}

// TestCCSDetectConnProbeReaderDoesNotWriteTheReturnValue only fails under -race.
// Before e1986a4d the background alert reader assigned `_, err = c.Conn.Read`,
// i.e. Write's own named return value, on every read. Here the destination
// never alerts but answers the real ChangeCipherSpec with a non-alert record,
// so the reader assigns err at the same moment Write assigns its result; the
// only ordering between the two is the pipe hand-off both are downstream of.
func TestCCSDetectConnProbeReaderDoesNotWriteTheReturnValue(t *testing.T) {
	if testing.Short() {
		t.Skip("waits out the three CCS probe rounds (~3s)")
	}
	// The probe rounds are bare 6-byte CCS messages; the caller's record carries
	// a trailing marker byte so the destination can tell them apart.
	record := []byte{20, 3, 3, 0, 1, 1, 0xAA}
	client, dest := net.Pipe()
	defer client.Close()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer dest.Close()
		buf := make([]byte, 512)
		for {
			n, err := dest.Read(buf)
			if err != nil {
				return
			}
			if n > 0 && buf[n-1] == 0xAA {
				dest.Write([]byte{0x16}) // not an alert
				return
			}
		}
	}()
	conn := &goreality.CCSDetectConn{Conn: client, Key: "yue-ccs-race"}
	if _, err := conn.Write(record); err != nil {
		t.Fatalf("Write: %v", err)
	}
	wg.Wait()
	// Let the reader see the destination close before the test returns.
	time.Sleep(50 * time.Millisecond)
}

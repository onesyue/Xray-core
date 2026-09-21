package vless_test

import (
	"bytes"
	gotls "crypto/tls"
	"reflect"
	"testing"

	utls "github.com/refraction-networking/utls"
	"github.com/xtls/reality"
	"github.com/xtls/xray-core/proxy/vless/encryption"
)

// [YUE] TestXTLSUnsafeFieldLayout pins the struct layout that
// proxy/vless/inbound and proxy/vless/outbound depend on: both look up the
// unexported "input" and "rawInput" fields by reflect and cast
// (p + field.Offset) to *bytes.Reader / *bytes.Buffer via unsafe.Pointer.
// That is only memory-safe while those fields ARE a bytes.Reader and a
// bytes.Buffer stored by value. onesyue/REALITY v0.0.0-yue.2 made
// reality.Conn.rawInput a *bytes.Buffer (pooled) and yue-node 743c0876 died
// on the canary with "fatal error: fault". A missing field is just as bad:
// FieldByName returns the zero StructField and the cast lands on offset 0.
func TestXTLSUnsafeFieldLayout(t *testing.T) {
	bufferT := reflect.TypeOf(bytes.Buffer{})
	readerT := reflect.TypeOf(bytes.Reader{})
	cases := []struct {
		name string
		typ  reflect.Type
	}{
		{"github.com/xtls/reality.Conn (VLESS inbound, REALITY)", reflect.TypeOf(reality.Conn{})},
		{"crypto/tls.Conn (VLESS inbound/outbound, TLS)", reflect.TypeOf(gotls.Conn{})},
		{"github.com/refraction-networking/utls.Conn (VLESS outbound, uTLS + REALITY client)", reflect.TypeOf(utls.Conn{})},
		{"proxy/vless/encryption.CommonConn (VLESS encryption)", reflect.TypeOf(encryption.CommonConn{})},
	}
	for _, tc := range cases {
		for _, want := range []struct {
			field string
			typ   reflect.Type
		}{{"input", readerT}, {"rawInput", bufferT}} {
			f, ok := tc.typ.FieldByName(want.field)
			if !ok {
				t.Errorf("%s: field %q is gone; the Vision unsafe cast would read offset 0", tc.name, want.field)
				continue
			}
			if f.Type != want.typ {
				t.Errorf("%s: field %q has type %v, want %v by value; Vision's (*%v)(unsafe.Pointer(p+Offset)) cast would read garbage",
					tc.name, want.field, f.Type, want.typ, want.typ)
			}
		}
	}
}

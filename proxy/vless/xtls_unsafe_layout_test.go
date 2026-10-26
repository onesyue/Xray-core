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

// TestXTLSUnsafeFieldLayout pins the struct layout Vision reads.
//
// input is a bytes.Reader on every connection Vision touches.
// rawInput is a bytes.Buffer by value on Go 1.27.1, utls, CommonConn, and
// onesyue/REALITY v0.0.0-yue.3. Go 1.27.3+ crypto/tls stores *bytes.Buffer
// (upstream #7176). AccessTLSBuffers accepts either rawInput shape and
// returns an error for anything else, so a pointer rawInput is no longer a
// use-after-cast fault. reality.Conn is the exception that must stay a
// value: v0.0.0-yue.2 pooled it as *bytes.Buffer and the canary died with
// "fatal error: fault". That module must never be consumed again, and this
// assertion is what turns a silent re-bump into a red test.
func TestXTLSUnsafeFieldLayout(t *testing.T) {
	bufferT := reflect.TypeOf(bytes.Buffer{})
	bufferPtrT := reflect.TypeOf((*bytes.Buffer)(nil))
	readerT := reflect.TypeOf(bytes.Reader{})
	cases := []struct {
		name         string
		typ          reflect.Type
		allowPointer bool
	}{
		{"github.com/xtls/reality.Conn (VLESS inbound, REALITY)", reflect.TypeOf(reality.Conn{}), false},
		{"crypto/tls.Conn (VLESS inbound/outbound, TLS)", reflect.TypeOf(gotls.Conn{}), true},
		{"github.com/refraction-networking/utls.Conn (VLESS outbound, uTLS + REALITY client)", reflect.TypeOf(utls.Conn{}), true},
		{"proxy/vless/encryption.CommonConn (VLESS encryption)", reflect.TypeOf(encryption.CommonConn{}), true},
	}
	for _, tc := range cases {
		input, ok := tc.typ.FieldByName("input")
		if !ok || input.Type != readerT {
			t.Errorf("%s: input = %v, want bytes.Reader by value", tc.name, fieldType(input, ok))
		}
		raw, ok := tc.typ.FieldByName("rawInput")
		if !ok {
			t.Errorf("%s: field rawInput is gone", tc.name)
			continue
		}
		switch {
		case raw.Type == bufferT:
		case tc.allowPointer && raw.Type == bufferPtrT:
		default:
			t.Errorf("%s: rawInput has type %v, want bytes.Buffer by value", tc.name, raw.Type)
		}
	}
}

func fieldType(f reflect.StructField, ok bool) string {
	if !ok {
		return "missing"
	}
	return f.Type.String()
}

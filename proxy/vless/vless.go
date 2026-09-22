// Package vless contains the implementation of VLess protocol and transportation.
//
// VLess contains both inbound and outbound connections. VLess inbound is usually used on servers
// together with 'freedom' to talk to final destination, while VLess outbound is usually used on
// clients with 'socks' for proxying.
package vless

import (
	"bytes"
	"errors"
	"net"
	"reflect"
	"unsafe"
)

const (
	None = "none"
	XRV  = "xtls-rprx-vision"
)

// AccessTLSBuffers returns the unexported TLS input buffers Vision drains when
// it switches a direction to direct copy.
//
// Go 1.27.1 (and onesyue/REALITY v0.0.0-yue.3, utls, CommonConn) store rawInput
// as a bytes.Buffer by value. Go 1.27.3+ crypto/tls stores *bytes.Buffer.
// Upstream #7176 detects which one is present. The address is taken with
// unsafe.Pointer(field.UnsafeAddr()) in this expression: reflect marks
// UnsafeAddr nocheckptr so the compiler accepts that conversion, and the
// uintptr is never stored. A stored uintptr is not a GC root and fails
// checkptr under -race. An incompatible layout returns an error instead of
// panicking or casting an unrelated offset.
func AccessTLSBuffers(conn net.Conn) (*bytes.Reader, **bytes.Buffer, error) {
	rv := reflect.ValueOf(conn)
	if rv.Kind() != reflect.Pointer || rv.IsNil() || rv.Elem().Kind() != reflect.Struct {
		return nil, nil, errors.New("XTLS incompatible TLS input buffer layout")
	}
	elem := rv.Elem()
	inputField := elem.FieldByName("input")
	rawField := elem.FieldByName("rawInput")
	if !inputField.IsValid() || !rawField.IsValid() || !inputField.CanAddr() || !rawField.CanAddr() ||
		inputField.Type() != reflect.TypeFor[bytes.Reader]() {
		return nil, nil, errors.New("XTLS incompatible TLS input buffer layout")
	}
	input := (*bytes.Reader)(unsafe.Pointer(inputField.UnsafeAddr()))
	switch rawField.Type() {
	case reflect.TypeFor[*bytes.Buffer]():
		return input, (**bytes.Buffer)(unsafe.Pointer(rawField.UnsafeAddr())), nil
	case reflect.TypeFor[bytes.Buffer]():
		raw := (*bytes.Buffer)(unsafe.Pointer(rawField.UnsafeAddr()))
		return input, &raw, nil
	default:
		return nil, nil, errors.New("XTLS incompatible TLS input buffer layout")
	}
}

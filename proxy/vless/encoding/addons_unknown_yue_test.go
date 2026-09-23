package encoding_test

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/uuid"
	"github.com/xtls/xray-core/proxy/vless"
	. "github.com/xtls/xray-core/proxy/vless/encoding"
	"google.golang.org/protobuf/encoding/protowire"
)

// [YUE] yue-node reads a connection-level device tag from VLESS request addons
// field 2026 (session.Inbound.RequestAddonsUnknown). The request must decode
// exactly as before, and the unknown bytes must survive Unmarshal intact.
func TestRequestAddonsUnknownFieldSurvivesDecode(t *testing.T) {
	user := &protocol.MemoryUser{Email: "tag@example.com"}
	id := uuid.New()
	user.Account = toAccount(&vless.Account{Id: id.String(), Flow: vless.XRV})
	request := &protocol.RequestHeader{
		Version: Version, User: user, Command: protocol.RequestCommandTCP,
		Address: net.DomainAddress("www.example.com"), Port: net.Port(443),
	}
	unknown := protowire.AppendTag(nil, 2026, protowire.BytesType)
	unknown = protowire.AppendString(unknown, "AbCdEfGhIjK")
	addons := &Addons{Flow: vless.XRV}
	addons.ProtoReflect().SetUnknown(unknown)

	buffer := buf.StackNew()
	common.Must(EncodeRequestHeader(&buffer, request, addons))
	validator := new(vless.MemoryValidator)
	validator.Add(user)

	_, decoded, decodedAddons, _, err := DecodeRequestHeader(false, nil, &buffer, validator)
	if err != nil {
		t.Fatalf("a request carrying an unknown addons field must decode: %v", err)
	}
	if decoded.User.Email != user.Email || decodedAddons.Flow != vless.XRV {
		t.Fatalf("decoded request changed: user=%q flow=%q", decoded.User.Email, decodedAddons.Flow)
	}
	if got := decodedAddons.ProtoReflect().GetUnknown(); !bytes.Equal(got, unknown) {
		t.Fatalf("unknown addons bytes = %x, want %x", got, unknown)
	}
}

// The inbound handler must hand those bytes to the embedder.
func TestInboundPublishesRequestAddonsUnknown(t *testing.T) {
	source, err := os.ReadFile("../inbound/inbound.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), "inbound.RequestAddonsUnknown = requestAddons.ProtoReflect().GetUnknown()") {
		t.Fatal("proxy/vless/inbound no longer publishes request addons unknown fields to session.Inbound")
	}
}

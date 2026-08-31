//go:build !yue_profile_vless

package inbound

import (
	"context"

	"github.com/xtls/xray-core/proxy"
	hysteria_proxy "github.com/xtls/xray-core/proxy/hysteria"
	"github.com/xtls/xray-core/transport/internet/hysteria"
)

func roleInboundListenContext(inbound proxy.Inbound) context.Context {
	ctx := context.Background()
	if server, ok := inbound.(*hysteria_proxy.Server); ok {
		ctx = hysteria.ContextWithValidator(ctx, server.HysteriaInboundValidator())
	}
	return ctx
}

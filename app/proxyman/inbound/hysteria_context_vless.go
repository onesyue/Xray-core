//go:build yue_profile_vless && !yue_profile_hy2

package inbound

import (
	"context"

	"github.com/xtls/xray-core/proxy"
)

func roleInboundListenContext(_ proxy.Inbound) context.Context { return context.Background() }

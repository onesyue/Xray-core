//go:build !yue_profile_vless

package dispatcher

import (
	"context"

	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/protocol/quic"
)

func roleProtocolSniffers() []protocolSnifferWithMetadata {
	return []protocolSnifferWithMetadata{
		{func(_ context.Context, payload []byte) (SniffResult, error) { return quic.SniffQUIC(payload) }, false, net.Network_UDP},
	}
}

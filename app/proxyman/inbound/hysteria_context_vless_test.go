//go:build yue_profile_vless && !yue_profile_hy2

package inbound

import "testing"

func TestVLESSProfileUsesTransportNeutralListenContext(t *testing.T) {
	if ctx := roleInboundListenContext(nil); ctx == nil {
		t.Fatal("roleInboundListenContext returned nil")
	}
}

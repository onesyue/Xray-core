//go:build yue_profile_vless && !yue_profile_hy2

package dispatcher

import "testing"

func TestVLESSProfileExcludesRoleProtocolSniffers(t *testing.T) {
	if got := len(roleProtocolSniffers()); got != 0 {
		t.Fatalf("roleProtocolSniffers length = %d, want 0", got)
	}
}

//go:build yue_profile_vless

package router

import (
	"os/exec"
	"strings"
	"testing"
)

func TestVLESSProfileRefusesRoutingScripts(t *testing.T) {
	if _, err := newScriptEngine("unused.lua"); err == nil ||
		!strings.Contains(err.Error(), "not supported in the yue_profile_vless build") {
		t.Fatalf("newScriptEngine error = %v, want profile refusal", err)
	}
}

// The reason for the split: app/router must not pull app/dns (and through its
// DoQ name server, quic-go) into the VLESS dependency graph.
func TestVLESSProfileRouterExcludesAppDNSAndQUIC(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "-tags", "yue_profile_vless", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	deps := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(deps) < 20 {
		t.Fatalf("go list returned only %d packages; the graph was not inspected", len(deps))
	}
	for _, dep := range deps {
		if dep == "github.com/xtls/xray-core/app/dns" || strings.HasPrefix(dep, "github.com/apernet/quic-go") {
			t.Errorf("VLESS-profile app/router depends on %s", dep)
		}
	}
}

//go:build !yue_profile_vless

package router

import (
	"github.com/xtls/xray-core/app/dns"
	featuredns "github.com/xtls/xray-core/features/dns"
	lua "github.com/yuin/gopher-lua"
)

func routingScriptSupported() error { return nil }

// registerScriptDNS exposes the DNS client to routing scripts as upstream does.
func registerScriptDNS(L *lua.LState, client featuredns.Client) {
	dns.RegisterLua(L, client)
}

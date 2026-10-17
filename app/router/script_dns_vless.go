//go:build yue_profile_vless

package router

import (
	"github.com/xtls/xray-core/common/errors"
	featuredns "github.com/xtls/xray-core/features/dns"
	lua "github.com/yuin/gopher-lua"
)

// Yue's VLESS role never configures a Lua routing script. Upstream's script
// engine binds app/dns into every script, and app/dns links the DoQ name
// server and with it quic-go, which the VLESS artifact must not contain. The
// profile therefore drops the binding and refuses a configured script outright
// instead of running it with a silently missing `dns` global.
func routingScriptSupported() error {
	return errors.New("routing scripts are not supported in the yue_profile_vless build")
}

func registerScriptDNS(*lua.LState, featuredns.Client) {}

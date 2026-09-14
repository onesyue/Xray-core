package session_test

import (
	"context"
	"testing"

	"github.com/xtls/xray-core/common/session"
)

type sessionTestCounter int64

func (c *sessionTestCounter) Value() int64      { return int64(*c) }
func (c *sessionTestCounter) Set(v int64) int64 { *c = sessionTestCounter(v); return v }
func (c *sessionTestCounter) Add(v int64) int64 { *c += sessionTestCounter(v); return int64(*c) }

// RequiresSplicePacing is the fail-closed half of paced splice: the embedder
// sets it per session, and proxy.CopyRawConnIfExist refuses raw splice when it
// is set but no pacer is reachable. It must be explicit, never inferred.
func TestRequiresSplicePacingIsExplicitPerSession(t *testing.T) {
	var counter sessionTestCounter
	// Counters alone say "meter this", not "cap this": an unlimited user has
	// counters too and must keep zero-copy splice.
	metered := &session.Inbound{UserUplinkCounter: &counter, UserDownlinkCounter: &counter, CanSpliceCopy: 1}
	if metered.RequiresSplicePacing {
		t.Fatal("RequiresSplicePacing must default to false; counters do not imply a ceiling")
	}

	capped := &session.Inbound{CanSpliceCopy: 1, RequiresSplicePacing: true}
	ctx := session.ContextWithInbound(context.Background(), capped)
	got := session.InboundFromContext(ctx)
	if got == nil || !got.RequiresSplicePacing {
		t.Fatal("RequiresSplicePacing did not survive the context round trip")
	}
	// The flag rides on the same *Inbound the copy loop re-reads every
	// iteration, so a later change by the embedder is observed, not snapshotted.
	capped.RequiresSplicePacing = false
	if session.InboundFromContext(ctx).RequiresSplicePacing {
		t.Fatal("context returned a copy of Inbound; the copy loop would read a stale flag")
	}
}

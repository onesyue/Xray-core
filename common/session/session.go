// Package session provides functions for sessions of incoming requests.
package session // import "github.com/xtls/xray-core/common/session"

import (
	"context"
	"math/rand"

	c "github.com/xtls/xray-core/common/ctx"
	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/common/geodata"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/signal"
	"github.com/xtls/xray-core/features/stats"
)

// NewID generates a new ID. The generated ID is high likely to be unique, but not cryptographically secure.
// The generated ID will never be 0.
func NewID() c.ID {
	for {
		id := c.ID(rand.Uint32())
		if id != 0 {
			return id
		}
	}
}

// ExportIDToError transfers session.ID into an error object, for logging purpose.
// This can be used with error.WriteToLog().
func ExportIDToError(ctx context.Context) errors.ExportOption {
	id := c.IDFromContext(ctx)
	return func(h *errors.ExportOptionHolder) {
		h.SessionID = uint32(id)
	}
}

// Inbound is the metadata of an inbound connection.
type Inbound struct {
	// Source address of the inbound connection.
	Source net.Destination
	// Local address of the inbound connection.
	Local net.Destination
	// Gateway address.
	Gateway net.Destination
	// Tag of the inbound proxy that handles the connection.
	Tag string
	// Name of the inbound proxy that handles the connection.
	Name string
	// User is the user that authenticates for the inbound. May be nil if the protocol allows anonymous traffic.
	User *protocol.MemoryUser
	// VlessRoute is the user-sent VLESS UUID's 7th<<8 | 8th bytes.
	VlessRoute net.Port
	// Used by splice copy. Conn is actually internet.Connection. May be nil.
	Conn net.Conn
	// Used by splice copy. Timer of the inbound buf copier. May be nil.
	Timer *signal.ActivityTimer
	// CanSpliceCopy is a property for this connection
	// 1 = can, 2 = after processing protocol info should be able to, 3 = cannot
	CanSpliceCopy int
	// UserUplinkCounter and UserDownlinkCounter are optional counters injected
	// by an embedding application. They remain visible when Vision switches
	// from buffered copy to raw kernel splice.
	UserUplinkCounter   stats.Counter
	UserDownlinkCounter stats.Counter
	// RequiresSplicePacing marks a credential whose byte rate is capped. Raw
	// splice bypasses every buf.Writer, so the copy may only take that path
	// when a SplicePacer is actually reachable from the writer chain. When this
	// is set and no pacer is found, the copy falls back to the buffered path:
	// serving an unmetered ceiling is worse than losing the zero-copy win.
	RequiresSplicePacing bool
	// RequestAddonsUnknown holds the unknown fields of the VLESS request
	// addons exactly as the client sent them (nil when there were none). The
	// embedding application parses its own extensions from it — yue-node reads
	// a connection-level device tag from field 2026. Xray never interprets it,
	// and a malformed or absent value must never fail the connection.
	RequestAddonsUnknown []byte
}

// Outbound is the metadata of an outbound connection.
type Outbound struct {
	// Target address of the outbound connection.
	OriginalTarget net.Destination
	Target         net.Destination
	RouteTarget    net.Destination
	// Gateway address
	Gateway net.Address
	// Tag of the outbound proxy that handles the connection.
	Tag string
	// Name of the outbound proxy that handles the connection.
	Name string
	// CanSpliceCopy is a property for this connection
	// 1 = can, 2 = after processing protocol info should be able to, 3 = cannot
	CanSpliceCopy int
}

// SniffingRequest controls the behavior of content sniffing. They are from inbound config. Read-only
type SniffingRequest struct {
	ExcludeForDomain               geodata.DomainMatcher
	ExcludeForIP                   geodata.IPMatcher
	OverrideDestinationForProtocol []string
	Enabled                        bool
	MetadataOnly                   bool
	RouteOnly                      bool
}

// Content is the metadata of the connection content. Mainly used for routing.
type Content struct {
	// Protocol of current content.
	Protocol string

	SniffingRequest SniffingRequest

	// HTTP traffic sniffed headers
	Attributes map[string]string

	// SkipDNSResolve is set from DNS module. the DOH remote server maybe a domain name, this prevents cycle resolving dead loop
	SkipDNSResolve bool
}

// Sockopt is the settings for socket connection.
type Sockopt struct {
	// Mark of the socket connection.
	Mark int32
}

// SetAttribute attaches additional string attributes to content.
func (c *Content) SetAttribute(name string, value string) {
	if c.Attributes == nil {
		c.Attributes = make(map[string]string)
	}
	c.Attributes[name] = value
}

// Attribute retrieves additional string attributes from content.
func (c *Content) Attribute(name string) string {
	if c.Attributes == nil {
		return ""
	}
	return c.Attributes[name]
}

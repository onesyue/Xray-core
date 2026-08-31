package inbound

import (
	"context"

	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/features"
)

// Handler is the interface for handlers that process inbound connections.
//
// xray:api:stable
type Handler interface {
	common.Runnable
	// The tag of this handler.
	Tag() string
	// Returns the active receiver settings.
	ReceiverSettings() *serial.TypedMessage
	// Returns the active proxy settings.
	ProxySettings() *serial.TypedMessage
}

// DrainingHandler can stop creating new sessions without closing protocol
// state used by sessions that were already accepted. It remains separate from
// Handler so third-party implementations keep source compatibility.
type DrainingHandler interface {
	Handler
	StopAccepting() error
}

// Manager is a feature that manages InboundHandlers.
//
// xray:api:stable
type Manager interface {
	features.Feature
	// GetHandler returns an InboundHandler for the given tag.
	GetHandler(ctx context.Context, tag string) (Handler, error)
	// AddHandler adds the given handler into this Manager.
	AddHandler(ctx context.Context, handler Handler) error

	// RemoveHandler removes a handler from Manager.
	RemoveHandler(ctx context.Context, tag string) error

	// ListHandlers returns a list of inbound.Handler.
	ListHandlers(ctx context.Context) []Handler
}

// DrainingManager retires a tagged listener from routing while retaining the
// handler until Manager.Close performs final protocol-state cleanup.
type DrainingManager interface {
	Manager
	StopAccepting(ctx context.Context, tag string) error
}

// ManagerType returns the type of Manager interface. Can be used for implementing common.HasType.
//
// xray:api:stable
func ManagerType() interface{} {
	return (*Manager)(nil)
}

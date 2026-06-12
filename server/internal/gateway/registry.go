package gateway

import (
	"context"
	"sync"

	"github.com/google/uuid"

	rmmpb "github.com/codex666-cenotaph/rmmagic/shared/rmmpb/rmm/v1"
)

// conn is the registry's handle on one live agent connection.
type conn interface {
	Send(ctx context.Context, env *rmmpb.Envelope) error
	Close(reason string)
}

// Registry tracks live agent connections on this gateway instance. A
// device has at most one connection; a newer connection replaces (and
// closes) the older one. Multi-gateway routing via NATS KV comes with
// the horizontal-scaling work.
type Registry struct {
	mu    sync.RWMutex
	conns map[uuid.UUID]conn
}

func NewRegistry() *Registry {
	return &Registry{conns: map[uuid.UUID]conn{}}
}

// add registers c, closing any previous connection for the device.
func (r *Registry) add(deviceID uuid.UUID, c conn) {
	r.mu.Lock()
	prev := r.conns[deviceID]
	r.conns[deviceID] = c
	r.mu.Unlock()
	if prev != nil {
		prev.Close("replaced by newer connection")
	}
}

// remove drops the mapping if it still points at c.
func (r *Registry) remove(deviceID uuid.UUID, c conn) {
	r.mu.Lock()
	if r.conns[deviceID] == c {
		delete(r.conns, deviceID)
	}
	r.mu.Unlock()
}

// Connected reports whether the device has a live connection here.
func (r *Registry) Connected(deviceID uuid.UUID) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.conns[deviceID]
	return ok
}

// Send delivers an envelope to a connected device; returns false when
// the device is not connected to this instance.
func (r *Registry) Send(ctx context.Context, deviceID uuid.UUID, env *rmmpb.Envelope) bool {
	r.mu.RLock()
	c := r.conns[deviceID]
	r.mu.RUnlock()
	if c == nil {
		return false
	}
	return c.Send(ctx, env) == nil
}

// Kick closes the device's connection (e.g. after decommission).
func (r *Registry) Kick(deviceID uuid.UUID, reason string) {
	r.mu.RLock()
	c := r.conns[deviceID]
	r.mu.RUnlock()
	if c != nil {
		c.Close(reason)
	}
}

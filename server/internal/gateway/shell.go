package gateway

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"

	rmmpb "github.com/codex666-cenotaph/rmmagic/shared/rmmpb/rmm/v1"
)

// shellOutputBuffer bounds in-flight terminal output per session before
// the shared gateway read loop applies backpressure.
const shellOutputBuffer = 256

// routeBlockMax is how long the read loop will wait to hand off output to
// a slow consumer before dropping the chunk. Bounded so one wedged
// browser cannot stall heartbeats/results for the device's connection.
const routeBlockMax = 2 * time.Second

// ShellSink carries one session's agent->browser terminal output. The
// API handler that owns the browser WebSocket drains Output and watches
// Done (closed when the agent's PTY exits).
type ShellSink struct {
	Output    chan []byte
	done      chan struct{}
	closeOnce sync.Once
}

// Done is closed when the agent reports the session has ended.
func (s *ShellSink) Done() <-chan struct{} { return s.done }

func (s *ShellSink) close() { s.closeOnce.Do(func() { close(s.done) }) }

// RegisterShell allocates a sink for sessionID so agent output can be
// routed to the caller. The caller must UnregisterShell when done.
func (g *Gateway) RegisterShell(sessionID string) *ShellSink {
	s := &ShellSink{Output: make(chan []byte, shellOutputBuffer), done: make(chan struct{})}
	g.shellMu.Lock()
	g.shellSinks[sessionID] = s
	g.shellMu.Unlock()
	return s
}

// UnregisterShell drops the sink and closes its Done channel.
func (g *Gateway) UnregisterShell(sessionID string) {
	g.shellMu.Lock()
	s := g.shellSinks[sessionID]
	delete(g.shellSinks, sessionID)
	g.shellMu.Unlock()
	if s != nil {
		s.close()
	}
}

// endShell closes the sink's Done channel without removing it, letting
// the owning handler observe the agent-side termination and clean up.
func (g *Gateway) endShell(sessionID string) {
	g.shellMu.Lock()
	s := g.shellSinks[sessionID]
	g.shellMu.Unlock()
	if s != nil {
		s.close()
	}
}

// routeShellOutput forwards an agent ShellData frame to the session's
// sink. It runs on the device's shared read loop, so delivery is bounded
// and lossy under sustained backpressure rather than blocking forever.
func (g *Gateway) routeShellOutput(ctx context.Context, d *rmmpb.ShellData) {
	if d == nil || d.SessionId == "" {
		return
	}
	g.shellMu.Lock()
	s := g.shellSinks[d.SessionId]
	g.shellMu.Unlock()
	if s == nil {
		return
	}
	timer := time.NewTimer(routeBlockMax)
	defer timer.Stop()
	select {
	case s.Output <- d.Data:
	case <-s.done:
	case <-ctx.Done():
	case <-timer.C:
		g.Log.Warn("shell output dropped (slow consumer)", "session_id", d.SessionId)
	}
}

// Online reports whether the device has a live connection to this
// gateway instance (shell requires the agent and API to share a process
// at MVP, before NATS routing).
func (g *Gateway) Online(deviceID uuid.UUID) bool {
	return g.Registry.Connected(deviceID)
}

// StartShell asks the agent to open a PTY session. Returns false when the
// device is not connected here.
func (g *Gateway) StartShell(ctx context.Context, deviceID uuid.UUID, sessionID string, cols, rows uint32) bool {
	return g.Registry.Send(ctx, deviceID, &rmmpb.Envelope{
		MessageId: uuid.NewString(),
		Payload: &rmmpb.Envelope_ShellStart{ShellStart: &rmmpb.ShellStart{
			SessionId: sessionID, Cols: cols, Rows: rows,
		}},
	})
}

// SendShellInput forwards browser keystrokes to the agent PTY.
func (g *Gateway) SendShellInput(ctx context.Context, deviceID uuid.UUID, sessionID string, data []byte) bool {
	return g.Registry.Send(ctx, deviceID, &rmmpb.Envelope{
		MessageId: uuid.NewString(),
		Payload: &rmmpb.Envelope_ShellInput{ShellInput: &rmmpb.ShellData{
			SessionId: sessionID, Data: data,
		}},
	})
}

// SendShellResize forwards a terminal resize to the agent PTY.
func (g *Gateway) SendShellResize(ctx context.Context, deviceID uuid.UUID, sessionID string, cols, rows uint32) bool {
	return g.Registry.Send(ctx, deviceID, &rmmpb.Envelope{
		MessageId: uuid.NewString(),
		Payload: &rmmpb.Envelope_ShellResize{ShellResize: &rmmpb.ShellResize{
			SessionId: sessionID, Cols: cols, Rows: rows,
		}},
	})
}

// StopShell tells the agent to terminate the PTY session.
func (g *Gateway) StopShell(ctx context.Context, deviceID uuid.UUID, sessionID string) {
	g.Registry.Send(ctx, deviceID, &rmmpb.Envelope{
		MessageId: uuid.NewString(),
		Payload:   &rmmpb.Envelope_ShellStop{ShellStop: &rmmpb.ShellStop{SessionId: sessionID}},
	})
}

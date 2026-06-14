// Package shell runs interactive PTY sessions on the endpoint for the
// remote-shell feature. Sessions are multiplexed over the agent's
// existing gateway connection by session ID; output is streamed back to
// the server (and on to the technician's browser) as it is produced.
//
// PTY allocation is platform-specific (see pty_linux.go / pty_other.go);
// the cross-platform agent builds everywhere but only Linux can open a
// real shell.
package shell

import (
	"io"
	"log/slog"
	"sync"
)

// readBuf is the PTY read chunk size; matches a typical terminal burst.
const readBuf = 32 * 1024

// OutputFunc delivers a chunk of PTY output for a session. The slice is
// owned by the callee.
type OutputFunc func(sessionID string, data []byte)

// ExitFunc is invoked exactly once when a session's PTY has exited.
type ExitFunc func(sessionID string)

// ptySession is a platform PTY handle: terminal I/O plus resize.
type ptySession interface {
	io.ReadWriteCloser
	Resize(cols, rows uint16) error
}

// Manager owns the agent's live PTY sessions, keyed by session ID.
type Manager struct {
	log      *slog.Logger
	onOutput OutputFunc
	onExit   ExitFunc

	mu       sync.Mutex
	sessions map[string]ptySession
}

func NewManager(log *slog.Logger, onOutput OutputFunc, onExit ExitFunc) *Manager {
	return &Manager{
		log:      log,
		onOutput: onOutput,
		onExit:   onExit,
		sessions: map[string]ptySession{},
	}
}

// Start opens a PTY for sessionID and begins streaming its output. It is
// idempotent: a duplicate start (e.g. command redelivery) is ignored.
func (m *Manager) Start(sessionID string, cols, rows uint16) error {
	if sessionID == "" {
		return nil
	}
	m.mu.Lock()
	if _, ok := m.sessions[sessionID]; ok {
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()

	ps, err := startPTY(cols, rows)
	if err != nil {
		return err
	}

	m.mu.Lock()
	// Lost a race with another Start for the same ID: close the loser.
	if _, ok := m.sessions[sessionID]; ok {
		m.mu.Unlock()
		_ = ps.Close()
		return nil
	}
	m.sessions[sessionID] = ps
	m.mu.Unlock()

	go m.pump(sessionID, ps)
	return nil
}

// pump streams PTY output until it closes, then fires onExit once.
func (m *Manager) pump(sessionID string, ps ptySession) {
	buf := make([]byte, readBuf)
	for {
		n, err := ps.Read(buf)
		if n > 0 && m.onOutput != nil {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			m.onOutput(sessionID, chunk)
		}
		if err != nil {
			break
		}
	}
	m.closeSession(sessionID)
	if m.onExit != nil {
		m.onExit(sessionID)
	}
}

// Input writes browser keystrokes to the session's PTY.
func (m *Manager) Input(sessionID string, data []byte) {
	m.mu.Lock()
	ps := m.sessions[sessionID]
	m.mu.Unlock()
	if ps == nil {
		return
	}
	if _, err := ps.Write(data); err != nil {
		m.log.Warn("shell input write failed", "session_id", sessionID, "error", err)
	}
}

// Resize applies a new terminal size to the session's PTY.
func (m *Manager) Resize(sessionID string, cols, rows uint16) {
	m.mu.Lock()
	ps := m.sessions[sessionID]
	m.mu.Unlock()
	if ps == nil {
		return
	}
	if err := ps.Resize(cols, rows); err != nil {
		m.log.Warn("shell resize failed", "session_id", sessionID, "error", err)
	}
}

// Stop terminates one session (server-initiated close). The pump goroutine
// observes the closed PTY and fires onExit.
func (m *Manager) Stop(sessionID string) {
	m.closeSession(sessionID)
}

// StopAll terminates every session; called when the gateway connection
// drops so no PTY is left orphaned.
func (m *Manager) StopAll() {
	m.mu.Lock()
	sessions := m.sessions
	m.sessions = map[string]ptySession{}
	m.mu.Unlock()
	for _, ps := range sessions {
		_ = ps.Close()
	}
}

// closeSession closes and removes a session if present; safe to call more
// than once for the same ID.
func (m *Manager) closeSession(sessionID string) {
	m.mu.Lock()
	ps := m.sessions[sessionID]
	delete(m.sessions, sessionID)
	m.mu.Unlock()
	if ps != nil {
		_ = ps.Close()
	}
}

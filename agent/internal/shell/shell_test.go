//go:build linux

package shell

import (
	"bytes"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// collector is a concurrency-safe sink for session output.
type collector struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (c *collector) write(_ string, data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.buf.Write(data)
}

func (c *collector) contains(s string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.Contains(c.buf.String(), s)
}

func waitFor(t *testing.T, cond func() bool, d time.Duration, msg string) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", msg)
}

func TestManagerEchoAndExit(t *testing.T) {
	col := &collector{}
	exited := make(chan string, 1)
	m := NewManager(slog.New(slog.NewTextHandler(io.Discard, nil)), col.write, func(id string) {
		exited <- id
	})

	if err := m.Start("s1", 80, 24); err != nil {
		t.Fatalf("start: %v", err)
	}

	// The PTY echoes input and the shell runs the command; both paths put
	// the marker in the stream.
	const marker = "rmm_marker_42"
	m.Input("s1", []byte("echo "+marker+"\n"))
	waitFor(t, func() bool { return col.contains(marker) }, 5*time.Second, "command output")

	// Resize must not error on a live session.
	m.Resize("s1", 120, 40)

	// Exiting the shell ends the session and fires onExit exactly once.
	m.Input("s1", []byte("exit\n"))
	select {
	case id := <-exited:
		if id != "s1" {
			t.Fatalf("onExit for wrong session: %q", id)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("session did not exit")
	}
}

func TestManagerStopFiresExit(t *testing.T) {
	exited := make(chan string, 1)
	m := NewManager(slog.New(slog.NewTextHandler(io.Discard, nil)),
		func(string, []byte) {}, func(id string) { exited <- id })

	if err := m.Start("s2", 80, 24); err != nil {
		t.Fatalf("start: %v", err)
	}
	m.Stop("s2")
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not fire onExit")
	}

	// Input/Resize on an unknown or stopped session must be no-ops.
	m.Input("s2", []byte("noop"))
	m.Resize("s2", 10, 10)
	m.Input("does-not-exist", []byte("noop"))
}

func TestManagerStopAll(t *testing.T) {
	var mu sync.Mutex
	exits := map[string]bool{}
	m := NewManager(slog.New(slog.NewTextHandler(io.Discard, nil)),
		func(string, []byte) {}, func(id string) {
			mu.Lock()
			exits[id] = true
			mu.Unlock()
		})
	if err := m.Start("a", 80, 24); err != nil {
		t.Fatal(err)
	}
	if err := m.Start("b", 80, 24); err != nil {
		t.Fatal(err)
	}
	m.StopAll()
	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return exits["a"] && exits["b"]
	}, 5*time.Second, "both sessions to exit")
}

package gateway

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	rmmpb "github.com/codex666-cenotaph/rmmagic/shared/rmmpb/rmm/v1"
)

// fakeConn captures envelopes the gateway sends to a "device".
type fakeConn struct{ sent chan *rmmpb.Envelope }

func (c *fakeConn) Send(_ context.Context, env *rmmpb.Envelope) error {
	c.sent <- env
	return nil
}
func (c *fakeConn) Close(string) {}

func newTestGateway() *Gateway {
	return &Gateway{Registry: NewRegistry(), Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		shellSinks: map[string]*ShellSink{}}
}

func TestShellSendHelpersEmitFrames(t *testing.T) {
	g := newTestGateway()
	dev := uuid.New()
	fc := &fakeConn{sent: make(chan *rmmpb.Envelope, 8)}
	g.Registry.add(dev, fc)

	ctx := context.Background()
	if !g.StartShell(ctx, dev, "s1", 100, 30) {
		t.Fatal("StartShell returned false for a connected device")
	}
	if start := (<-fc.sent).GetShellStart(); start == nil || start.SessionId != "s1" || start.Cols != 100 {
		t.Fatalf("unexpected ShellStart: %v", start)
	}
	g.SendShellInput(ctx, dev, "s1", []byte("ls\n"))
	if in := (<-fc.sent).GetShellInput(); in == nil || string(in.Data) != "ls\n" {
		t.Fatalf("unexpected ShellInput: %v", in)
	}
	g.SendShellResize(ctx, dev, "s1", 120, 40)
	if rs := (<-fc.sent).GetShellResize(); rs == nil || rs.Rows != 40 {
		t.Fatalf("unexpected ShellResize: %v", rs)
	}
	g.StopShell(ctx, dev, "s1")
	if st := (<-fc.sent).GetShellStop(); st == nil || st.SessionId != "s1" {
		t.Fatalf("unexpected ShellStop: %v", st)
	}

	// An offline device cannot start a shell.
	if g.StartShell(ctx, uuid.New(), "s2", 80, 24) {
		t.Fatal("StartShell should fail for an unconnected device")
	}
}

func TestShellOutputRoutingAndEnd(t *testing.T) {
	g := newTestGateway()
	sink := g.RegisterShell("sess")

	g.routeShellOutput(context.Background(), &rmmpb.ShellData{SessionId: "sess", Data: []byte("hi")})
	select {
	case data := <-sink.Output:
		if string(data) != "hi" {
			t.Fatalf("got %q", data)
		}
	case <-time.After(time.Second):
		t.Fatal("output not routed to sink")
	}

	// Output for an unknown session is dropped, not a panic.
	g.routeShellOutput(context.Background(), &rmmpb.ShellData{SessionId: "ghost", Data: []byte("x")})

	// The agent ending the session closes Done.
	g.endShell("sess")
	select {
	case <-sink.Done():
	case <-time.After(time.Second):
		t.Fatal("endShell did not close Done")
	}

	g.UnregisterShell("sess")
	if g.shellSink("sess") != nil {
		t.Fatal("sink still registered after UnregisterShell")
	}
}

// shellSink is a test accessor for the private map.
func (g *Gateway) shellSink(id string) *ShellSink {
	g.shellMu.Lock()
	defer g.shellMu.Unlock()
	return g.shellSinks[id]
}

// Package gateway terminates persistent agent WebSocket connections:
// challenge-response device authentication, heartbeat liveness, and
// command delivery (M3+) over the rmm.v1 protobuf envelope protocol.
package gateway

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/codex666-cenotaph/rmmagic/server/internal/store"
	"github.com/codex666-cenotaph/rmmagic/shared/devicesig"
	rmmpb "github.com/codex666-cenotaph/rmmagic/shared/rmmpb/rmm/v1"
)

const (
	authTimeout       = 10 * time.Second
	heartbeatInterval = 30 * time.Second
	// Connections silent for 3 intervals are presumed dead.
	readDeadline = 3 * heartbeatInterval
	maxFrameSize = 1 << 20
)

type Gateway struct {
	Store    *store.Store
	Registry *Registry
	Log      *slog.Logger
	// InsecureAllowed permits non-TLS upgrades (dev only).
}

func New(st *store.Store, log *slog.Logger) *Gateway {
	return &Gateway{Store: st, Registry: NewRegistry(), Log: log}
}

// HandleConnect is mounted at GET /agent/v1/connect.
func (g *Gateway) HandleConnect(w http.ResponseWriter, r *http.Request) {
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// Agents are not browsers; origin checks don't apply.
		OriginPatterns: []string{"*"},
	})
	if err != nil {
		return
	}
	ws.SetReadLimit(maxFrameSize)

	ac := &agentConn{ws: ws}
	deviceID, tenantID, err := g.authenticate(r.Context(), ac)
	if err != nil {
		g.Log.Info("agent auth failed", "error", err, "remote", r.RemoteAddr)
		ws.Close(websocket.StatusPolicyViolation, "authentication failed")
		return
	}

	g.Registry.add(deviceID, ac)
	defer g.Registry.remove(deviceID, ac)
	g.Log.Info("agent connected", "device_id", deviceID)

	g.serve(r.Context(), ac, deviceID, tenantID)
	g.Log.Info("agent disconnected", "device_id", deviceID)
}

// authenticate runs the challenge-response handshake and returns the
// verified device identity.
func (g *Gateway) authenticate(ctx context.Context, ac *agentConn) (uuid.UUID, uuid.UUID, error) {
	ctx, cancel := context.WithTimeout(ctx, authTimeout)
	defer cancel()

	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	if err := ac.Send(ctx, &rmmpb.Envelope{
		MessageId: uuid.NewString(),
		Payload:   &rmmpb.Envelope_AuthChallenge{AuthChallenge: &rmmpb.AuthChallenge{Nonce: nonce}},
	}); err != nil {
		return uuid.Nil, uuid.Nil, err
	}

	env, err := ac.Read(ctx)
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	resp := env.GetAuthResponse()
	if resp == nil {
		return uuid.Nil, uuid.Nil, errors.New("expected auth response")
	}
	deviceID, err := uuid.Parse(resp.DeviceId)
	if err != nil {
		return uuid.Nil, uuid.Nil, errors.New("invalid device id")
	}

	var dev store.AuthDevice
	err = g.Store.System(ctx, func(tx pgx.Tx) error {
		var err error
		dev, err = store.LookupDevice(ctx, tx, deviceID)
		return err
	})
	if err != nil {
		return uuid.Nil, uuid.Nil, errors.New("unknown device")
	}
	if dev.Status != "active" {
		return uuid.Nil, uuid.Nil, errors.New("device not active")
	}
	if !devicesig.VerifyChallenge(ed25519.PublicKey(dev.Pubkey), nonce, resp.Signature) {
		return uuid.Nil, uuid.Nil, errors.New("bad signature")
	}
	return deviceID, dev.TenantID, nil
}

// serve runs the post-auth read loop.
func (g *Gateway) serve(ctx context.Context, ac *agentConn, deviceID, tenantID uuid.UUID) {
	lastTouch := time.Time{}
	for {
		readCtx, cancel := context.WithTimeout(ctx, readDeadline)
		env, err := ac.Read(readCtx)
		cancel()
		if err != nil {
			return
		}

		switch p := env.Payload.(type) {
		case *rmmpb.Envelope_Hello:
			if err := g.touch(ctx, tenantID, deviceID, p.Hello.AgentVersion, &lastTouch, true); err != nil {
				g.Log.Error("hello touch failed", "device_id", deviceID, "error", err)
			}
			_ = ac.Send(ctx, &rmmpb.Envelope{
				MessageId: uuid.NewString(),
				InReplyTo: env.MessageId,
				Payload: &rmmpb.Envelope_HelloAck{HelloAck: &rmmpb.HelloAck{
					ServerTime:         timestamppb.Now(),
					HeartbeatIntervalS: uint32(heartbeatInterval / time.Second),
				}},
			})
		case *rmmpb.Envelope_Heartbeat:
			if err := g.touch(ctx, tenantID, deviceID, "", &lastTouch, false); err != nil {
				g.Log.Error("heartbeat touch failed", "device_id", deviceID, "error", err)
			}
		case *rmmpb.Envelope_Ack:
			// Command acks are wired up in M3.
		default:
			g.Log.Warn("unexpected frame from agent", "device_id", deviceID)
		}
	}
}

// touch updates last_seen_at, throttled so heartbeats don't write the
// DB more than once per interval.
func (g *Gateway) touch(ctx context.Context, tenantID, deviceID uuid.UUID, agentVersion string, last *time.Time, force bool) error {
	if !force && time.Since(*last) < heartbeatInterval/2 {
		return nil
	}
	*last = time.Now()
	return g.Store.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		return store.TouchDevice(ctx, tx, deviceID, agentVersion)
	})
}

// Decommission notifies a live agent and closes its connection.
func (g *Gateway) Decommission(ctx context.Context, deviceID uuid.UUID) {
	g.Registry.Send(ctx, deviceID, &rmmpb.Envelope{
		MessageId: uuid.NewString(),
		Payload:   &rmmpb.Envelope_Decommission{Decommission: &rmmpb.Decommission{}},
	})
	g.Registry.Kick(deviceID, "decommissioned")
}

// agentConn wraps a websocket with protobuf framing and a write lock.
type agentConn struct {
	ws *websocket.Conn
	mu sync.Mutex
}

func (c *agentConn) Send(ctx context.Context, env *rmmpb.Envelope) error {
	b, err := proto.Marshal(env)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ws.Write(ctx, websocket.MessageBinary, b)
}

func (c *agentConn) Read(ctx context.Context) (*rmmpb.Envelope, error) {
	typ, b, err := c.ws.Read(ctx)
	if err != nil {
		return nil, err
	}
	if typ != websocket.MessageBinary {
		return nil, errors.New("expected binary frame")
	}
	env := &rmmpb.Envelope{}
	if err := proto.Unmarshal(b, env); err != nil {
		return nil, err
	}
	return env, nil
}

func (c *agentConn) Close(reason string) {
	_ = c.ws.Close(websocket.StatusNormalClosure, reason)
}

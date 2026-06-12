// Package conn implements the agent's lifecycle against the platform:
// enrollment over HTTPS, the persistent gateway WebSocket (challenge
// auth, heartbeats, command dispatch), and signed stats uploads.
package conn

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/codex666-cenotaph/rmmagic/agent/internal/collect"
	agentexec "github.com/codex666-cenotaph/rmmagic/agent/internal/exec"
	"github.com/codex666-cenotaph/rmmagic/agent/internal/identity"
	"github.com/codex666-cenotaph/rmmagic/shared/devicesig"
	rmmpb "github.com/codex666-cenotaph/rmmagic/shared/rmmpb/rmm/v1"
	"github.com/codex666-cenotaph/rmmagic/shared/version"
)

// ErrDecommissioned signals the server revoked this device; the agent
// must stop reconnecting.
var ErrDecommissioned = errors.New("device decommissioned by server")

const (
	statsInterval = 60 * time.Second
	backoffMax    = 5 * time.Minute
)

// Enroll performs first-time enrollment and persists the identity.
func Enroll(ctx context.Context, serverURL, token, stateDir string) error {
	if identity.Exists(stateDir) {
		return fmt.Errorf("identity already exists in %s (remove it to re-enroll)", stateDir)
	}
	pub, priv, err := identity.GenerateKey()
	if err != nil {
		return err
	}
	hostname, _ := os.Hostname()

	body, _ := json.Marshal(map[string]string{
		"token":         token,
		"hostname":      hostname,
		"os":            runtime.GOOS,
		"arch":          runtime.GOARCH,
		"agent_version": version.Version,
		"pubkey":        base64.StdEncoding.EncodeToString(pub),
	})
	req, err := http.NewRequestWithContext(ctx, "POST",
		strings.TrimRight(serverURL, "/")+"/agent/v1/enroll", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("enrollment request failed: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("enrollment rejected (%d): %s", resp.StatusCode, raw)
	}
	var out struct {
		DeviceID string `json:"device_id"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.DeviceID == "" {
		return fmt.Errorf("unexpected enrollment response")
	}

	return identity.Save(stateDir, &identity.Identity{
		DeviceID:      out.DeviceID,
		ServerURL:     strings.TrimRight(serverURL, "/"),
		PrivateKeyB64: base64.StdEncoding.EncodeToString(priv),
	})
}

// Agent is the running connection manager.
type Agent struct {
	ID      *identity.Identity
	Key     ed25519.PrivateKey
	Log     *slog.Logger
	HTTP    *http.Client
	Journal *agentexec.Journal
}

func NewAgent(id *identity.Identity, log *slog.Logger, journal *agentexec.Journal) (*Agent, error) {
	key, err := id.PrivateKey()
	if err != nil {
		return nil, err
	}
	return &Agent{ID: id, Key: key, Log: log, Journal: journal,
		HTTP: &http.Client{Timeout: 30 * time.Second}}, nil
}

// Run maintains the gateway connection and the stats loop until ctx is
// done or the device is decommissioned.
func (a *Agent) Run(ctx context.Context) error {
	go a.statsLoop(ctx)

	backoff := time.Second
	for {
		err := a.connectAndServe(ctx)
		switch {
		case errors.Is(err, ErrDecommissioned):
			return err
		case ctx.Err() != nil:
			return nil
		}
		// Jittered exponential backoff.
		sleep := backoff/2 + time.Duration(rand.Int64N(int64(backoff)))
		a.Log.Info("reconnecting", "in", sleep.Round(time.Second).String(), "error", err)
		select {
		case <-time.After(sleep):
		case <-ctx.Done():
			return nil
		}
		if backoff *= 2; backoff > backoffMax {
			backoff = backoffMax
		}
	}
}

func (a *Agent) wsURL() string {
	u := a.ID.ServerURL
	u = strings.Replace(u, "https://", "wss://", 1)
	u = strings.Replace(u, "http://", "ws://", 1)
	return u + "/agent/v1/connect"
}

func (a *Agent) connectAndServe(ctx context.Context) error {
	dialCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	ws, _, err := websocket.Dial(dialCtx, a.wsURL(), nil)
	cancel()
	if err != nil {
		return err
	}
	defer ws.Close(websocket.StatusNormalClosure, "")
	// Headroom above a 1 MiB script body plus protobuf framing.
	ws.SetReadLimit(2 << 20)

	// Challenge-response authentication.
	env, err := read(ctx, ws)
	if err != nil {
		return err
	}
	ch := env.GetAuthChallenge()
	if ch == nil {
		return errors.New("expected auth challenge")
	}
	if err := write(ctx, ws, &rmmpb.Envelope{
		Payload: &rmmpb.Envelope_AuthResponse{AuthResponse: &rmmpb.AuthResponse{
			DeviceId:  a.ID.DeviceID,
			Signature: devicesig.SignChallenge(a.Key, ch.Nonce),
		}},
	}); err != nil {
		return err
	}

	hostname, _ := os.Hostname()
	if err := write(ctx, ws, &rmmpb.Envelope{
		Payload: &rmmpb.Envelope_Hello{Hello: &rmmpb.Hello{
			ProtocolVersion: version.ProtocolVersion,
			AgentVersion:    version.Version,
			Os:              runtime.GOOS,
			Arch:            runtime.GOARCH,
			Hostname:        hostname,
		}},
	}); err != nil {
		return err
	}

	env, err = read(ctx, ws)
	if err != nil {
		return fmt.Errorf("no hello ack (auth rejected?): %w", err)
	}
	ack := env.GetHelloAck()
	if ack == nil {
		if env.GetDecommission() != nil {
			return ErrDecommissioned
		}
		return errors.New("expected hello ack")
	}
	interval := time.Duration(ack.HeartbeatIntervalS) * time.Second
	if interval <= 0 {
		interval = 30 * time.Second
	}
	a.Log.Info("connected", "heartbeat_interval", interval.String())

	// Heartbeat writer.
	hbCtx, stopHB := context.WithCancel(ctx)
	defer stopHB()
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				_ = write(hbCtx, ws, &rmmpb.Envelope{
					Payload: &rmmpb.Envelope_Heartbeat{Heartbeat: &rmmpb.Heartbeat{}},
				})
			case <-hbCtx.Done():
				return
			}
		}
	}()

	// Read loop.
	for {
		env, err := read(ctx, ws)
		if err != nil {
			return err
		}
		switch p := env.Payload.(type) {
		case *rmmpb.Envelope_Decommission:
			a.Log.Warn("decommissioned by server")
			return ErrDecommissioned
		case *rmmpb.Envelope_CommandRequest:
			go a.executeCommand(ctx, ws, p.CommandRequest)
		}
	}
}

// statsLoop samples and uploads stats independent of the WS connection
// (HTTPS ingest tolerates gateway hiccups).
func (a *Agent) statsLoop(ctx context.Context) {
	t := time.NewTicker(statsInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s, err := collect.Collect(ctx)
			if err != nil {
				a.Log.Error("collect failed", "error", err)
				continue
			}
			if err := a.postStats(ctx, []collect.Sample{s}); err != nil {
				a.Log.Warn("stats upload failed", "error", err)
			}
		}
	}
}

func (a *Agent) postStats(ctx context.Context, samples []collect.Sample) error {
	body, err := json.Marshal(map[string]any{"samples": samples})
	if err != nil {
		return err
	}
	ts := time.Now().Unix()
	req, err := http.NewRequestWithContext(ctx, "POST",
		a.ID.ServerURL+"/agent/v1/stats", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Device-Id", a.ID.DeviceID)
	req.Header.Set("X-Timestamp", strconv.FormatInt(ts, 10))
	req.Header.Set("X-Signature",
		base64.StdEncoding.EncodeToString(devicesig.SignRequest(a.Key, ts, body)))

	resp, err := a.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096)) //nolint:errcheck
	if resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("stats rejected: %d", resp.StatusCode)
	}
	return nil
}

// executeCommand runs a CommandRequest and sends back a CommandResult.
// It is called in its own goroutine so the read loop keeps serving.
func (a *Agent) executeCommand(ctx context.Context, ws *websocket.Conn, req *rmmpb.CommandRequest) {
	if req == nil || req.CommandId == "" {
		return
	}
	// Idempotency: skip if already completed.
	if a.Journal != nil && a.Journal.Contains(req.CommandId) {
		a.Log.Info("skipping already-executed command", "command_id", req.CommandId)
		return
	}
	a.Log.Info("executing command", "command_id", req.CommandId, "kind", req.Kind)

	var result agentexec.Result
	pbStatus := rmmpb.CommandStatus_COMMAND_STATUS_FAILED

	if req.Kind == rmmpb.CommandKind_COMMAND_KIND_SCRIPT {
		spec, err := agentexec.ParseSpec(req.Spec)
		if err != nil {
			a.Log.Error("bad command spec", "command_id", req.CommandId, "error", err)
			result = agentexec.Result{Output: []byte("bad spec: " + err.Error())}
		} else {
			result = agentexec.RunScript(ctx, spec, req.TimeoutS)
		}
	} else {
		result = agentexec.Result{Output: []byte("unsupported command kind")}
	}

	switch {
	case result.Err != nil && errors.Is(result.Err, context.DeadlineExceeded):
		pbStatus = rmmpb.CommandStatus_COMMAND_STATUS_TIMEOUT
	case result.Err != nil:
		pbStatus = rmmpb.CommandStatus_COMMAND_STATUS_FAILED
	case result.ExitCode == 0:
		pbStatus = rmmpb.CommandStatus_COMMAND_STATUS_SUCCEEDED
	default:
		pbStatus = rmmpb.CommandStatus_COMMAND_STATUS_FAILED
	}

	// Record in journal before sending the result so a crash after
	// recording but before sending results in a duplicate send (which the
	// server handles idempotently), not a duplicate execution.
	if a.Journal != nil {
		if err := a.Journal.Record(req.CommandId); err != nil {
			a.Log.Warn("journal write failed", "command_id", req.CommandId, "error", err)
		}
	}

	_ = write(ctx, ws, &rmmpb.Envelope{
		MessageId: uuid.NewString(),
		Payload: &rmmpb.Envelope_CommandResult{CommandResult: &rmmpb.CommandResult{
			CommandId:  req.CommandId,
			Status:     pbStatus,
			ExitCode:   int32(result.ExitCode),
			Output:     result.Output,
			Truncated:  result.Truncated,
			StartedAt:  timestamppb.New(result.StartedAt),
			FinishedAt: timestamppb.New(result.FinishedAt),
		}},
	})
}

func read(ctx context.Context, ws *websocket.Conn) (*rmmpb.Envelope, error) {
	_, b, err := ws.Read(ctx)
	if err != nil {
		return nil, err
	}
	env := &rmmpb.Envelope{}
	if err := proto.Unmarshal(b, env); err != nil {
		return nil, err
	}
	return env, nil
}

func write(ctx context.Context, ws *websocket.Conn, env *rmmpb.Envelope) error {
	b, err := proto.Marshal(env)
	if err != nil {
		return err
	}
	return ws.Write(ctx, websocket.MessageBinary, b)
}

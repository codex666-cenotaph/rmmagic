// Package gateway terminates persistent agent WebSocket connections:
// challenge-response device authentication, heartbeat liveness, and
// command delivery over the rmm.v1 protobuf envelope protocol.
package gateway

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
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
	// Agents cap command output at 1 MiB; the CommandResult envelope adds
	// protobuf framing on top, so the read limit must exceed 1 MiB or a
	// max-output result would overflow and drop the connection.
	maxFrameSize = 2 << 20
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
			// Drain any pending/unacknowledged jobs for this device.
			go g.drainPendingJobs(ctx, ac, tenantID, deviceID)
		case *rmmpb.Envelope_Heartbeat:
			if err := g.touch(ctx, tenantID, deviceID, "", &lastTouch, false); err != nil {
				g.Log.Error("heartbeat touch failed", "device_id", deviceID, "error", err)
			}
		case *rmmpb.Envelope_Ack:
			// Acks for non-command messages; no-op for now.
		case *rmmpb.Envelope_CommandResult:
			g.handleCommandResult(ctx, tenantID, p.CommandResult)
		case *rmmpb.Envelope_UpdateStatus:
			g.handleUpdateStatus(ctx, tenantID, deviceID, p.UpdateStatus)
		default:
			g.Log.Warn("unexpected frame from agent", "device_id", deviceID)
		}
	}
}

// drainPendingJobs sends any pending or unacknowledged jobs to a newly
// connected device. Called after HelloAck is sent.
func (g *Gateway) drainPendingJobs(ctx context.Context, ac *agentConn, tenantID, deviceID uuid.UUID) {
	var pending []store.PendingJob
	if err := g.Store.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		pending, err = store.ListPendingJobsForDevice(ctx, tx, deviceID)
		return err
	}); err != nil {
		g.Log.Error("list pending jobs failed", "device_id", deviceID, "error", err)
		return
	}
	for _, pj := range pending {
		kind, spec, err := commandForPendingJob(pj)
		if err != nil {
			g.Log.Error("build spec failed", "job_id", pj.JobID, "error", err)
			continue
		}
		if err := ac.Send(ctx, &rmmpb.Envelope{
			MessageId: uuid.NewString(),
			Payload: &rmmpb.Envelope_CommandRequest{CommandRequest: &rmmpb.CommandRequest{
				CommandId: pj.CommandID,
				Kind:      kind,
				Spec:      spec,
				ExpiresAt: timestamppb.New(pj.ExpiresAt),
				TimeoutS:  uint32(pj.TimeoutS),
			}},
		}); err != nil {
			return
		}
		if err := g.Store.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
			return store.MarkJobSent(ctx, tx, pj.JobID)
		}); err != nil {
			g.Log.Warn("mark job sent failed", "job_id", pj.JobID, "error", err)
		}
	}
}

// handleCommandResult persists the execution result from the agent.
func (g *Gateway) handleCommandResult(ctx context.Context, tenantID uuid.UUID, res *rmmpb.CommandResult) {
	if res == nil || res.CommandId == "" {
		return
	}
	statusStr := commandStatusString(res.Status)
	output := string(res.Output)

	var exitCode *int
	if res.Status == rmmpb.CommandStatus_COMMAND_STATUS_SUCCEEDED ||
		res.Status == rmmpb.CommandStatus_COMMAND_STATUS_FAILED {
		ec := int(res.ExitCode)
		exitCode = &ec
	}

	startedAt := time.Now()
	if res.StartedAt != nil {
		startedAt = res.StartedAt.AsTime()
	}
	finishedAt := time.Now()
	if res.FinishedAt != nil {
		finishedAt = res.FinishedAt.AsTime()
	}

	if err := g.Store.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		if err := store.CompleteJob(ctx, tx, res.CommandId, statusStr, output, exitCode, startedAt, finishedAt); err != nil {
			return err
		}
		// If this job came from a health-check schedule, map its result to
		// the device's health state. No-op for ordinary jobs.
		return store.RecordJobHealth(ctx, tx, res.CommandId, exitCode, output)
	}); err != nil {
		g.Log.Error("complete job failed", "command_id", res.CommandId, "error", err)
	}
}

// handleUpdateStatus persists an agent-reported update phase. On a
// successful apply the device's recorded agent_version advances too, so the
// fleet view reflects the new build before the next heartbeat.
func (g *Gateway) handleUpdateStatus(ctx context.Context, tenantID, deviceID uuid.UUID, st *rmmpb.UpdateStatus) {
	if st == nil {
		return
	}
	phase := updatePhaseString(st.Phase)
	if err := g.Store.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		if err := store.SetDeviceUpdatePhase(ctx, tx, deviceID, st.Version, phase, st.Error); err != nil {
			return err
		}
		if st.Phase == rmmpb.UpdatePhase_UPDATE_PHASE_APPLIED {
			return store.TouchDevice(ctx, tx, deviceID, st.Version)
		}
		return nil
	}); err != nil {
		g.Log.Error("update status persist failed", "device_id", deviceID, "error", err)
	}
	g.Log.Info("update status", "device_id", deviceID, "version", st.Version, "phase", phase)
}

func updatePhaseString(p rmmpb.UpdatePhase) string {
	switch p {
	case rmmpb.UpdatePhase_UPDATE_PHASE_DOWNLOADING:
		return "downloading"
	case rmmpb.UpdatePhase_UPDATE_PHASE_VERIFIED:
		return "verified"
	case rmmpb.UpdatePhase_UPDATE_PHASE_APPLIED:
		return "applied"
	case rmmpb.UpdatePhase_UPDATE_PHASE_ROLLED_BACK:
		return "rolled_back"
	case rmmpb.UpdatePhase_UPDATE_PHASE_FAILED:
		return "failed"
	default:
		return "offered"
	}
}

func commandStatusString(s rmmpb.CommandStatus) string {
	switch s {
	case rmmpb.CommandStatus_COMMAND_STATUS_SUCCEEDED:
		return "succeeded"
	case rmmpb.CommandStatus_COMMAND_STATUS_FAILED:
		return "failed"
	case rmmpb.CommandStatus_COMMAND_STATUS_TIMEOUT:
		return "timed_out"
	case rmmpb.CommandStatus_COMMAND_STATUS_EXPIRED:
		return "expired"
	default:
		return "failed"
	}
}

// scriptSpec is the JSON payload for COMMAND_KIND_SCRIPT.
type scriptSpec struct {
	Language   string            `json:"language"`
	Body       string            `json:"body"`
	Parameters map[string]string `json:"parameters"`
}

func buildScriptSpec(language, body string, params json.RawMessage) ([]byte, error) {
	spec := scriptSpec{Language: language, Body: body, Parameters: map[string]string{}}
	if len(params) > 0 && string(params) != "null" {
		if err := json.Unmarshal(params, &spec.Parameters); err != nil {
			return nil, err
		}
	}
	return json.Marshal(spec)
}

// commandKindFor maps a job kind string to the protocol CommandKind.
func commandKindFor(kind string) rmmpb.CommandKind {
	switch kind {
	case "package_install":
		return rmmpb.CommandKind_COMMAND_KIND_PACKAGE_INSTALL
	case "package_remove":
		return rmmpb.CommandKind_COMMAND_KIND_PACKAGE_REMOVE
	default:
		return rmmpb.CommandKind_COMMAND_KIND_SCRIPT
	}
}

// commandForPendingJob builds the protocol kind and spec bytes for a
// (re-)dispatched job: script bodies are assembled from the snapshot,
// package jobs forward their stored spec verbatim.
func commandForPendingJob(pj store.PendingJob) (rmmpb.CommandKind, []byte, error) {
	if pj.Kind == "package_install" || pj.Kind == "package_remove" {
		return commandKindFor(pj.Kind), []byte(pj.Spec), nil
	}
	spec, err := buildScriptSpec(pj.Language, pj.ScriptBody, pj.Parameters)
	return rmmpb.CommandKind_COMMAND_KIND_SCRIPT, spec, err
}

// DispatchJob sends a job (script or package) to a connected device.
// Returns true if the device was online and the frame was sent.
func (g *Gateway) DispatchJob(ctx context.Context, tenantID, deviceID, jobID uuid.UUID, commandID string) bool {
	kind, spec, timeoutS, expiresAt, err := g.jobSpecForCommand(ctx, tenantID, commandID)
	if err != nil {
		g.Log.Error("dispatch job spec failed", "job_id", jobID, "error", err)
		return false
	}
	return g.Registry.Send(ctx, deviceID, &rmmpb.Envelope{
		MessageId: uuid.NewString(),
		Payload: &rmmpb.Envelope_CommandRequest{CommandRequest: &rmmpb.CommandRequest{
			CommandId: commandID,
			Kind:      kind,
			Spec:      spec,
			ExpiresAt: timestamppb.New(expiresAt),
			TimeoutS:  uint32(timeoutS),
		}},
	})
}

func (g *Gateway) jobSpecForCommand(ctx context.Context, tenantID uuid.UUID, commandID string) (rmmpb.CommandKind, []byte, int, time.Time, error) {
	var kind rmmpb.CommandKind
	var spec []byte
	var timeoutS int
	var expiresAt time.Time
	err := g.Store.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		var jobKind, lang, body string
		var params, pkgSpec json.RawMessage
		err := tx.QueryRow(ctx, `
			SELECT kind, COALESCE(language,''), COALESCE(script_body,''),
			       parameters, COALESCE(spec,'null'::jsonb), timeout_s, expires_at
			FROM jobs WHERE command_id=$1`, commandID).
			Scan(&jobKind, &lang, &body, &params, &pkgSpec, &timeoutS, &expiresAt)
		if err != nil {
			return err
		}
		kind = commandKindFor(jobKind)
		if jobKind == "package_install" || jobKind == "package_remove" {
			spec = []byte(pkgSpec)
			return nil
		}
		spec, err = buildScriptSpec(lang, body, params)
		return err
	})
	return kind, spec, timeoutS, expiresAt, err
}

// OfferUpdate sends an UpdateOffer for a release to a connected device.
// Returns true if the device was online and the frame was sent.
//
// For server-hosted releases (StorageKey set) the offer carries the
// device-authenticated download *path*; the agent resolves it against its
// own server URL and signs the request. Releases registered with an
// external URL still send that absolute URL (downloaded unauthenticated).
func (g *Gateway) OfferUpdate(ctx context.Context, deviceID uuid.UUID, rel store.AgentRelease) bool {
	sig, err := base64.StdEncoding.DecodeString(rel.Signature)
	if err != nil {
		g.Log.Error("release has bad signature encoding", "release_id", rel.ID, "error", err)
		return false
	}
	url := rel.URL
	if rel.StorageKey != "" {
		url = "/agent/v1/releases/" + rel.ID.String() + "/download"
	}
	return g.Registry.Send(ctx, deviceID, &rmmpb.Envelope{
		MessageId: uuid.NewString(),
		Payload: &rmmpb.Envelope_UpdateOffer{UpdateOffer: &rmmpb.UpdateOffer{
			Version:   rel.Version,
			Url:       url,
			Sha256:    rel.SHA256,
			Signature: sig,
		}},
	})
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

// RequestInventoryRefresh asks a connected agent to re-collect and
// upload its inventory immediately. Fire-and-forget: the agent's
// result for the ad-hoc command ID matches no job and is ignored.
// Returns false when the device is not connected to this gateway.
func (g *Gateway) RequestInventoryRefresh(ctx context.Context, deviceID uuid.UUID) bool {
	return g.Registry.Send(ctx, deviceID, &rmmpb.Envelope{
		MessageId: uuid.NewString(),
		Payload: &rmmpb.Envelope_CommandRequest{CommandRequest: &rmmpb.CommandRequest{
			CommandId: "invrefresh-" + uuid.NewString(),
			Kind:      rmmpb.CommandKind_COMMAND_KIND_INVENTORY_REFRESH,
			ExpiresAt: timestamppb.New(time.Now().Add(5 * time.Minute)),
			TimeoutS:  120,
		}},
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

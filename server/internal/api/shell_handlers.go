package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/codex666-cenotaph/rmmagic/server/internal/auth"
	"github.com/codex666-cenotaph/rmmagic/server/internal/recordings"
	"github.com/codex666-cenotaph/rmmagic/server/internal/store"
)

// Terminal dimension bounds. xterm can request large grids; clamp to keep
// PTY ioctls and recordings sane.
const (
	maxCols = 500
	maxRows = 300
	// browserWriteTimeout bounds a single write to the browser so a wedged
	// client tears the session down instead of leaking the goroutine.
	browserWriteTimeout = 15 * time.Second
	// shellIdleTimeout closes a session with no traffic in either
	// direction, so an unattended root shell does not stay open.
	shellIdleTimeout = 30 * time.Minute
)

type shellSessionJSON struct {
	ID           uuid.UUID  `json:"id"`
	DeviceID     uuid.UUID  `json:"device_id"`
	Hostname     string     `json:"hostname"`
	Status       string     `json:"status"`
	Cols         int        `json:"cols"`
	Rows         int        `json:"rows"`
	BytesIn      int64      `json:"bytes_in"`
	BytesOut     int64      `json:"bytes_out"`
	HasRecording bool       `json:"has_recording"`
	Error        *string    `json:"error,omitempty"`
	StartedAt    time.Time  `json:"started_at"`
	EndedAt      *time.Time `json:"ended_at"`
}

func toShellSessionJSON(s store.ShellSession) shellSessionJSON {
	return shellSessionJSON{
		ID: s.ID, DeviceID: s.DeviceID, Hostname: s.Hostname, Status: s.Status,
		Cols: s.Cols, Rows: s.Rows, BytesIn: s.BytesIn, BytesOut: s.BytesOut,
		HasRecording: s.RecordingRef != nil && *s.RecordingRef != "",
		Error:        s.Error, StartedAt: s.StartedAt, EndedAt: s.EndedAt,
	}
}

func clampDim(raw string, def, max int) int {
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return def
	}
	if n > max {
		return max
	}
	return n
}

// handleShellConnect upgrades to a WebSocket and bridges the browser
// terminal to the device PTY over the agent gateway, recording the
// session. All validation happens before the upgrade because the
// ResponseWriter is hijacked afterwards.
func (s *Server) handleShellConnect(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	if s.Gateway == nil {
		writeError(w, http.StatusServiceUnavailable, "shell gateway not available on this node")
		return
	}

	var dev store.Device
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		var e error
		dev, e = getAuthorizedDevice(r, tx, auth.PermShellConnect, id)
		return e
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if dev.Status != "active" {
		writeError(w, http.StatusConflict, "device is not active")
		return
	}
	if !s.Gateway.Online(id) {
		writeError(w, http.StatusConflict, "device is offline")
		return
	}

	cols := clampDim(r.URL.Query().Get("cols"), 80, maxCols)
	rows := clampDim(r.URL.Query().Get("rows"), 24, maxRows)
	ip, _ := ctx.Value(ctxIP).(string)

	// Open the session row and audit the start before bridging, so an
	// access is recorded even if the connection drops immediately.
	var sessionID uuid.UUID
	err = s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		var openedBy *uuid.UUID
		if p.UserID != uuid.Nil {
			uid := p.UserID
			openedBy = &uid
		}
		var e error
		sessionID, e = store.CreateShellSession(ctx, tx, p.TenantID, id, openedBy, ip, cols, rows)
		if e != nil {
			return e
		}
		return recordAudit(ctx, tx, "shell.start", "shell_session", sessionID, map[string]any{
			"device_id": id.String(), "hostname": dev.Hostname, "cols": cols, "rows": rows,
		})
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}

	// Context for the finalize/audit work that outlives the request (the
	// request context is cancelled when the socket closes).
	finCtx := auth.WithPrincipal(context.WithValue(context.Background(), ctxIP, ip), p)

	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: s.WSOrigins})
	if err != nil {
		s.Log.Info("shell websocket upgrade failed", "session_id", sessionID, "error", err)
		s.finalizeShell(finCtx, p.TenantID, sessionID, nil, nil, 0, 0, "error", "websocket upgrade failed")
		return
	}

	s.bridgeShell(r.Context(), ws, finCtx, p.TenantID, id, sessionID, cols, rows)
}

// bridgeShell runs the live session: it tells the agent to open a PTY,
// pumps browser<->agent traffic, tees output to a recording, and
// finalizes the session row on exit.
func (s *Server) bridgeShell(ctx context.Context, ws *websocket.Conn, finCtx context.Context,
	tenantID, deviceID, sessionID uuid.UUID, cols, rows int) {
	sid := sessionID.String()
	sink := s.Gateway.RegisterShell(sid)
	defer s.Gateway.UnregisterShell(sid)

	// Optional recording to a temp file, uploaded on close.
	recorder, recFile := s.newRecorder(sessionID, cols, rows)

	bctx, cancel := context.WithCancel(ctx)
	defer cancel()

	status := "closed"
	errMsg := ""
	var bytesIn, bytesOut atomic.Int64

	if !s.Gateway.StartShell(bctx, deviceID, sid, uint32(cols), uint32(rows)) {
		_ = ws.Close(websocket.StatusTryAgainLater, "device offline")
		s.finalizeShell(finCtx, tenantID, sessionID, recorder, recFile,
			0, 0, "error", "device offline")
		return
	}

	// Idle watchdog: an unattended root shell is a standing risk, so a
	// session with no traffic in either direction is torn down. It only
	// cancels the context (never writes to the socket — the pump goroutine
	// is the sole writer), so there is no concurrent-write race.
	var lastActive atomic.Int64
	var idleHit atomic.Bool
	lastActive.Store(time.Now().UnixNano())
	touch := func() { lastActive.Store(time.Now().UnixNano()) }
	go func() {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			select {
			case <-bctx.Done():
				return
			case <-t.C:
				if time.Since(time.Unix(0, lastActive.Load())) > shellIdleTimeout {
					idleHit.Store(true)
					cancel()
					return
				}
			}
		}
	}()

	// agent -> browser pump.
	pumpDone := make(chan struct{})
	go func() {
		defer close(pumpDone)
		defer cancel() // unblock the browser reader when the agent ends
		for {
			select {
			case <-bctx.Done():
				return
			case data := <-sink.Output:
				touch()
				if !writeTerminal(bctx, ws, data, recorder, &bytesOut) {
					return
				}
			case <-sink.Done():
				// Agent PTY exited: flush any buffered output, then signal.
				for {
					select {
					case data := <-sink.Output:
						if !writeTerminal(bctx, ws, data, recorder, &bytesOut) {
							return
						}
					default:
						writeControl(bctx, ws, map[string]any{"type": "exit"})
						return
					}
				}
			}
		}
	}()

	// browser -> agent pump (this goroutine).
readLoop:
	for {
		typ, data, err := ws.Read(bctx)
		if err != nil {
			break
		}
		touch()
		switch typ {
		case websocket.MessageText:
			var ctl struct {
				Type string `json:"type"`
				Cols int    `json:"cols"`
				Rows int    `json:"rows"`
			}
			if json.Unmarshal(data, &ctl) == nil && ctl.Type == "resize" {
				c := clampDim(strconv.Itoa(ctl.Cols), cols, maxCols)
				rw := clampDim(strconv.Itoa(ctl.Rows), rows, maxRows)
				s.Gateway.SendShellResize(bctx, deviceID, sid, uint32(c), uint32(rw))
				if recorder != nil {
					recorder.Resize(c, rw)
				}
			}
		case websocket.MessageBinary:
			bytesIn.Add(int64(len(data)))
			if !s.Gateway.SendShellInput(bctx, deviceID, sid, data) {
				errMsg = "device disconnected"
				break readLoop
			}
		}
	}

	cancel()
	<-pumpDone
	if idleHit.Load() {
		errMsg = "idle timeout"
	}
	if errMsg != "" {
		status = "error"
	}
	// Best-effort: stop the agent PTY (no-op if it already exited).
	s.Gateway.StopShell(context.Background(), deviceID, sid)
	_ = ws.Close(websocket.StatusNormalClosure, "")

	s.finalizeShell(finCtx, tenantID, sessionID, recorder, recFile,
		bytesIn.Load(), bytesOut.Load(), status, errMsg)
}

// writeTerminal sends one output chunk to the browser and tees it to the
// recording. Returns false on write failure (caller should stop).
func writeTerminal(ctx context.Context, ws *websocket.Conn, data []byte,
	recorder *recordings.Recorder, bytesOut *atomic.Int64) bool {
	wctx, cancel := context.WithTimeout(ctx, browserWriteTimeout)
	err := ws.Write(wctx, websocket.MessageBinary, data)
	cancel()
	if err != nil {
		return false
	}
	bytesOut.Add(int64(len(data)))
	if recorder != nil {
		recorder.Output(data)
	}
	return true
}

func writeControl(ctx context.Context, ws *websocket.Conn, msg map[string]any) {
	b, err := json.Marshal(msg)
	if err != nil {
		return
	}
	wctx, cancel := context.WithTimeout(ctx, browserWriteTimeout)
	defer cancel()
	_ = ws.Write(wctx, websocket.MessageText, b)
}

// newRecorder starts an asciinema recorder backed by a temp file, or
// returns nils when recording is disabled or setup fails (the session
// still runs, just without playback).
func (s *Server) newRecorder(sessionID uuid.UUID, cols, rows int) (*recordings.Recorder, *os.File) {
	if s.Recordings == nil {
		return nil, nil
	}
	f, err := os.CreateTemp("", "rmm-rec-*.cast")
	if err != nil {
		s.Log.Warn("shell recording temp file failed", "session_id", sessionID, "error", err)
		return nil, nil
	}
	rec, err := recordings.NewRecorder(f, cols, rows)
	if err != nil {
		f.Close()
		_ = os.Remove(f.Name())
		s.Log.Warn("shell recorder init failed", "session_id", sessionID, "error", err)
		return nil, nil
	}
	return rec, f
}

// finalizeShell uploads the recording (if any), updates the session row,
// and records the shell.end audit entry. Runs on a background context so
// it survives the closed socket.
func (s *Server) finalizeShell(ctx context.Context, tenantID, sessionID uuid.UUID,
	recorder *recordings.Recorder, recFile *os.File,
	bytesIn, bytesOut int64, status, errMsg string) {

	ref := ""
	if recFile != nil {
		ref = s.uploadRecording(ctx, tenantID, sessionID, recFile)
		recFile.Close()
		_ = os.Remove(recFile.Name())
	}
	if recorder != nil && recorder.Err() != nil {
		s.Log.Warn("shell recording had write errors", "session_id", sessionID, "error", recorder.Err())
	}

	err := s.Store.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		if e := store.FinishShellSession(ctx, tx, sessionID, status, ref, bytesIn, bytesOut, errMsg); e != nil {
			return e
		}
		return recordAudit(ctx, tx, "shell.end", "shell_session", sessionID, map[string]any{
			"bytes_in": bytesIn, "bytes_out": bytesOut, "status": status,
			"recorded": ref != "",
		})
	})
	if err != nil {
		s.Log.Error("finalize shell session failed", "session_id", sessionID, "error", err)
	}
}

// uploadRecording flushes the temp cast file and stores it, returning the
// storage key (empty on failure).
func (s *Server) uploadRecording(ctx context.Context, tenantID, sessionID uuid.UUID, f *os.File) string {
	if s.Recordings == nil {
		return ""
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		s.Log.Error("recording seek failed", "session_id", sessionID, "error", err)
		return ""
	}
	fi, err := f.Stat()
	if err != nil {
		s.Log.Error("recording stat failed", "session_id", sessionID, "error", err)
		return ""
	}
	key := recordingKey(tenantID, sessionID)
	if err := s.Recordings.Put(ctx, key, f, fi.Size()); err != nil {
		s.Log.Error("recording upload failed", "session_id", sessionID, "error", err)
		return ""
	}
	return key
}

func recordingKey(tenantID, sessionID uuid.UUID) string {
	return "recordings/" + tenantID.String() + "/" + sessionID.String() + ".cast"
}

// --- session history + playback ---

func (s *Server) handleListShellSessions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	limit := clampDim(r.URL.Query().Get("limit"), 100, 500)
	out := []shellSessionJSON{}
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		if _, e := getAuthorizedDevice(r, tx, auth.PermShellConnect, id); e != nil {
			return e
		}
		sessions, e := store.ListShellSessions(ctx, tx, id, limit)
		if e != nil {
			return e
		}
		for _, ss := range sessions {
			out = append(out, toShellSessionJSON(ss))
		}
		return nil
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": out})
}

// authorizedSession loads a session and enforces shell.connect at the
// owning device's site scope.
func authorizedSession(r *http.Request, tx pgx.Tx, id uuid.UUID) (store.ShellSession, error) {
	sess, err := store.GetShellSession(r.Context(), tx, id)
	if err != nil {
		return sess, err
	}
	if _, err := getAuthorizedDevice(r, tx, auth.PermShellConnect, sess.DeviceID); err != nil {
		return sess, err
	}
	return sess, nil
}

func (s *Server) handleGetShellSession(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	var sess store.ShellSession
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		var e error
		sess, e = authorizedSession(r, tx, id)
		return e
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toShellSessionJSON(sess))
}

func (s *Server) handleShellRecording(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := auth.PrincipalFrom(ctx)
	id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	var sess store.ShellSession
	err := s.Store.WithTenant(ctx, p.TenantID, func(tx pgx.Tx) error {
		var e error
		sess, e = authorizedSession(r, tx, id)
		return e
	})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if s.Recordings == nil || sess.RecordingRef == nil || *sess.RecordingRef == "" {
		writeError(w, http.StatusNotFound, "no recording for this session")
		return
	}
	rc, err := s.Recordings.Get(ctx, *sess.RecordingRef)
	if err != nil {
		s.Log.Warn("recording fetch failed", "session_id", id, "error", err)
		writeError(w, http.StatusNotFound, "recording unavailable")
		return
	}
	defer rc.Close()
	w.Header().Set("Content-Type", "application/x-asciicast")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, rc)
}

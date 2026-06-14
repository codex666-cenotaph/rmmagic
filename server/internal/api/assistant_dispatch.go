package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"

	"github.com/codex666-cenotaph/rmmagic/server/internal/auth"
)

// The in-dashboard assistant executes its tools by replaying them against
// this server's own REST handlers — in process, with the chatting user's
// already-authenticated Principal in context. This reuses every handler's
// fine-grained authorization unchanged: the assistant can read or do
// exactly what the logged-in user could do through the dashboard, and
// nothing more. It adds no privileges of its own.

// internalMux lazily builds a ServeMux of the raw route handlers WITHOUT
// the requireAuth middleware. It is never exposed on the network — it is
// only reachable through executeInternal, which injects an authenticated
// Principal itself. Pattern matching (and thus {id} path values) works
// exactly as for the public mux.
func (s *Server) internalMux() http.Handler {
	s.internalOnce.Do(func() {
		mux := http.NewServeMux()
		for _, rt := range s.Routes() {
			// Skip public/agent and auth endpoints: the assistant only
			// drives the authenticated dashboard API surface.
			if rt.Public || rt.Perm == PermSelf {
				continue
			}
			mux.Handle(rt.Method+" "+rt.Pattern, rt.Handler)
		}
		s.internalHandler = mux
	})
	return s.internalHandler
}

// executeInternal runs one API call (method + path + optional JSON body)
// as the given principal and returns the response status and body. It is
// the bridge the assistant's tools call.
func (s *Server) executeInternal(ctx context.Context, p *auth.Principal, method, path string, body []byte) (int, json.RawMessage) {
	var reader *bytes.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	// Carry the authenticated principal and a marker IP so handlers and
	// audit logging behave as they do for a real request.
	rctx := auth.WithPrincipal(ctx, p)
	rctx = context.WithValue(rctx, ctxIP, "assistant")
	req = req.WithContext(rctx)

	rec := httptest.NewRecorder()
	s.internalMux().ServeHTTP(rec, req)
	return rec.Code, json.RawMessage(rec.Body.Bytes())
}

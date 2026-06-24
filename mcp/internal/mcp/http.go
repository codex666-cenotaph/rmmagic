package mcp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
)

// EnrichFunc derives a per-request context from the incoming HTTP
// request — typically to extract an Authorization bearer token and stash
// it for tool handlers. It runs before any JSON-RPC message is handled.
type EnrichFunc func(ctx context.Context, r *http.Request) context.Context

// HTTPHandler returns an http.Handler implementing the MCP Streamable
// HTTP transport: clients POST JSON-RPC messages and read JSON-RPC
// responses. This lets web-based agents and remote MCP clients connect
// to the server over the network instead of spawning it over stdio.
//
// The handler is stateless — tools carry no session state — but it still
// issues an Mcp-Session-Id on initialize for clients that expect one.
// enrich may be nil.
func (s *Server) HTTPHandler(enrich EnrichFunc) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			s.serveHTTPPost(w, r, enrich)
		case http.MethodGet:
			// Server-initiated SSE streams are not offered; the spec allows
			// declining the GET stream with 405.
			w.Header().Set("Allow", "POST, DELETE")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		case http.MethodDelete:
			// Stateless: nothing to tear down for a session.
			w.WriteHeader(http.StatusNoContent)
		default:
			w.Header().Set("Allow", "POST, GET, DELETE")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	return mux
}

func (s *Server) serveHTTPPost(w http.ResponseWriter, r *http.Request, enrich EnrichFunc) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 16*1024*1024))
	if err != nil {
		writeHTTPError(w, http.StatusBadRequest, codeParseError, "could not read body")
		return
	}

	ctx := r.Context()
	if enrich != nil {
		ctx = enrich(ctx, r)
	}

	// A JSON-RPC payload is either a single message or a batch array.
	trimmed := firstNonSpace(body)
	if trimmed == '[' {
		var batch []rpcRequest
		if err := json.Unmarshal(body, &batch); err != nil {
			writeHTTPError(w, http.StatusBadRequest, codeParseError, "parse error")
			return
		}
		var responses []rpcResponse
		var sawInit bool
		for _, req := range batch {
			if req.Method == "initialize" {
				sawInit = true
			}
			if resp, ok := s.handleHTTP(ctx, req); ok {
				responses = append(responses, resp)
			}
		}
		if sawInit {
			w.Header().Set("Mcp-Session-Id", newSessionID())
		}
		if len(responses) == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		writeJSON(w, responses)
		return
	}

	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeHTTPError(w, http.StatusBadRequest, codeParseError, "parse error")
		return
	}
	if req.Method == "initialize" {
		w.Header().Set("Mcp-Session-Id", newSessionID())
	}
	resp, ok := s.handleHTTP(ctx, req)
	if !ok {
		// Notification: acknowledge with no body.
		w.WriteHeader(http.StatusAccepted)
		return
	}
	writeJSON(w, resp)
}

// handleHTTP processes one request and reports whether a response should
// be written (notifications produce none).
func (s *Server) handleHTTP(ctx context.Context, req rpcRequest) (rpcResponse, bool) {
	if len(req.ID) == 0 {
		s.handle(ctx, req) // run side effects; notifications get no reply
		return rpcResponse{}, false
	}
	result, rerr := s.handle(ctx, req)
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	if rerr != nil {
		resp.Error = rerr
	} else {
		resp.Result = result
	}
	return resp, true
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func writeHTTPError(w http.ResponseWriter, status, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(rpcResponse{
		JSONRPC: "2.0",
		Error:   &rpcError{Code: code, Message: msg},
	})
}

func firstNonSpace(b []byte) byte {
	for _, c := range b {
		switch c {
		case ' ', '\t', '\r', '\n':
			continue
		default:
			return c
		}
	}
	return 0
}

func newSessionID() string {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "session"
	}
	return hex.EncodeToString(raw)
}

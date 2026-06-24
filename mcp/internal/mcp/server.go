// Package mcp implements a minimal Model Context Protocol server over the
// stdio transport using only the standard library. It speaks JSON-RPC 2.0
// with newline-delimited messages, which is what MCP stdio clients
// (Claude Desktop, the Claude Agent SDK, and other agents) expect.
//
// The package is transport- and domain-agnostic: callers register tools
// with RegisterTool and then call Serve. The rmmagic-specific tools live
// in the tools package.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

// ProtocolVersion is the MCP revision this server implements. The server
// echoes the client's requested version when it is one we understand and
// otherwise falls back to this value.
const ProtocolVersion = "2025-06-18"

var supportedVersions = map[string]bool{
	"2024-11-05": true,
	"2025-03-26": true,
	"2025-06-18": true,
}

// ToolHandler executes a tool call. args holds the decoded "arguments"
// object from the request (never nil). The returned string is sent back
// to the client as text content. A non-nil error is reported to the
// client as a tool execution error (isError: true) rather than a
// protocol-level failure, so the agent can read and react to it.
type ToolHandler func(ctx context.Context, args map[string]any) (string, error)

// Tool is a registered tool: its advertised metadata plus the handler.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	handler     ToolHandler    `json:"-"`
}

// Server is an MCP server bound to a single stdio session.
type Server struct {
	name    string
	version string

	mu    sync.RWMutex
	tools map[string]Tool
	order []string
}

// NewServer returns a server identifying itself with the given name and
// version in the initialize handshake.
func NewServer(name, version string) *Server {
	return &Server{name: name, version: version, tools: map[string]Tool{}}
}

// RegisterTool adds a tool to the server. Registering a duplicate name
// overwrites the previous definition.
func (s *Server) RegisterTool(name, description string, schema map[string]any, h ToolHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.tools[name]; !exists {
		s.order = append(s.order, name)
	}
	s.tools[name] = Tool{Name: name, Description: description, InputSchema: schema, handler: h}
}

// --- JSON-RPC wire types ---

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32603
)

// Serve runs the read/dispatch/write loop until the input stream closes
// (EOF), which is how an MCP client signals shutdown of a stdio server.
// Writes are serialized so handlers could safely emit messages
// concurrently in the future; today dispatch is sequential.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	// Allow large messages (tool outputs, inventory blobs) up to 16 MiB.
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	enc := json.NewEncoder(out)
	var writeMu sync.Mutex
	write := func(resp rpcResponse) {
		writeMu.Lock()
		defer writeMu.Unlock()
		resp.JSONRPC = "2.0"
		_ = enc.Encode(resp)
	}

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			write(rpcResponse{Error: &rpcError{Code: codeParseError, Message: "parse error"}})
			continue
		}
		// Notifications (no id) get no response.
		isNotification := len(req.ID) == 0
		result, rerr := s.handle(ctx, req)
		if isNotification {
			continue
		}
		if rerr != nil {
			write(rpcResponse{ID: req.ID, Error: rerr})
			continue
		}
		write(rpcResponse{ID: req.ID, Result: result})
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func (s *Server) handle(ctx context.Context, req rpcRequest) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		return s.handleInitialize(req)
	case "notifications/initialized", "notifications/cancelled":
		return nil, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return s.handleToolsList(), nil
	case "tools/call":
		return s.handleToolsCall(ctx, req)
	default:
		return nil, &rpcError{Code: codeMethodNotFound, Message: "method not found: " + req.Method}
	}
}

func (s *Server) handleInitialize(req rpcRequest) (any, *rpcError) {
	var params struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if len(req.Params) > 0 {
		_ = json.Unmarshal(req.Params, &params)
	}
	version := ProtocolVersion
	if supportedVersions[params.ProtocolVersion] {
		version = params.ProtocolVersion
	}
	return map[string]any{
		"protocolVersion": version,
		"capabilities": map[string]any{
			"tools": map[string]any{"listChanged": false},
		},
		"serverInfo": map[string]any{
			"name":    s.name,
			"version": s.version,
		},
	}, nil
}

func (s *Server) handleToolsList() any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	list := make([]Tool, 0, len(s.order))
	for _, name := range s.order {
		list = append(list, s.tools[name])
	}
	return map[string]any{"tools": list}
}

func (s *Server) handleToolsCall(ctx context.Context, req rpcRequest) (any, *rpcError) {
	var params struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: "invalid params"}
	}
	s.mu.RLock()
	tool, ok := s.tools[params.Name]
	s.mu.RUnlock()
	if !ok {
		return nil, &rpcError{Code: codeMethodNotFound, Message: "unknown tool: " + params.Name}
	}
	if params.Arguments == nil {
		params.Arguments = map[string]any{}
	}

	text, err := tool.handler(ctx, params.Arguments)
	if err != nil {
		// Tool execution errors are reported in-band so the model can see
		// and recover from them, per the MCP tool-call contract.
		return toolResult(fmt.Sprintf("Error: %s", err.Error()), true), nil
	}
	return toolResult(text, false), nil
}

func toolResult(text string, isError bool) any {
	return map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": text},
		},
		"isError": isError,
	}
}

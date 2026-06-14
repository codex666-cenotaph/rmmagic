// rmmmcp is a Model Context Protocol (MCP) server that exposes a
// rmmagic instance to AI agents. It connects over the existing REST API
// using a tenant API token and speaks MCP over stdio, so it can be wired
// into Claude Desktop, the Claude Agent SDK, or any MCP-capable client.
//
// Configuration (environment variables):
//
//	RMM_MCP_SERVER_URL   base URL of the rmmagic server
//	                     (e.g. https://rmm.example.com). Required.
//	RMM_MCP_TOKEN        rmmagic API token ("rmm_..."). Required.
//
// The token's permissions and scope govern what the agent can do; this
// server grants no privileges of its own.
//
// Example client configuration (mcpServers entry):
//
//	{
//	  "command": "rmmmcp",
//	  "env": {
//	    "RMM_MCP_SERVER_URL": "https://rmm.example.com",
//	    "RMM_MCP_TOKEN": "rmm_xxxxxxxx"
//	  }
//	}
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/codex666-cenotaph/rmmagic/mcp/internal/mcp"
	"github.com/codex666-cenotaph/rmmagic/mcp/internal/rmm"
	"github.com/codex666-cenotaph/rmmagic/mcp/internal/tools"
)

// version is the rmmmcp build version, injected via -ldflags at release
// time and reported to MCP clients in the initialize handshake.
var version = "0.0.0-dev"

func main() {
	// Logs go to stderr; stdout is reserved for the JSON-RPC stream.
	log.SetOutput(os.Stderr)
	log.SetFlags(0)
	log.SetPrefix("rmmmcp: ")

	baseURL := strings.TrimSpace(os.Getenv("RMM_MCP_SERVER_URL"))
	token := strings.TrimSpace(os.Getenv("RMM_MCP_TOKEN"))
	if baseURL == "" || token == "" {
		fmt.Fprintln(os.Stderr, "rmmmcp: RMM_MCP_SERVER_URL and RMM_MCP_TOKEN are required")
		os.Exit(2)
	}

	client := rmm.New(baseURL, token)
	srv := mcp.NewServer("rmmagic", version)
	tools.Register(srv, client)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Printf("serving MCP over stdio for %s", baseURL)
	if err := srv.Serve(ctx, os.Stdin, os.Stdout); err != nil {
		log.Fatalf("serve: %v", err)
	}
}

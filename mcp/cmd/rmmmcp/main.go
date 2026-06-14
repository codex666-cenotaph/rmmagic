// rmmmcp is a Model Context Protocol (MCP) server that exposes a
// rmmagic instance to AI agents. It wraps the existing REST API as MCP
// tools and speaks MCP over two transports:
//
//	stdio (default) — a local subprocess an MCP client spawns and talks
//	    to over stdin/stdout (Claude Desktop, Claude Code, the Agent SDK).
//	    Authenticates with one token from RMM_MCP_TOKEN.
//
//	HTTP (--http ADDR) — a remote/network MCP server (Streamable HTTP
//	    transport at POST /mcp) that web-based agents and connectors reach
//	    over the network. Authenticates per request with the caller's
//	    "rmm_..." token in the Authorization: Bearer header, so it holds
//	    no shared credential and is multi-tenant safe.
//
// Configuration (environment variables):
//
//	RMM_MCP_SERVER_URL   base URL of the rmmagic server. Required.
//	RMM_MCP_TOKEN        rmmagic API token ("rmm_..."). Required for stdio;
//	                     ignored for HTTP (each request carries its own).
//
// The token's permissions and scope govern what an agent can do; this
// server grants no privileges of its own.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/codex666-cenotaph/rmmagic/mcp/internal/mcp"
	"github.com/codex666-cenotaph/rmmagic/mcp/internal/rmm"
	"github.com/codex666-cenotaph/rmmagic/mcp/internal/tools"
)

// version is the rmmmcp build version, injected via -ldflags at release
// time and reported to MCP clients in the initialize handshake.
var version = "0.0.0-dev"

func main() {
	// Logs go to stderr; on stdio, stdout is reserved for the JSON-RPC stream.
	log.SetOutput(os.Stderr)
	log.SetFlags(0)
	log.SetPrefix("rmmmcp: ")

	httpAddr := flag.String("http", "", "serve the remote MCP HTTP transport on this address (e.g. :9090) instead of stdio")
	flag.Parse()

	baseURL := strings.TrimSpace(os.Getenv("RMM_MCP_SERVER_URL"))
	if baseURL == "" {
		log.Fatal("RMM_MCP_SERVER_URL is required")
	}

	srv := mcp.NewServer("rmmagic", version)
	tools.Register(srv, func(ctx context.Context) (*rmm.Client, error) {
		token := rmm.TokenFrom(ctx)
		if token == "" {
			return nil, errors.New("no API token: send Authorization: Bearer rmm_... (HTTP) or set RMM_MCP_TOKEN (stdio)")
		}
		return rmm.New(baseURL, token), nil
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if *httpAddr != "" {
		serveHTTP(ctx, srv, *httpAddr, baseURL)
		return
	}
	serveStdio(ctx, srv, baseURL)
}

func serveStdio(ctx context.Context, srv *mcp.Server, baseURL string) {
	token := strings.TrimSpace(os.Getenv("RMM_MCP_TOKEN"))
	if token == "" {
		log.Fatal("RMM_MCP_TOKEN is required for stdio transport")
	}
	// The single env token authenticates every tool call.
	ctx = rmm.WithToken(ctx, token)
	log.Printf("serving MCP over stdio for %s", baseURL)
	if err := srv.Serve(ctx, os.Stdin, os.Stdout); err != nil {
		log.Fatalf("serve: %v", err)
	}
}

func serveHTTP(ctx context.Context, srv *mcp.Server, addr, baseURL string) {
	// Each request authenticates itself: forward its bearer token to the API.
	handler := srv.HTTPHandler(func(ctx context.Context, r *http.Request) context.Context {
		if tok := bearerToken(r); tok != "" {
			return rmm.WithToken(ctx, tok)
		}
		return ctx
	})

	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
	}()

	log.Printf("serving MCP over HTTP on %s (POST %s/mcp) for %s", addr, addr, baseURL)
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("serve: %v", err)
	}
}

func bearerToken(r *http.Request) string {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if len(h) > len(prefix) && strings.EqualFold(h[:len(prefix)], prefix) {
		return strings.TrimSpace(h[len(prefix):])
	}
	return ""
}

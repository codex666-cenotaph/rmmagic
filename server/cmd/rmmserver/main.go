// rmmserver is the control-plane binary. All logical roles (api,
// gateway, worker) live in this one binary and are enabled with
// --roles; at small scale run all of them in one process, at large
// scale run dedicated pools per role behind the same image.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/codex666-cenotaph/rmmagic/shared/version"
)

type config struct {
	roles      map[string]bool
	listenAddr string
}

func parseConfig() (config, error) {
	cfg := config{roles: map[string]bool{}}
	roles := flag.String("roles", "api,gateway,worker", "comma-separated roles to run: api,gateway,worker")
	flag.StringVar(&cfg.listenAddr, "listen", envOr("RMM_LISTEN", ":8080"), "HTTP listen address")
	flag.Parse()

	for _, r := range strings.Split(*roles, ",") {
		r = strings.TrimSpace(r)
		switch r {
		case "api", "gateway", "worker":
			cfg.roles[r] = true
		case "":
		default:
			return cfg, fmt.Errorf("unknown role %q", r)
		}
	}
	if len(cfg.roles) == 0 {
		return cfg, errors.New("at least one role is required")
	}
	return cfg, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(log)

	cfg, err := parseConfig()
	if err != nil {
		log.Error("invalid configuration", "error", err)
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /version", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"version":%q,"commit":%q,"protocol":%d}`,
			version.Version, version.Commit, version.ProtocolVersion)
	})

	srv := &http.Server{
		Addr:              cfg.listenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("rmmserver starting",
			"version", version.Version,
			"roles", rolesList(cfg.roles),
			"listen", cfg.listenAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		log.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Error("shutdown error", "error", err)
		}
	case err := <-errCh:
		log.Error("server failed", "error", err)
		os.Exit(1)
	}
}

func rolesList(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for r := range m {
		out = append(out, r)
	}
	return out
}

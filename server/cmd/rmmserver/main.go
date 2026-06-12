// rmmserver is the control-plane binary. All logical roles (api,
// gateway, worker) live in this one binary and are enabled with
// --roles; at small scale run all of them in one process, at large
// scale run dedicated pools per role behind the same image.
//
// Subcommands:
//
//	rmmserver [--roles=...] [--listen=...]   serve (default)
//	rmmserver bootstrap --tenant NAME --slug SLUG --email EMAIL
//	    creates the first tenant + owner; password read from
//	    RMM_BOOTSTRAP_PASSWORD. Requires a privileged DB connection
//	    (RMM_APP_ROLE empty), since RLS blocks tenant creation otherwise.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/codex666-cenotaph/rmmagic/server/internal/api"
	"github.com/codex666-cenotaph/rmmagic/server/internal/bootstrap"
	"github.com/codex666-cenotaph/rmmagic/server/internal/gateway"
	"github.com/codex666-cenotaph/rmmagic/server/internal/secrets"
	"github.com/codex666-cenotaph/rmmagic/server/internal/store"
	"github.com/codex666-cenotaph/rmmagic/server/internal/worker"
	"github.com/codex666-cenotaph/rmmagic/shared/version"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(log)

	if len(os.Args) > 1 && os.Args[1] == "bootstrap" {
		runBootstrap(log, os.Args[2:])
		return
	}
	runServe(log)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func openStore(ctx context.Context, log *slog.Logger, appRole string) *store.Store {
	dsn := os.Getenv("RMM_DATABASE_URL")
	if dsn == "" {
		log.Error("RMM_DATABASE_URL is required")
		os.Exit(2)
	}
	st, err := store.Open(ctx, dsn, appRole)
	if err != nil {
		log.Error("database connection failed", "error", err)
		os.Exit(1)
	}
	return st
}

func runBootstrap(log *slog.Logger, args []string) {
	fs := flag.NewFlagSet("bootstrap", flag.ExitOnError)
	tenant := fs.String("tenant", "", "tenant (MSP) display name")
	slug := fs.String("slug", "", "tenant slug")
	email := fs.String("email", "", "owner email")
	_ = fs.Parse(args)

	password := os.Getenv("RMM_BOOTSTRAP_PASSWORD")
	if password == "" {
		log.Error("set RMM_BOOTSTRAP_PASSWORD (not a flag, to keep it out of shell history args)")
		os.Exit(2)
	}

	ctx := context.Background()
	// Privileged connection: no SET ROLE, so tenant creation passes RLS.
	st := openStore(ctx, log, "")
	defer st.Close()

	id, err := bootstrap.Run(ctx, st, bootstrap.Input{
		TenantName: *tenant, Slug: *slug, Email: *email, Password: password,
	})
	if err != nil {
		log.Error("bootstrap failed", "error", err)
		os.Exit(1)
	}
	log.Info("tenant bootstrapped", "tenant_id", id.String(), "owner", *email)
}

func runServe(log *slog.Logger) {
	var listenAddr string
	roles := flag.String("roles", "api,gateway,worker", "comma-separated roles to run: api,gateway,worker")
	webDir := flag.String("web-dir", envOr("RMM_WEB_DIR", ""), "directory of built web assets to serve (empty = no UI)")
	flag.StringVar(&listenAddr, "listen", envOr("RMM_LISTEN", ":8080"), "HTTP listen address")
	flag.Parse()

	enabled := map[string]bool{}
	for _, r := range strings.Split(*roles, ",") {
		switch r = strings.TrimSpace(r); r {
		case "api", "gateway", "worker":
			enabled[r] = true
		case "":
		default:
			log.Error("unknown role", "role", r)
			os.Exit(2)
		}
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

	if enabled["api"] || enabled["gateway"] || enabled["worker"] {
		st := openStore(ctx, log, envOr("RMM_APP_ROLE", "rmm_app"))
		defer st.Close()

		var gw *gateway.Gateway
		if enabled["gateway"] {
			gw = gateway.New(st, log)
			mux.HandleFunc("GET /agent/v1/connect", gw.HandleConnect)
		}
		if enabled["api"] {
			box, err := secrets.NewBox(os.Getenv("RMM_MASTER_KEY"))
			if err != nil {
				log.Error("RMM_MASTER_KEY invalid", "error", err)
				os.Exit(2)
			}
			cookieSecure := envOr("RMM_COOKIE_SECURE", "true") != "false"
			srv := api.NewServer(st, box, log, cookieSecure)
			srv.Gateway = gw
			mux.Handle("/api/v1/", srv.Handler())
			mux.Handle("/agent/v1/enroll", srv.Handler())
			mux.Handle("/agent/v1/stats", srv.Handler())
			mux.Handle("/agent/v1/inventory", srv.Handler())
		}
		if enabled["worker"] {
			// gw is nil when the gateway role runs elsewhere; schedule-fired
			// jobs then reach agents via reconnect drain instead.
			go worker.New(st, log, gw).Run(ctx)
		}
	}

	// Serve pre-built web assets when --web-dir is set. Any path that
	// does not match a registered route falls through to the SPA shell.
	if *webDir != "" {
		fsys := os.DirFS(*webDir)
		fileServer := http.FileServer(http.FS(fsys))
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			// Try the exact file first; fall back to index.html so the
			// React router handles client-side navigation.
			if _, err := fs.Stat(fsys, strings.TrimPrefix(r.URL.Path, "/")); err == nil {
				fileServer.ServeHTTP(w, r)
				return
			}
			http.ServeFileFS(w, r, fsys, "index.html")
		})
		log.Info("serving web UI", "dir", *webDir)
	}

	httpSrv := &http.Server{
		Addr:              listenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("rmmserver starting",
			"version", version.Version, "roles", *roles, "listen", listenAddr)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		log.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := httpSrv.Shutdown(shutdownCtx); err != nil {
			log.Error("shutdown error", "error", err)
		}
	case err := <-errCh:
		log.Error("server failed", "error", err)
		os.Exit(1)
	}
}

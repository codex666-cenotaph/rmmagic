// rmmagent is the endpoint agent. Subcommands:
//
//	enroll --server URL --token TOKEN [--state-dir DIR]
//	    one-time enrollment: generates the device keypair locally and
//	    registers with the platform
//	run [--state-dir DIR]
//	    connect and serve (systemd entrypoint)
//	version
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/codex666-cenotaph/rmmagic/agent/internal/conn"
	agentexec "github.com/codex666-cenotaph/rmmagic/agent/internal/exec"
	"github.com/codex666-cenotaph/rmmagic/agent/internal/identity"
	"github.com/codex666-cenotaph/rmmagic/shared/version"
)

const defaultStateDir = "/var/lib/rmmagent"

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(log)

	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	switch os.Args[1] {
	case "version":
		fmt.Printf("rmmagent %s (%s, protocol %d)\n",
			version.Version, version.Commit, version.ProtocolVersion)

	case "enroll":
		fs := flag.NewFlagSet("enroll", flag.ExitOnError)
		server := fs.String("server", "", "server base URL, e.g. https://rmm.example.com")
		token := fs.String("token", "", "enrollment token (rmme_...)")
		stateDir := fs.String("state-dir", defaultStateDir, "directory for the device identity")
		_ = fs.Parse(os.Args[2:])
		if *server == "" || *token == "" {
			fmt.Fprintln(os.Stderr, "enroll requires --server and --token")
			os.Exit(2)
		}
		if err := conn.Enroll(context.Background(), *server, *token, *stateDir); err != nil {
			log.Error("enrollment failed", "error", err)
			os.Exit(1)
		}
		log.Info("enrolled", "state_dir", *stateDir)

	case "run":
		fs := flag.NewFlagSet("run", flag.ExitOnError)
		stateDir := fs.String("state-dir", defaultStateDir, "directory with the device identity")
		_ = fs.Parse(os.Args[2:])

		id, err := identity.Load(*stateDir)
		if err != nil {
			log.Error("no identity — run `rmmagent enroll` first", "error", err)
			os.Exit(1)
		}
		if id.Revoked {
			log.Error("device has been decommissioned; re-enroll to continue")
			os.Exit(1)
		}
		journal, err := agentexec.NewJournal(*stateDir)
		if err != nil {
			log.Error("cannot load command journal", "error", err)
			os.Exit(1)
		}
		agent, err := conn.NewAgent(id, log, journal)
		if err != nil {
			log.Error("invalid identity", "error", err)
			os.Exit(1)
		}

		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		log.Info("rmmagent starting", "version", version.Version, "device_id", id.DeviceID)

		if err := agent.Run(ctx); err != nil {
			if errors.Is(err, conn.ErrDecommissioned) {
				id.Revoked = true
				_ = identity.Save(*stateDir, id)
				log.Warn("decommissioned; identity marked revoked")
				os.Exit(0)
			}
			log.Error("agent stopped", "error", err)
			os.Exit(1)
		}

	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: rmmagent <enroll|run|version> [flags]")
}

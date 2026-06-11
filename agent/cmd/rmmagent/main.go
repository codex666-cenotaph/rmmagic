// rmmagent is the endpoint agent. Subcommands:
//
//	enroll  --server URL --token TOKEN   one-time enrollment, writes identity
//	run                                  connect and serve (systemd entrypoint)
//	version                              print version info
package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/codex666-cenotaph/rmmagic/shared/version"
)

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
		token := fs.String("token", "", "enrollment token")
		_ = fs.Parse(os.Args[2:])
		if *server == "" || *token == "" {
			fmt.Fprintln(os.Stderr, "enroll requires --server and --token")
			os.Exit(2)
		}
		// Enrollment (keypair generation, CSR, identity storage) lands in M2.
		log.Error("enrollment not implemented yet")
		os.Exit(1)
	case "run":
		// Connection loop (WSS, heartbeat, command execution) lands in M2.
		log.Error("run not implemented yet")
		os.Exit(1)
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: rmmagent <enroll|run|version> [flags]")
}

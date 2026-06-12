//go:build windows

// Package winsvc integrates the agent with the Windows Service Control Manager.
package winsvc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const (
	ServiceName        = "rmmagent"
	serviceDisplayName = "rmmagic endpoint agent"
	serviceDescription = "Monitors and manages this endpoint via the rmmagic platform."
)

// Runner is implemented by conn.Agent (and any test double).
type Runner interface {
	Run(ctx context.Context) error
}

// IsService reports whether the current process was started by the Windows SCM.
func IsService() bool {
	ok, _ := svc.IsWindowsService()
	return ok
}

// Run hands control to the Windows SCM under the given service name.
// It blocks until the SCM sends a Stop or Shutdown command.
func Run(name string, r Runner, log *slog.Logger) error {
	return svc.Run(name, &handler{runner: r, log: log})
}

type handler struct {
	runner Runner
	log    *slog.Logger
}

func (h *handler) Execute(_ []string, req <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- h.runner.Run(ctx) }()

	status <- svc.Status{
		State:   svc.Running,
		Accepts: svc.AcceptStop | svc.AcceptShutdown,
	}

	for {
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, context.Canceled) {
				h.log.Error("agent stopped", "error", err)
			}
			return false, 0
		case c := <-req:
			switch c.Cmd {
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				cancel()
				<-done
				return false, 0
			case svc.Interrogate:
				status <- c.CurrentStatus
			}
		}
	}
}

// Install registers rmmagent as a Windows service that starts automatically.
// exePath is the full path to the rmmagent binary; stateDir is the device
// identity directory (written into the service command line).
func Install(exePath, stateDir string) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("install-service: open SCM: %w", err)
	}
	defer m.Disconnect()

	if s, err := m.OpenService(ServiceName); err == nil {
		s.Close()
		return fmt.Errorf("service %q is already installed (run uninstall-service first)", ServiceName)
	}

	imagePath := quoteArg(exePath) + " run --state-dir " + quoteArg(stateDir)
	s, err := m.CreateService(ServiceName, imagePath, mgr.Config{
		StartType:   mgr.StartAutomatic,
		DisplayName: serviceDisplayName,
		Description: serviceDescription,
	})
	if err != nil {
		return fmt.Errorf("install-service: create service: %w", err)
	}
	s.Close()
	return nil
}

// Uninstall stops and removes the rmmagent service.
func Uninstall() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("uninstall-service: open SCM: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(ServiceName)
	if err != nil {
		return fmt.Errorf("uninstall-service: service %q not found: %w", ServiceName, err)
	}
	defer s.Close()

	// Best-effort stop; the service may already be stopped.
	_, _ = s.Control(svc.Stop)

	return s.Delete()
}

// quoteArg wraps arg in double quotes if it contains whitespace.
func quoteArg(arg string) string {
	if strings.ContainsAny(arg, " \t") {
		return `"` + arg + `"`
	}
	return arg
}

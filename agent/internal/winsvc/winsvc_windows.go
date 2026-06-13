//go:build windows

// Package winsvc integrates the agent with the Windows Service Control Manager.
package winsvc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"golang.org/x/sys/windows/registry"
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

	// Pass only the bare exe path so mgr quotes it cleanly as "exe".
	// Passing a pre-quoted or arg-containing string causes mgr to double-quote,
	// producing an ImagePath the SCM cannot parse.
	s, err := m.CreateService(ServiceName, exePath, mgr.Config{
		StartType:   mgr.StartAutomatic,
		DisplayName: serviceDisplayName,
		Description: serviceDescription,
	})
	if err != nil {
		return fmt.Errorf("install-service: create service: %w", err)
	}
	s.Close()

	// mgr.CreateService stores only the exe path; patch ImagePath directly in
	// the registry to append the subcommand and state-dir argument.
	imagePath := `"` + exePath + `" run --state-dir "` + stateDir + `"`
	key, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`SYSTEM\CurrentControlSet\Services\`+ServiceName,
		registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("install-service: open service registry key: %w", err)
	}
	defer key.Close()
	if err := key.SetStringValue("ImagePath", imagePath); err != nil {
		return fmt.Errorf("install-service: patch ImagePath: %w", err)
	}
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


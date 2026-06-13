//go:build !windows

// Package winsvc integrates the agent with the Windows Service Control Manager.
package winsvc

import (
	"context"
	"errors"
	"log/slog"
)

const ServiceName = "rmmagent"

// Runner is implemented by conn.Agent (and any test double).
type Runner interface {
	Run(ctx context.Context) error
}

var errNotWindows = errors.New("Windows service management is only supported on Windows")

// IsService always returns false on non-Windows platforms.
func IsService() bool { return false }

// Run is not supported on non-Windows platforms.
func Run(_ string, _ Runner, _ *slog.Logger) error { return errNotWindows }

// Install is not supported on non-Windows platforms.
func Install(_, _ string) error { return errNotWindows }

// Uninstall is not supported on non-Windows platforms.
func Uninstall() error { return errNotWindows }

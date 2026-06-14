//go:build linux

package shell

import (
	"os"
	"os/exec"

	"github.com/creack/pty"
)

// startPTY launches an interactive login shell attached to a new PTY of
// the given size. The shell runs with the agent's privileges (root under
// systemd), which is the remote-administration model — access is gated by
// the server's shell.connect permission and fully audited/recorded.
func startPTY(cols, rows uint16) (ptySession, error) {
	cmd := exec.Command(shellPath())
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")

	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: cols, Rows: rows})
	if err != nil {
		return nil, err
	}
	return &linuxPTY{f: f, cmd: cmd}, nil
}

// shellPath picks the user's shell, falling back to common locations.
func shellPath() string {
	if s := os.Getenv("SHELL"); s != "" {
		if _, err := os.Stat(s); err == nil {
			return s
		}
	}
	for _, p := range []string{"/bin/bash", "/usr/bin/bash", "/bin/sh"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "/bin/sh"
}

type linuxPTY struct {
	f   *os.File
	cmd *exec.Cmd
}

func (p *linuxPTY) Read(b []byte) (int, error)  { return p.f.Read(b) }
func (p *linuxPTY) Write(b []byte) (int, error) { return p.f.Write(b) }

func (p *linuxPTY) Resize(cols, rows uint16) error {
	return pty.Setsize(p.f, &pty.Winsize{Cols: cols, Rows: rows})
}

// Close kills the shell process, closes the PTY master, and reaps the
// child so no zombie is left behind. Safe to call once per session.
func (p *linuxPTY) Close() error {
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	err := p.f.Close()
	_ = p.cmd.Wait()
	return err
}

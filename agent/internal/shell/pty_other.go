//go:build !linux

package shell

import "errors"

// startPTY is unavailable on non-Linux endpoints at MVP (the plan ships
// the Linux agent first). The cross-platform agent still builds; remote
// shell simply reports unsupported until a platform PTY is added.
func startPTY(cols, rows uint16) (ptySession, error) {
	return nil, errors.New("remote shell is only supported on Linux agents")
}

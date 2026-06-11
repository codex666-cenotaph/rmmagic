// Package version holds build-time version information shared by the
// server and agent. Values are injected via -ldflags at release time.
package version

var (
	// Version is the semantic version of the build (e.g. "0.1.0").
	Version = "0.0.0-dev"
	// Commit is the git commit hash the binary was built from.
	Commit = "unknown"
)

// ProtocolVersion is the agent<->server wire protocol version. Bump only
// on breaking protocol changes; the gateway rejects agents speaking a
// newer major protocol than it understands.
const ProtocolVersion = 1

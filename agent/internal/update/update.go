// Package update implements the agent side of signed auto-update:
// download a release binary, verify its sha256 and a detached Ed25519
// signature against the agent's embedded trusted keys, atomically swap the
// running executable (keeping a .prev for rollback), and confirm the new
// build is healthy before discarding the previous one.
//
// Trust: the agent never runs a binary it cannot verify. The signature is
// raw Ed25519 over the exact bytes downloaded; the matching public key(s)
// are embedded at build time via the TrustedKeysB64 ldflag so a rogue
// server cannot push code. Multiple keys are supported for rotation.
package update

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// TrustedKeysB64 is a comma-separated list of base64 (std) Ed25519 public
// keys the agent trusts to sign releases. Injected at build time, e.g.:
//
//	-ldflags "-X github.com/.../agent/internal/update.TrustedKeysB64=AAAA..,BBBB.."
//
// Empty in dev builds: with no trusted keys, verification always fails, so
// an un-provisioned agent refuses every update (fail closed).
var TrustedKeysB64 string

// maxBinaryBytes caps a downloaded release to a sane size (defends against
// a malicious/huge response exhausting disk before the hash check).
const maxBinaryBytes = 256 << 20 // 256 MiB

// healthCheckWindow is how long a freshly-applied build has to reach a
// healthy state before the watchdog rolls back to the previous binary.
const healthCheckWindow = 5 * time.Minute

// markerName is the rollback marker persisted in the state dir.
const markerName = "update.json"

// TrustedKeys parses TrustedKeysB64 into Ed25519 public keys.
func TrustedKeys() ([]ed25519.PublicKey, error) {
	var keys []ed25519.PublicKey
	for _, part := range strings.Split(TrustedKeysB64, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(part)
		if err != nil {
			return nil, fmt.Errorf("bad trusted key encoding: %w", err)
		}
		if len(raw) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("bad trusted key length %d", len(raw))
		}
		keys = append(keys, ed25519.PublicKey(raw))
	}
	return keys, nil
}

// Verify checks that data hashes to sha256Hex and carries a valid Ed25519
// signature (base64) from one of the trusted keys. Returns nil only when
// both the hash and a signature match.
func Verify(data []byte, sha256Hex, signatureB64 string, keys []ed25519.PublicKey) error {
	if len(keys) == 0 {
		return errors.New("no trusted update keys embedded; refusing update")
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); !strings.EqualFold(got, sha256Hex) {
		return fmt.Errorf("sha256 mismatch: got %s want %s", got, sha256Hex)
	}
	sig, err := base64.StdEncoding.DecodeString(signatureB64)
	if err != nil {
		return fmt.Errorf("bad signature encoding: %w", err)
	}
	for _, k := range keys {
		if ed25519.Verify(k, data, sig) {
			return nil
		}
	}
	return errors.New("signature does not match any trusted key")
}

// Download fetches up to maxBinaryBytes from url. The body is read fully so
// the caller can hash and verify before anything touches disk durably.
func Download(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBinaryBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBinaryBytes {
		return nil, fmt.Errorf("release exceeds %d byte cap", maxBinaryBytes)
	}
	return data, nil
}

// Marker records an in-progress update so a restart can confirm health or
// roll back. Persisted in the state dir (writable), separate from the
// binary location.
type Marker struct {
	Version   string    `json:"version"`
	SelfPath  string    `json:"self_path"`
	PrevPath  string    `json:"prev_path"`
	AppliedAt time.Time `json:"applied_at"`
	Deadline  time.Time `json:"deadline"`
}

func markerPath(stateDir string) string { return filepath.Join(stateDir, markerName) }

// Apply atomically replaces the executable at selfPath with verified bytes,
// keeping the previous binary at selfPath+".prev", and writes a rollback
// marker into stateDir. The caller should restart into the new binary after
// a nil return.
//
// Steps are ordered so a crash at any point leaves a runnable binary:
// stage temp -> move current aside (.prev) -> move new into place. If the
// final move fails, the previous binary is restored before returning.
func Apply(stateDir, selfPath string, data []byte, version string) error {
	dir := filepath.Dir(selfPath)
	tmp, err := os.CreateTemp(dir, ".rmmagent-update-*")
	if err != nil {
		return fmt.Errorf("stage update: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("write update: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Chmod(tmpPath, 0o755); err != nil {
		os.Remove(tmpPath)
		return err
	}

	prevPath := selfPath + ".prev"
	_ = os.Remove(prevPath)
	if err := os.Rename(selfPath, prevPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("back up current binary: %w", err)
	}
	if err := os.Rename(tmpPath, selfPath); err != nil {
		// Restore so we never leave the host without an agent binary.
		_ = os.Rename(prevPath, selfPath)
		os.Remove(tmpPath)
		return fmt.Errorf("install update: %w", err)
	}

	m := Marker{
		Version:   version,
		SelfPath:  selfPath,
		PrevPath:  prevPath,
		AppliedAt: time.Now().UTC(),
		Deadline:  time.Now().UTC().Add(healthCheckWindow),
	}
	if err := writeMarker(stateDir, m); err != nil {
		// The swap succeeded; a missing marker only disables auto-rollback.
		return nil //nolint:nilerr
	}
	return nil
}

func writeMarker(stateDir string, m Marker) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := markerPath(stateDir) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, markerPath(stateDir))
}

// PendingMarker returns the rollback marker if an update is awaiting a
// health confirmation.
func PendingMarker(stateDir string) (Marker, bool) {
	b, err := os.ReadFile(markerPath(stateDir))
	if err != nil {
		return Marker{}, false
	}
	var m Marker
	if err := json.Unmarshal(b, &m); err != nil {
		return Marker{}, false
	}
	return m, true
}

// ConfirmHealthy clears the rollback marker and removes the kept-around
// previous binary: the new build proved it can connect, so commit to it.
func ConfirmHealthy(stateDir string) {
	if m, ok := PendingMarker(stateDir); ok {
		_ = os.Remove(m.PrevPath)
	}
	_ = os.Remove(markerPath(stateDir))
}

// Rollback restores the previous binary recorded in the marker and clears
// the marker. Used by the watchdog when a new build fails to become
// healthy in time.
func Rollback(stateDir string) error {
	m, ok := PendingMarker(stateDir)
	if !ok {
		return errors.New("no pending update to roll back")
	}
	if _, err := os.Stat(m.PrevPath); err != nil {
		return fmt.Errorf("previous binary missing: %w", err)
	}
	if err := os.Rename(m.PrevPath, m.SelfPath); err != nil {
		return fmt.Errorf("restore previous binary: %w", err)
	}
	_ = os.Remove(markerPath(stateDir))
	return nil
}

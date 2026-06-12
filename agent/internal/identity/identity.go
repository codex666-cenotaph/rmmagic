// Package identity manages the device's cryptographic identity: an
// Ed25519 keypair generated locally at enrollment. The private key
// never leaves the device.
package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const fileName = "identity.json"

type Identity struct {
	DeviceID  string `json:"device_id"`
	ServerURL string `json:"server_url"`
	// PrivateKeyB64 is the base64 Ed25519 seed+public concatenation
	// (ed25519.PrivateKey raw bytes).
	PrivateKeyB64 string `json:"private_key"`
	Revoked       bool   `json:"revoked,omitempty"`
}

func (id *Identity) PrivateKey() (ed25519.PrivateKey, error) {
	b, err := base64.StdEncoding.DecodeString(id.PrivateKeyB64)
	if err != nil || len(b) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("corrupt private key in identity file")
	}
	return ed25519.PrivateKey(b), nil
}

// GenerateKey creates a fresh keypair.
func GenerateKey() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	return ed25519.GenerateKey(rand.Reader)
}

// Save writes the identity with restrictive permissions (key material).
func Save(stateDir string, id *Identity) error {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(id, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(stateDir, fileName+".tmp")
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(stateDir, fileName))
}

func Load(stateDir string) (*Identity, error) {
	b, err := os.ReadFile(filepath.Join(stateDir, fileName))
	if err != nil {
		return nil, err
	}
	var id Identity
	if err := json.Unmarshal(b, &id); err != nil {
		return nil, fmt.Errorf("corrupt identity file: %w", err)
	}
	return &id, nil
}

func Exists(stateDir string) bool {
	_, err := os.Stat(filepath.Join(stateDir, fileName))
	return err == nil
}

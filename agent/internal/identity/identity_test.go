package identity

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	pub, priv, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	id := &Identity{
		DeviceID:      "11111111-1111-1111-1111-111111111111",
		ServerURL:     "https://rmm.example.com",
		PrivateKeyB64: base64.StdEncoding.EncodeToString(priv),
	}
	if err := Save(dir, id); err != nil {
		t.Fatal(err)
	}
	if !Exists(dir) {
		t.Fatal("identity should exist after save")
	}

	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	key, err := got.PrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	if !pub.Equal(key.Public()) {
		t.Fatal("loaded private key does not match generated public key")
	}
	if got.DeviceID != id.DeviceID || got.ServerURL != id.ServerURL {
		t.Fatalf("round trip mismatch: %+v", got)
	}

	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(dir, "identity.json"))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("identity file must be 0600, got %v", info.Mode().Perm())
		}
	}
}

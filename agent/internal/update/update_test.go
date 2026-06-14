package update

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func sign(t *testing.T, data []byte) (ed25519.PublicKey, string, string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	sig := ed25519.Sign(priv, data)
	return pub, hex.EncodeToString(sum[:]), base64.StdEncoding.EncodeToString(sig)
}

func TestVerify(t *testing.T) {
	data := []byte("a brand new agent binary")
	pub, shaHex, sigB64 := sign(t, data)
	keys := []ed25519.PublicKey{pub}

	if err := Verify(data, shaHex, sigB64, keys); err != nil {
		t.Fatalf("valid release rejected: %v", err)
	}

	// No trusted keys => fail closed.
	if err := Verify(data, shaHex, sigB64, nil); err == nil {
		t.Error("expected failure with no trusted keys")
	}
	// Tampered bytes => sha mismatch.
	if err := Verify([]byte("tampered"), shaHex, sigB64, keys); err == nil {
		t.Error("expected sha256 mismatch")
	}
	// Wrong key => signature mismatch.
	other, _, _ := sign(t, data)
	if err := Verify(data, shaHex, sigB64, []ed25519.PublicKey{other}); err == nil {
		t.Error("expected signature mismatch with untrusted key")
	}
	// Bad signature encoding.
	if err := Verify(data, shaHex, "!!!not base64!!!", keys); err == nil {
		t.Error("expected bad signature encoding error")
	}
}

func TestTrustedKeysParsing(t *testing.T) {
	pub1, _, _ := sign(t, []byte("x"))
	pub2, _, _ := sign(t, []byte("y"))
	TrustedKeysB64 = base64.StdEncoding.EncodeToString(pub1) + " , " +
		base64.StdEncoding.EncodeToString(pub2)
	t.Cleanup(func() { TrustedKeysB64 = "" })

	keys, err := TrustedKeys()
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys, got %d", len(keys))
	}
}

func TestDownload(t *testing.T) {
	body := []byte("binary payload")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	got, err := Download(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) {
		t.Fatalf("got %q want %q", got, body)
	}

	// A 404 is an error, not silent empty bytes.
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer bad.Close()
	if _, err := Download(context.Background(), bad.Client(), bad.URL); err == nil {
		t.Error("expected error on HTTP 404")
	}
}

func TestApplyConfirmHealthy(t *testing.T) {
	dir := t.TempDir()
	stateDir := t.TempDir()
	selfPath := filepath.Join(dir, "rmmagent")
	if err := os.WriteFile(selfPath, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := Apply(stateDir, selfPath, []byte("NEW"), "1.2.3"); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(selfPath); string(got) != "NEW" {
		t.Fatalf("binary not swapped: %q", got)
	}
	if got, _ := os.ReadFile(selfPath + ".prev"); string(got) != "OLD" {
		t.Fatalf("previous binary not preserved: %q", got)
	}
	m, ok := PendingMarker(stateDir)
	if !ok || m.Version != "1.2.3" {
		t.Fatalf("marker not written: %+v ok=%v", m, ok)
	}

	ConfirmHealthy(stateDir)
	if _, ok := PendingMarker(stateDir); ok {
		t.Error("marker should be cleared after ConfirmHealthy")
	}
	if _, err := os.Stat(selfPath + ".prev"); !os.IsNotExist(err) {
		t.Error(".prev should be removed after ConfirmHealthy")
	}
}

func TestApplyRollback(t *testing.T) {
	dir := t.TempDir()
	stateDir := t.TempDir()
	selfPath := filepath.Join(dir, "rmmagent")
	if err := os.WriteFile(selfPath, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Apply(stateDir, selfPath, []byte("NEW"), "2.0.0"); err != nil {
		t.Fatal(err)
	}

	if err := Rollback(stateDir); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(selfPath); string(got) != "OLD" {
		t.Fatalf("rollback did not restore previous binary: %q", got)
	}
	if _, ok := PendingMarker(stateDir); ok {
		t.Error("marker should be cleared after rollback")
	}
	// Nothing pending now.
	if err := Rollback(stateDir); err == nil {
		t.Error("expected error rolling back with no pending update")
	}
}

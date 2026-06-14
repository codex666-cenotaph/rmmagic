package storage

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

func TestFSRoundtrip(t *testing.T) {
	st, err := NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	key := "releases/abc-123"
	want := []byte("a signed agent binary")

	if ok, _ := st.Exists(ctx, key); ok {
		t.Fatal("key should not exist yet")
	}
	if err := st.Put(ctx, key, bytes.NewReader(want), int64(len(want))); err != nil {
		t.Fatal(err)
	}
	if ok, err := st.Exists(ctx, key); err != nil || !ok {
		t.Fatalf("expected key to exist: ok=%v err=%v", ok, err)
	}

	rc, size, err := st.Get(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	if size != int64(len(want)) {
		t.Fatalf("size: got %d want %d", size, len(want))
	}
	got, _ := io.ReadAll(rc)
	if !bytes.Equal(got, want) {
		t.Fatalf("content mismatch: %q", got)
	}

	// Overwrite works.
	if err := st.Put(ctx, key, bytes.NewReader([]byte("v2")), 2); err != nil {
		t.Fatal(err)
	}
	rc2, _, _ := st.Get(ctx, key)
	got2, _ := io.ReadAll(rc2)
	rc2.Close()
	if string(got2) != "v2" {
		t.Fatalf("overwrite failed: %q", got2)
	}
}

func TestFSMissing(t *testing.T) {
	st, _ := NewFS(t.TempDir())
	if _, _, err := st.Get(context.Background(), "nope"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestFSContainsTraversal(t *testing.T) {
	root := t.TempDir()
	st, _ := NewFS(root)
	// A key with traversal segments must be neutralized (clamped under
	// root), never resolve outside it.
	p, err := st.safePath("../../escape")
	if err != nil {
		t.Fatal(err)
	}
	if rel, _ := filepath.Rel(root, p); strings.HasPrefix(rel, "..") {
		t.Fatalf("key escaped root: %s", p)
	}
	if err := st.Put(context.Background(), "../../escape", bytes.NewReader([]byte("x")), 1); err != nil {
		t.Fatalf("contained traversal key should still store: %v", err)
	}
}

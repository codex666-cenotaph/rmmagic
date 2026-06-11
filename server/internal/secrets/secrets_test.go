package secrets

import (
	"bytes"
	"strings"
	"testing"
)

func TestSealOpenRoundTrip(t *testing.T) {
	box, err := NewBox(strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	ct, err := box.Seal([]byte("totp-seed"), []byte("user-1"))
	if err != nil {
		t.Fatal(err)
	}
	pt, err := box.Open(ct, []byte("user-1"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(pt, []byte("totp-seed")) {
		t.Fatalf("round trip mismatch: %q", pt)
	}
	// Context binding: opening with different additional data must fail.
	if _, err := box.Open(ct, []byte("user-2")); err == nil {
		t.Fatal("expected failure with wrong additional data")
	}
}

func TestNewBoxRejectsBadKeys(t *testing.T) {
	for _, k := range []string{"", "abcd", strings.Repeat("zz", 32)} {
		if _, err := NewBox(k); err == nil {
			t.Errorf("NewBox(%q) should fail", k)
		}
	}
}

package recordings

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestRecorderCastFormat(t *testing.T) {
	var buf bytes.Buffer
	rec, err := NewRecorder(&buf, 120, 40)
	if err != nil {
		t.Fatal(err)
	}
	rec.Output([]byte("hello\r\n"))
	rec.Resize(80, 24)
	rec.Output([]byte("bye\r\n"))
	if err := rec.Err(); err != nil {
		t.Fatalf("recorder error: %v", err)
	}

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("expected header + 3 events, got %d lines: %q", len(lines), buf.String())
	}

	var hdr castHeader
	if err := json.Unmarshal([]byte(lines[0]), &hdr); err != nil {
		t.Fatalf("bad header: %v", err)
	}
	if hdr.Version != 2 || hdr.Width != 120 || hdr.Height != 40 {
		t.Fatalf("unexpected header: %+v", hdr)
	}

	// Each event is [float, code, data].
	var ev []any
	if err := json.Unmarshal([]byte(lines[1]), &ev); err != nil || len(ev) != 3 {
		t.Fatalf("bad event line: %q (%v)", lines[1], err)
	}
	if ev[1] != "o" || ev[2] != "hello\r\n" {
		t.Fatalf("unexpected output event: %v", ev)
	}
	if err := json.Unmarshal([]byte(lines[2]), &ev); err != nil {
		t.Fatal(err)
	}
	if ev[1] != "r" || ev[2] != "80x24" {
		t.Fatalf("unexpected resize event: %v", ev)
	}
}

func TestRecorderSizeCap(t *testing.T) {
	var buf bytes.Buffer
	rec, _ := NewRecorder(&buf, 80, 24)
	big := bytes.Repeat([]byte("x"), MaxRecordingBytes+1)
	rec.Output(big)
	if !rec.Truncated() {
		t.Fatal("expected recorder to report truncation past the cap")
	}
}

func TestFSStoreRoundTrip(t *testing.T) {
	st, err := Open(context.Background(), Config{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	key := "recordings/00000000-0000-0000-0000-000000000000/sess.cast"
	payload := []byte("cast-bytes\n[1.0,\"o\",\"hi\"]\n")
	if err := st.Put(context.Background(), key, bytes.NewReader(payload), int64(len(payload))); err != nil {
		t.Fatal(err)
	}
	rc, err := st.Get(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if !bytes.Equal(got, payload) {
		t.Fatalf("round trip mismatch: %q != %q", got, payload)
	}
}

func TestSafeKeyRejectsTraversal(t *testing.T) {
	for _, bad := range []string{"../escape", "a/../../b", ""} {
		if _, err := safeKey(bad); err == nil {
			t.Errorf("expected %q to be rejected", bad)
		}
	}
	if _, err := safeKey("recordings/x/y.cast"); err != nil {
		t.Errorf("valid key rejected: %v", err)
	}
}

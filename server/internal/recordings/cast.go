// Package recordings records remote-shell sessions in the asciinema v2
// "cast" format and stores them in object storage (filesystem or S3) for
// later playback.
//
// The cast format is a JSON header line followed by one JSON array per
// event: [elapsed_seconds, code, data]. code is "o" for terminal output
// and "r" for a resize ("COLSxROWS"). See
// https://docs.asciinema.org/manual/asciicast/v2/.
package recordings

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"
)

// MaxRecordingBytes caps the recorded output of a single session. Past
// the cap the session keeps streaming live but recording stops, so a
// runaway process (e.g. `yes`) cannot fill the disk/bucket.
const MaxRecordingBytes = 64 << 20 // 64 MiB

// Recorder serializes shell events to an asciinema v2 stream. It is safe
// for concurrent use: output and resize events arrive from different
// pump goroutines.
type Recorder struct {
	mu        sync.Mutex
	w         io.Writer
	start     time.Time
	written   int64
	truncated bool
	err       error
}

type castHeader struct {
	Version   int               `json:"version"`
	Width     int               `json:"width"`
	Height    int               `json:"height"`
	Timestamp int64             `json:"timestamp"`
	Env       map[string]string `json:"env,omitempty"`
}

// NewRecorder writes the cast header for a cols×rows terminal and returns
// a recorder appending to w.
func NewRecorder(w io.Writer, cols, rows int) (*Recorder, error) {
	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}
	r := &Recorder{w: w, start: time.Now()}
	hdr, err := json.Marshal(castHeader{
		Version: 2, Width: cols, Height: rows,
		Timestamp: r.start.Unix(),
		Env:       map[string]string{"TERM": "xterm-256color"},
	})
	if err != nil {
		return nil, err
	}
	if _, err := fmt.Fprintf(w, "%s\n", hdr); err != nil {
		return nil, err
	}
	return r, nil
}

// Output records a chunk of terminal output. Once the size cap is hit
// further output is dropped from the recording (Truncated reports this).
func (r *Recorder) Output(p []byte) {
	r.event("o", string(p), int64(len(p)))
}

// Resize records a terminal resize.
func (r *Recorder) Resize(cols, rows int) {
	r.event("r", fmt.Sprintf("%dx%d", cols, rows), 0)
}

func (r *Recorder) event(code, data string, cost int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return
	}
	if r.written+cost > MaxRecordingBytes {
		r.truncated = true
		return
	}
	line, err := json.Marshal([]any{time.Since(r.start).Seconds(), code, data})
	if err != nil {
		r.err = err
		return
	}
	if _, err := fmt.Fprintf(r.w, "%s\n", line); err != nil {
		r.err = err
		return
	}
	r.written += cost
}

// Truncated reports whether the recording hit the size cap.
func (r *Recorder) Truncated() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.truncated
}

// Err returns the first write error encountered, if any.
func (r *Recorder) Err() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}

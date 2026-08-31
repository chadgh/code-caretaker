// Package status provides logging and the append-only JSONL status feed.
//
// The feed is consumed by a separate relay session. Every cycle appends at
// least one record, so the newest line's ts doubles as a heartbeat: if it goes
// stale (> ~1h) the loop has likely died. Records with notify=true are meant
// to trigger a proactive ping.
package status

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Event is one record destined for the status feed. Fields carries any extra
// key/values beyond the standard set; Event must always carry a non-empty
// Event name.
type Event struct {
	Event   string
	Level   string
	Notify  bool
	Summary string
	Fields  map[string]any
}

// Log prints a timestamped line to stdout.
func Log(msg string) {
	fmt.Printf("[%s] %s\n", time.Now().Format("2006-01-02 15:04:05"), msg)
	os.Stdout.Sync()
}

// Logf is Log with printf-style formatting.
func Logf(format string, args ...any) {
	Log(fmt.Sprintf(format, args...))
}

// Emit appends one JSON record to the status feed (best-effort, never fatal).
// level defaults to "info" when empty.
func Emit(statusFile string, ev Event) {
	level := ev.Level
	if level == "" {
		level = "info"
	}
	record := map[string]any{
		"ts":      time.Now().UTC().Format(time.RFC3339Nano),
		"event":   ev.Event,
		"level":   level,
		"notify":  ev.Notify,
		"summary": ev.Summary,
	}
	for k, v := range ev.Fields {
		record[k] = v
	}

	data, err := json.Marshal(record)
	if err != nil {
		Logf("Failed to encode status record: %v", err)
		return
	}

	if parent := filepath.Dir(statusFile); parent != "" {
		if err := os.MkdirAll(parent, 0o755); err != nil {
			Logf("Failed to write status feed: %v", err)
			return
		}
	}
	f, err := os.OpenFile(statusFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		Logf("Failed to write status feed: %v", err)
		return
	}
	defer f.Close()
	if _, err := f.Write(append(data, '\n')); err != nil {
		Logf("Failed to write status feed: %v", err)
	}
}

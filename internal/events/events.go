// Package events provides structured, secret-redacted event and operational
// log persistence for BeeChief (v0.5 hardening).
//
// Events are stored in a bounded in-memory ring buffer and appended as JSON
// Lines (JSONL) to disk with automatic size-based rotation and 0600 file
// permissions.
package events

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/meistro57/beechief/internal/report"
)

// EnvEventsPath overrides the default persisted events JSONL file path.
const EnvEventsPath = "BEECHIEF_EVENTS_LOG"

const (
	// DefaultMaxBytes caps the active events.jsonl file size before rotating
	// to events.jsonl.1.
	DefaultMaxBytes int64 = 2 * 1024 * 1024
	// MaxMemoryEvents bounds the in-memory ring buffer.
	MaxMemoryEvents = 500
)

// Kind classifies the operational event recorded by BeeChief.
type Kind string

const (
	KindScan           Kind = "scan"
	KindSkillCheck     Kind = "skill_check"
	KindSelfReview     Kind = "self_review"
	KindSwarmReview    Kind = "swarm_review"
	KindHiveHealth     Kind = "hive_health"
	KindServiceControl Kind = "service_control"
	KindHoneyReport    Kind = "honey_report"
	KindConfigChange   Kind = "config_change"
	KindInstruction    Kind = "instruction"
)

// Level classifies event severity.
type Level string

const (
	LevelInfo  Level = "info"
	LevelWarn  Level = "warn"
	LevelError Level = "error"
)

// Event is one structured operational log entry.
type Event struct {
	Timestamp  time.Time `json:"timestamp"`
	Kind       Kind      `json:"kind"`
	Level      Level     `json:"level"`
	Hive       string    `json:"hive,omitempty"`
	Bee        string    `json:"bee,omitempty"`
	Summary    string    `json:"summary"`
	Detail     string    `json:"detail,omitempty"`
	DurationMs int64     `json:"durationMs,omitempty"`
}

// FormatLine renders e as a single-line human-readable log entry suitable for
// the unified log browser.
func (e Event) FormatLine() string {
	ts := e.Timestamp.UTC().Format("2006-01-02T15:04:05Z")
	lvl := strings.ToUpper(string(e.Level))
	if lvl == "" {
		lvl = "INFO"
	}
	var scope []string
	if e.Hive != "" {
		scope = append(scope, "hive="+e.Hive)
	}
	if e.Bee != "" {
		scope = append(scope, "bee="+e.Bee)
	}
	if e.DurationMs > 0 {
		scope = append(scope, fmt.Sprintf("dur=%dms", e.DurationMs))
	}
	scopeStr := ""
	if len(scope) > 0 {
		scopeStr = " (" + strings.Join(scope, " ") + ")"
	}
	line := fmt.Sprintf("%s %s [%s]%s %s", ts, lvl, e.Kind, scopeStr, e.Summary)
	if e.Detail != "" {
		line += " — " + strings.ReplaceAll(strings.TrimSpace(e.Detail), "\n", " | ")
	}
	return line
}

// DefaultPath returns $BEECHIEF_EVENTS_LOG if set, otherwise
// <user config dir>/beechief/events.jsonl.
func DefaultPath() string {
	if p := strings.TrimSpace(os.Getenv(EnvEventsPath)); p != "" {
		return p
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "beechief", "events.jsonl")
}

// Store manages structured event persistence and retrieval.
type Store struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	mem      []Event
}

// NewStore creates a Store backed by path (or in-memory only when path is empty).
func NewStore(path string, maxBytes int64) *Store {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	s := &Store{
		path:     strings.TrimSpace(path),
		maxBytes: maxBytes,
		mem:      make([]Event, 0, 64),
	}
	if s.path != "" {
		if loaded, err := readJSONLFile(s.path, MaxMemoryEvents); err == nil && len(loaded) > 0 {
			s.mem = loaded
		}
	}
	return s
}

// Path returns the configured JSONL persistence path.
func (s *Store) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

// Record sanitizes, redacts, and persists ev to memory and disk.
func (s *Store) Record(ev Event) error {
	if s == nil {
		return nil
	}
	if ev.Timestamp.IsZero() {
		ev.Timestamp = time.Now().UTC()
	} else {
		ev.Timestamp = ev.Timestamp.UTC()
	}
	if ev.Level == "" {
		ev.Level = LevelInfo
	}
	ev.Hive = report.Redact(strings.TrimSpace(ev.Hive))
	ev.Bee = report.Redact(strings.TrimSpace(ev.Bee))
	ev.Summary = report.Redact(strings.TrimSpace(ev.Summary))
	ev.Detail = report.Redact(strings.TrimSpace(ev.Detail))

	s.mu.Lock()
	defer s.mu.Unlock()

	s.mem = append(s.mem, ev)
	if len(s.mem) > MaxMemoryEvents {
		s.mem = append([]Event(nil), s.mem[len(s.mem)-MaxMemoryEvents:]...)
	}

	if s.path == "" {
		return nil
	}
	return s.appendDiskLocked(ev)
}

func (s *Store) appendDiskLocked(ev Event) error {
	if dir := filepath.Dir(s.path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create events directory: %w", err)
		}
	}
	if info, err := os.Stat(s.path); err == nil && info.Size() >= s.maxBytes {
		_ = os.Rename(s.path, s.path+".1")
	}
	data, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("encode event: %w", err)
	}
	data = append(data, '\n')
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open events log: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write event: %w", err)
	}
	return nil
}

// Recent returns up to the last limit events in chronological order.
func (s *Store) Recent(limit int) []Event {
	if s == nil {
		return nil
	}
	if limit <= 0 || limit > MaxMemoryEvents {
		limit = MaxMemoryEvents
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	n := len(s.mem)
	if n == 0 {
		return nil
	}
	start := 0
	if n > limit {
		start = n - limit
	}
	out := make([]Event, n-start)
	copy(out, s.mem[start:])
	return out
}

func readJSONLFile(path string, max int) ([]Event, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var list []Event
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var ev Event
		if err := json.Unmarshal([]byte(line), &ev); err == nil {
			list = append(list, ev)
		}
	}
	if max > 0 && len(list) > max {
		list = list[len(list)-max:]
	}
	return list, scanner.Err()
}

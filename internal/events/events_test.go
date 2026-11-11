package events

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreRedactsPersistsAndRotates(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "events.jsonl")

	store := NewStore(logPath, 350) // small maxBytes to trigger rotation
	err := store.Record(Event{
		Kind:       KindScan,
		Level:      LevelInfo,
		Hive:       "alpha",
		Summary:    "Scanned Hive alpha with token sk-secret1234567890abcdef",
		DurationMs: 42,
	})
	if err != nil {
		t.Fatalf("Record 1: %v", err)
	}

	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.Contains(string(raw), "sk-secret1234567890abcdef") {
		t.Errorf("secret was not redacted before writing to disk: %s", string(raw))
	}
	if !strings.Contains(string(raw), "[REDACTED]") {
		t.Errorf("expected [REDACTED] marker in persisted event: %s", string(raw))
	}

	// Record more events to exceed 350 bytes and verify rotation to events.jsonl.1
	for i := 0; i < 4; i++ {
		if err := store.Record(Event{
			Kind:    KindHiveHealth,
			Level:   LevelWarn,
			Hive:    "alpha",
			Summary: "Disk usage above 85% threshold on root filesystem",
		}); err != nil {
			t.Fatalf("Record %d: %v", i+2, err)
		}
	}

	if _, err := os.Stat(logPath + ".1"); err != nil {
		t.Errorf("expected rotated file %s.1 to exist: %v", logPath, err)
	}

	recent := store.Recent(10)
	if len(recent) != 5 {
		t.Fatalf("Recent(10): got %d events, want 5", len(recent))
	}
	if !strings.Contains(recent[0].FormatLine(), "[scan]") || !strings.Contains(recent[0].FormatLine(), "hive=alpha") {
		t.Errorf("unexpected FormatLine: %q", recent[0].FormatLine())
	}

	// Reload from disk in a fresh Store
	reloaded := NewStore(logPath, DefaultMaxBytes)
	if len(reloaded.Recent(10)) == 0 {
		t.Error("expected reloaded Store to read persisted JSONL events")
	}
}

func TestStoreRedactsHiveAndBeeFields(t *testing.T) {
	store := NewStore("", DefaultMaxBytes)
	if err := store.Record(Event{
		Kind:    KindInstruction,
		Level:   LevelInfo,
		Hive:    "api_key=supersecretvalue",
		Bee:     "password=hunter2",
		Summary: "ok",
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	recent := store.Recent(1)
	if len(recent) != 1 {
		t.Fatalf("Recent(1): got %d", len(recent))
	}
	if strings.Contains(recent[0].Hive, "supersecretvalue") || strings.Contains(recent[0].Bee, "hunter2") {
		t.Errorf("Hive/Bee were not redacted: %+v", recent[0])
	}
	if !strings.Contains(recent[0].Hive, "[REDACTED]") || !strings.Contains(recent[0].Bee, "[REDACTED]") {
		t.Errorf("expected redaction markers: %+v", recent[0])
	}
}

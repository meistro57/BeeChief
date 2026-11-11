package jobs

import (
	"testing"
	"time"
)

func TestParseScheduleAndNext(t *testing.T) {
	s, err := ParseSchedule("*/15 9-17 * * mon-fri")
	if err != nil {
		t.Fatalf("ParseSchedule: %v", err)
	}
	base := time.Date(2026, 1, 5, 8, 59, 0, 0, time.UTC) // Monday
	got := s.Next(base)
	want := time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("Next: got %v, want %v", got, want)
	}

	s, err = ParseSchedule("0 3 * * *")
	if err != nil {
		t.Fatal(err)
	}
	got = s.Next(time.Date(2026, 1, 5, 3, 0, 0, 0, time.UTC))
	want = time.Date(2026, 1, 6, 3, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("daily Next: got %v, want %v", got, want)
	}

	s, err = ParseSchedule("30 2 * * sun")
	if err != nil {
		t.Fatal(err)
	}
	got = s.Next(time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC))
	want = time.Date(2026, 1, 11, 2, 30, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("weekly Next: got %v, want %v", got, want)
	}
}

func TestParseScheduleRejectsInvalidInput(t *testing.T) {
	for _, expr := range []string{"", "* * * *", "61 * * * *", "*/0 * * * *", "x * * * *", "* 24 * * *", "0 0 32 * *"} {
		if _, err := ParseSchedule(expr); err == nil {
			t.Errorf("expected error for %q", expr)
		}
	}
}

func TestParseScheduleNamesAndRanges(t *testing.T) {
	if _, err := ParseSchedule("0 0 1 jan-mar mon-fri"); err != nil {
		t.Fatalf("named schedule: %v", err)
	}
	if _, err := ParseSchedule("0,30 8-18/2 * * 1-5"); err != nil {
		t.Fatalf("list/step schedule: %v", err)
	}
}

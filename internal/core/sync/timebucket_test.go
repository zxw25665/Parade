package sync

import (
	"testing"
	"time"
)

func TestParseHLCBucket_Valid(t *testing.T) {
	b, err := ParseHLCBucket("2026-04-13T10:30:00.000Z_0001_abc12345")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if b.Year != "2026" {
		t.Errorf("year = %q, want 2026", b.Year)
	}
	if b.Month != "2026-04" {
		t.Errorf("month = %q, want 2026-04", b.Month)
	}
	if b.Day != "2026-04-13" {
		t.Errorf("day = %q, want 2026-04-13", b.Day)
	}
	if b.Hour != "2026-04-13T10" {
		t.Errorf("hour = %q, want 2026-04-13T10", b.Hour)
	}
}

func TestParseHLCBucket_Invalid(t *testing.T) {
	_, err := ParseHLCBucket("invalid-format")
	if err == nil {
		t.Fatal("expected error for invalid HLC")
	}
}

func TestBucketPath(t *testing.T) {
	hlc := "2026-04-13T10:30:00.000Z_0001_abc12345"

	year, _ := BucketPath(LevelYear, hlc)
	if year != "2026" {
		t.Errorf("year path = %q", year)
	}

	month, _ := BucketPath(LevelMonth, hlc)
	if month != "2026-04" {
		t.Errorf("month path = %q", month)
	}

	day, _ := BucketPath(LevelDay, hlc)
	if day != "2026-04-13" {
		t.Errorf("day path = %q", day)
	}

	hour, _ := BucketPath(LevelHour, hlc)
	if hour != "2026-04-13T10" {
		t.Errorf("hour path = %q", hour)
	}
}

func TestParentBucketPath(t *testing.T) {
	tests := []struct {
		path  string
		level int
		want  string
	}{
		{"2026-04-13T10", LevelHour, "2026-04-13"},
		{"2026-04-13", LevelDay, "2026-04"},
		{"2026-04", LevelMonth, "2026"},
		{"2026", LevelYear, ""},
	}
	for _, tt := range tests {
		got := ParentBucketPath(tt.path, tt.level)
		if got != tt.want {
			t.Errorf("ParentBucketPath(%q, %d) = %q, want %q", tt.path, tt.level, got, tt.want)
		}
	}
}

func TestBucketTimeRange(t *testing.T) {
	start, end, err := BucketTimeRange("2026-04-13", LevelDay)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if start != "2026-04-13T00:00:00.000Z_0000_" {
		t.Errorf("start = %q", start)
	}
	if end != "2026-04-14T00:00:00.000Z_0000_" {
		t.Errorf("end = %q", end)
	}
}

func TestIsFrozenBucketAge(t *testing.T) {
	now := time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC)

	// 15 days ago - should be frozen
	oldDay := "2026-04-04"
	if !IsFrozenBucketAge(oldDay, LevelDay, now) {
		t.Error("expected old day to be frozen")
	}

	// 5 days ago - should NOT be frozen
	recentDay := "2026-04-15"
	if IsFrozenBucketAge(recentDay, LevelDay, now) {
		t.Error("expected recent day to NOT be frozen")
	}

	// Hour level should never be frozen by age
	if IsFrozenBucketAge("2026-04-13T10", LevelHour, now) {
		t.Error("expected hour bucket to never be frozen by age")
	}
}

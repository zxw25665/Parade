// Package sync implements the Sparse Time Bucket Merkle Tree synchronization algorithm.
//
// The algorithm organizes messages into a 5-level hierarchical time bucket structure:
//
//	Level 0: Year  (YYYY)
//	Level 1: Month (YYYY-MM)
//	Level 2: Day   (YYYY-MM-DD)
//	Level 3: Hour  (YYYY-MM-DDTHH)
//	Level 4: Message (individual HLC)
//
// Only buckets that contain messages are created (sparse). Each level forms a Merkle
// tree where leaf hashes are BLAKE3 of sorted child content. During sync, peers
// compare level by level from top to bottom — matching hashes mean the entire
// subtree is in sync; mismatches drill down to find the exact diverging messages.
package sync

import (
	"fmt"
	"regexp"
	"time"
)

// Bucket level constants.
const (
	LevelYear   = 0
	LevelMonth  = 1
	LevelDay    = 2
	LevelHour   = 3
	LevelMsg    = 4
	MaxLevel    = 4
)

// HLCBucket holds all bucket paths derived from a single HLC string.
type HLCBucket struct {
	Year  string // "2006"
	Month string // "2006-01"
	Day   string // "2006-01-02"
	Hour  string // "2006-01-02T15"
}

// hlcRegex matches the HLC format: "2006-01-02T15:04:05.000Z_0001_NodeID"
var hlcRegex = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})T(\d{2}):\d{2}:\d{2}\.\d{3}Z_\d{4}_.+$`)

// ParseHLCBucket extracts all bucket paths from an HLC string.
func ParseHLCBucket(hlc string) (*HLCBucket, error) {
	matches := hlcRegex.FindStringSubmatch(hlc)
	if matches == nil {
		return nil, fmt.Errorf("sync: invalid HLC format: %q", hlc)
	}
	return &HLCBucket{
		Year:  matches[1],
		Month: matches[1] + "-" + matches[2],
		Day:   matches[1] + "-" + matches[2] + "-" + matches[3],
		Hour:  matches[1] + "-" + matches[2] + "-" + matches[3] + "T" + matches[4],
	}, nil
}

// BucketPath returns the bucket path for a given level and HLC.
func BucketPath(level int, hlc string) (string, error) {
	b, err := ParseHLCBucket(hlc)
	if err != nil {
		return "", err
	}
	switch level {
	case LevelYear:
		return b.Year, nil
	case LevelMonth:
		return b.Month, nil
	case LevelDay:
		return b.Day, nil
	case LevelHour:
		return b.Hour, nil
	default:
		return "", fmt.Errorf("sync: invalid bucket level %d", level)
	}
}

// BucketLevelName returns a human-readable name for a bucket level.
func BucketLevelName(level int) string {
	switch level {
	case LevelYear:
		return "year"
	case LevelMonth:
		return "month"
	case LevelDay:
		return "day"
	case LevelHour:
		return "hour"
	case LevelMsg:
		return "message"
	default:
		return fmt.Sprintf("level_%d", level)
	}
}

// HLCToTime parses the time portion of an HLC string.
func HLCToTime(hlc string) (time.Time, error) {
	b, err := ParseHLCBucket(hlc)
	if err != nil {
		return time.Time{}, err
	}
	return time.Parse("2006-01-02T15", b.Hour)
}

// ParentBucketPath returns the parent bucket path for a given bucket path and its level.
// For example, ParentBucketPath("2026-04-13T10", LevelHour) returns "2026-04-13".
func ParentBucketPath(path string, level int) string {
	switch level {
	case LevelYear:
		return "" // year has no parent
	case LevelMonth:
		if len(path) >= 4 {
			return path[:4]
		}
	case LevelDay:
		if len(path) >= 7 {
			return path[:7]
		}
	case LevelHour:
		if len(path) >= 10 {
			return path[:10]
		}
	}
	return ""
}

// ChildBucketPrefix returns the prefix pattern for child buckets of a given bucket path.
// Used for GLOB queries in SQLite.
func ChildBucketPrefix(path string, level int) string {
	switch level {
	case LevelYear:
		return path + "-%"
	case LevelMonth:
		return path + "-%"
	case LevelDay:
		return path + "T%"
	case LevelHour:
		return path + "%" // message-level doesn't have a prefix pattern
	default:
		return path + "%"
	}
}

// BucketTimeRange returns the inclusive start and exclusive end HLC range for a bucket.
// This is used to fetch all messages within a bucket's time range.
func BucketTimeRange(path string, level int) (startHLC, endHLC string, err error) {
	var t time.Time
	switch level {
	case LevelYear:
		t, err = time.Parse("2006", path)
		if err != nil {
			return "", "", err
		}
		startHLC = t.Format("2006-01-02T15:04:05.000Z") + "_0000_"
		endHLC = t.AddDate(1, 0, 0).Format("2006-01-02T15:04:05.000Z") + "_0000_"
	case LevelMonth:
		t, err = time.Parse("2006-01", path)
		if err != nil {
			return "", "", err
		}
		startHLC = t.Format("2006-01-02T15:04:05.000Z") + "_0000_"
		endHLC = t.AddDate(0, 1, 0).Format("2006-01-02T15:04:05.000Z") + "_0000_"
	case LevelDay:
		t, err = time.Parse("2006-01-02", path)
		if err != nil {
			return "", "", err
		}
		startHLC = t.Format("2006-01-02T15:04:05.000Z") + "_0000_"
		endHLC = t.AddDate(0, 0, 1).Format("2006-01-02T15:04:05.000Z") + "_0000_"
	case LevelHour:
		t, err = time.Parse("2006-01-02T15", path)
		if err != nil {
			return "", "", err
		}
		startHLC = t.Format("2006-01-02T15:04:05.000Z") + "_0000_"
		endHLC = t.Add(1 * time.Hour).Format("2006-01-02T15:04:05.000Z") + "_0000_"
	default:
		return "", "", fmt.Errorf("sync: invalid bucket level %d for time range", level)
	}
	return startHLC, endHLC, nil
}

// IsFrozenBucketAge returns true if the given bucket path (day-level or higher)
// is older than the freeze window (14 days by default).
func IsFrozenBucketAge(bucketPath string, level int, now time.Time) bool {
	if level > LevelDay {
		return false // hour and message buckets are never frozen by age alone
	}
	var t time.Time
	var err error
	switch level {
	case LevelYear:
		t, err = time.Parse("2006", bucketPath)
	case LevelMonth:
		t, err = time.Parse("2006-01", bucketPath)
	case LevelDay:
		t, err = time.Parse("2006-01-02", bucketPath)
	default:
		return false
	}
	if err != nil {
		return false
	}
	// A day is "past" if its end (next day) is before now minus freeze window
	freezeThreshold := now.Add(-14 * 24 * time.Hour)
	return t.AddDate(0, 0, 1).Before(freezeThreshold)
}

package logger

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestNewLogBroker(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.log")

	lb, err := NewLogBroker(filePath, 100)
	if err != nil {
		t.Fatalf("NewLogBroker returned error: %v", err)
	}
	if lb == nil {
		t.Fatal("NewLogBroker returned nil broker")
	}
	defer lb.Close()

	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		t.Fatal("log file was not created")
	}

	if lb.maxEntries != 100 {
		t.Fatalf("expected maxEntries 100, got %d", lb.maxEntries)
	}
}

func TestLogBroker_Levels(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.log")

	lb, err := NewLogBroker(filePath, 100)
	if err != nil {
		t.Fatal(err)
	}
	defer lb.Close()

	lb.SetMinLevel(Trace)

	lb.Trace("src", "trace msg")
	lb.Debug("src", "debug msg")
	lb.Info("src", "info msg")
	lb.Warn("src", "warn msg")
	lb.Error("src", "error msg")

	entries := lb.Entries()
	if len(entries) != 5 {
		t.Fatalf("expected 5 entries, got %d", len(entries))
	}

	expectedLevels := []LogLevel{Trace, Debug, Info, Warning, Error}
	for i, entry := range entries {
		if entry.Level != expectedLevels[i] {
			t.Errorf("entry %d: expected level %s, got %s", i, expectedLevels[i], entry.Level)
		}
	}
}

func TestLogBroker_Filtering(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.log")

	lb, err := NewLogBroker(filePath, 100)
	if err != nil {
		t.Fatal(err)
	}
	defer lb.Close()

	lb.SetMinLevel(Warning)

	lb.Info("src", "should be filtered")
	lb.Warn("src", "should be stored")
	lb.Error("src", "should also be stored")

	entries := lb.Entries()
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries after filtering, got %d", len(entries))
	}
	if entries[0].Level != Warning {
		t.Errorf("expected first entry Warning, got %s", entries[0].Level)
	}
	if entries[1].Level != Error {
		t.Errorf("expected second entry Error, got %s", entries[1].Level)
	}
}

func TestLogBroker_MaxEntries(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.log")

	lb, err := NewLogBroker(filePath, 5)
	if err != nil {
		t.Fatal(err)
	}
	defer lb.Close()

	lb.SetMinLevel(Trace)

	for range 10 {
		lb.Info("src", "msg")
	}

	entries := lb.Entries()
	if len(entries) != 5 {
		t.Fatalf("expected 5 entries after overflow, got %d", len(entries))
	}

	// All 5 entries should exist and have valid timestamps
	for i, entry := range entries {
		if entry.Timestamp.IsZero() {
			t.Errorf("entry %d has zero timestamp", i)
		}
		if entry.Level != Info {
			t.Errorf("entry %d: expected Info, got %s", i, entry.Level)
		}
	}
}

func TestLogBroker_FileOutput(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.log")

	lb, err := NewLogBroker(filePath, 100)
	if err != nil {
		t.Fatal(err)
	}

	lb.Info("filesrc", "file test message")
	lb.Error("filesrc2", "error in file")

	if err := lb.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	f, err := os.Open(filePath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	var lineCount int
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		var entry LogEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			t.Errorf("invalid JSON line: %s (error: %v)", string(line), err)
			continue
		}
		if entry.Timestamp.IsZero() {
			t.Error("parsed entry has zero timestamp")
		}
		if entry.Source == "" {
			t.Error("parsed entry has empty source")
		}
		if entry.Message == "" {
			t.Error("parsed entry has empty message")
		}
		lineCount++
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}

	if lineCount < 1 {
		t.Fatal("expected at least 1 JSON line in log file, got 0")
	}
}

func TestLogBroker_Callback(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.log")

	lb, err := NewLogBroker(filePath, 100)
	if err != nil {
		t.Fatal(err)
	}
	defer lb.Close()

	var mu sync.Mutex
	var received []LogEntry

	lb.SetCallback(func(entry LogEntry) {
		mu.Lock()
		received = append(received, entry)
		mu.Unlock()
	})

	lb.Info("cb_src", "callback msg")
	lb.Warn("cb_src2", "another callback msg")

	mu.Lock()
	count := len(received)
	mu.Unlock()

	if count != 2 {
		t.Fatalf("callback should have been called 2 times, got %d", count)
	}

	mu.Lock()
	first := received[0]
	mu.Unlock()

	if first.Level != Info {
		t.Errorf("first callback entry: expected Info, got %s", first.Level)
	}
	if first.Source != "cb_src" {
		t.Errorf("first callback entry: expected source cb_src, got %s", first.Source)
	}
}

func TestLogBroker_Clear(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.log")

	lb, err := NewLogBroker(filePath, 100)
	if err != nil {
		t.Fatal(err)
	}
	defer lb.Close()

	lb.Info("src", "msg1")
	lb.Info("src", "msg2")

	if len(lb.Entries()) != 2 {
		t.Fatal("expected 2 entries before clear")
	}

	lb.Clear()

	if len(lb.Entries()) != 0 {
		t.Fatalf("expected 0 entries after clear, got %d", len(lb.Entries()))
	}
}

func TestLogBroker_Close(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.log")

	lb, err := NewLogBroker(filePath, 100)
	if err != nil {
		t.Fatal(err)
	}

	lb.Info("src", "before close")

	if err := lb.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	// Second close should be safe (file is already nil)
	if err := lb.Close(); err != nil {
		t.Fatalf("second Close returned error: %v", err)
	}
}

func TestLogBroker_Concurrent(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.log")

	lb, err := NewLogBroker(filePath, 1000)
	if err != nil {
		t.Fatal(err)
	}
	defer lb.Close()

	lb.SetMinLevel(Trace)

	const numGoroutines = 50
	const entriesPerGoroutine = 10

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for range numGoroutines {
		go func() {
			defer wg.Done()
			for range entriesPerGoroutine {
				lb.Info("concurrent", "test message")
			}
		}()
	}

	wg.Wait()

	entries := lb.Entries()
	if len(entries) > 0 && len(entries) > lb.maxEntries {
		t.Errorf("entries count %d exceeds maxEntries %d", len(entries), lb.maxEntries)
	}
	if len(entries) == 0 {
		t.Error("expected at least some entries after concurrent writes")
	}

	t.Logf("concurrent test: %d entries after %d goroutines x %d writes",
		len(entries), numGoroutines, entriesPerGoroutine)
}

func TestNewLogBroker_RejectsNonPositiveMaxEntries(t *testing.T) {
	dir := t.TempDir()

	for _, tc := range []struct {
		name       string
		maxEntries int
	}{
		{name: "zero", maxEntries: 0},
		{name: "negative", maxEntries: -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			filePath := filepath.Join(dir, tc.name+".log")

			lb, err := NewLogBroker(filePath, tc.maxEntries)
			if err == nil {
				t.Fatalf("NewLogBroker(maxEntries=%d) should return an error", tc.maxEntries)
			}
			if lb != nil {
				t.Fatal("NewLogBroker should return a nil broker for non-positive maxEntries")
			}
			if _, err := os.Stat(filePath); !os.IsNotExist(err) {
				t.Errorf("log file should not be created for maxEntries=%d", tc.maxEntries)
			}
		})
	}
}

func TestLogBroker_LogAfterClose(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.log")

	lb, err := NewLogBroker(filePath, 10)
	if err != nil {
		t.Fatal(err)
	}
	lb.SetMinLevel(Trace)

	lb.Info("src", "before close")

	if err := lb.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	// Logging after Close must not panic and must not write to the file.
	lb.Trace("src", "after close trace")
	lb.Info("src", "after close info")
	lb.Warn("src", "after close warn")
	lb.Error("src", "after close error")

	lines := readLogLines(t, filePath)
	if len(lines) != 1 {
		t.Fatalf("expected 1 line in log file after Close, got %d", len(lines))
	}
}

func TestLogBroker_ConcurrentConfigAndLogging(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.log")

	lb, err := NewLogBroker(filePath, 100)
	if err != nil {
		t.Fatal(err)
	}
	defer lb.Close()

	levels := []LogLevel{Trace, Debug, Info, Warning, Error}

	var wg sync.WaitGroup
	wg.Add(4)

	go func() {
		defer wg.Done()
		for i := range 100 {
			lb.SetMinLevel(levels[i%len(levels)])
		}
	}()

	go func() {
		defer wg.Done()
		for range 100 {
			lb.SetCallback(func(LogEntry) {})
		}
	}()

	go func() {
		defer wg.Done()
		for range 100 {
			lb.Info("cfg", "concurrent info")
		}
	}()

	go func() {
		defer wg.Done()
		for range 100 {
			lb.Warn("cfg", "concurrent warn")
		}
	}()

	wg.Wait()

	entries := lb.Entries()
	if len(entries) > lb.maxEntries {
		t.Fatalf("entries count %d exceeds maxEntries %d", len(entries), lb.maxEntries)
	}
}

func readLogLines(t *testing.T, filePath string) []string {
	t.Helper()
	f, err := os.Open(filePath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return lines
}

func TestLogLevel_String(t *testing.T) {
	tests := []struct {
		level    LogLevel
		expected string
	}{
		{Trace, "trace"},
		{Debug, "debug"},
		{Info, "info"},
		{Warning, "warning"},
		{Error, "error"},
		{LogLevel(99), "unknown"},
	}

	for _, tt := range tests {
		got := tt.level.String()
		if got != tt.expected {
			t.Errorf("LogLevel(%d).String() = %q, want %q", tt.level, got, tt.expected)
		}
	}
}

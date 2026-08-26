package logger

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// LogBroker is a thread-safe logger that stores entries in a ring buffer
// and writes them as JSON lines to a file.
type LogBroker struct {
	mu         sync.RWMutex
	fileMu     sync.Mutex
	buf        []LogEntry
	head       int
	count      int
	maxEntries int
	minLevel   LogLevel
	callback   func(LogEntry)
	file       *os.File
}

// NewLogBroker creates a LogBroker that appends JSON log lines to the given file.
// maxEntries controls the size of the in-memory ring buffer.
// The default minimum level is Debug.
func NewLogBroker(filePath string, maxEntries int) (*LogBroker, error) {
	if maxEntries <= 0 {
		return nil, fmt.Errorf("logger: maxEntries must be positive, got %d", maxEntries)
	}

	f, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("logger: failed to open log file: %w", err)
	}

	return &LogBroker{
		buf:        make([]LogEntry, maxEntries),
		maxEntries: maxEntries,
		minLevel:   Debug,
		file:       f,
	}, nil
}

func (lb *LogBroker) log(level LogLevel, source, msg string) {
	lb.mu.Lock()
	if level < lb.minLevel {
		lb.mu.Unlock()
		return
	}

	entry := LogEntry{
		Timestamp: time.Now(),
		Level:     level,
		Source:    source,
		Message:   msg,
	}

	lb.buf[lb.head] = entry
	lb.head = (lb.head + 1) % lb.maxEntries
	if lb.count < lb.maxEntries {
		lb.count++
	}
	cb := lb.callback
	lb.mu.Unlock()

	lb.writeToFile(entry)

	if cb != nil {
		cb(entry)
	}
}

func (lb *LogBroker) writeToFile(entry LogEntry) {
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}

	lb.fileMu.Lock()
	defer lb.fileMu.Unlock()

	// Close clears lb.file while holding fileMu; logging after Close must
	// neither panic nor write to a nil file.
	if lb.file == nil {
		return
	}
	fmt.Fprintf(lb.file, "%s\n", data)
}

func (lb *LogBroker) SetMinLevel(level LogLevel) {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	lb.minLevel = level
}

func (lb *LogBroker) SetCallback(cb func(LogEntry)) {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	lb.callback = cb
}

func (lb *LogBroker) Entries() []LogEntry {
	lb.mu.RLock()
	defer lb.mu.RUnlock()

	result := make([]LogEntry, lb.count)
	if lb.count == lb.maxEntries {
		n := copy(result, lb.buf[lb.head:])
		copy(result[n:], lb.buf[:lb.head])
	} else {
		copy(result, lb.buf[:lb.count])
	}
	return result
}

func (lb *LogBroker) Clear() {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	lb.head = 0
	lb.count = 0
}

func (lb *LogBroker) Close() error {
	lb.mu.Lock()
	defer lb.mu.Unlock()

	lb.fileMu.Lock()
	defer lb.fileMu.Unlock()

	if lb.file != nil {
		err := lb.file.Close()
		lb.file = nil
		return err
	}
	return nil
}

func (lb *LogBroker) Trace(source, msg string) { lb.log(Trace, source, msg) }
func (lb *LogBroker) Debug(source, msg string) { lb.log(Debug, source, msg) }
func (lb *LogBroker) Info(source, msg string)  { lb.log(Info, source, msg) }
func (lb *LogBroker) Warn(source, msg string)  { lb.log(Warning, source, msg) }
func (lb *LogBroker) Error(source, msg string) { lb.log(Error, source, msg) }

var _ Logger = (*LogBroker)(nil)

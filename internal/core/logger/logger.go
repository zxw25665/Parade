package logger

import "time"

// LogLevel represents the severity of a log entry.
type LogLevel int

const (
	Trace   LogLevel = 1
	Debug   LogLevel = 2
	Info    LogLevel = 3
	Warning LogLevel = 4
	Error   LogLevel = 5
)

// String returns the lowercase name of the log level.
func (l LogLevel) String() string {
	switch l {
	case Trace:
		return "trace"
	case Debug:
		return "debug"
	case Info:
		return "info"
	case Warning:
		return "warning"
	case Error:
		return "error"
	default:
		return "unknown"
	}
}

// LogEntry represents a single log record.
type LogEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Level     LogLevel  `json:"level"`
	Source    string    `json:"source"`
	Message   string    `json:"message"`
}

// Logger defines the standard logging interface.
type Logger interface {
	Trace(source, msg string)
	Debug(source, msg string)
	Info(source, msg string)
	Warn(source, msg string)
	Error(source, msg string)
}

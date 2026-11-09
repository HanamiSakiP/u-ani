package logger

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var (
	mu       sync.Mutex
	logFile  *os.File
	filePath string
)

const (
	AnsiGreen  = "\033[1;32m"
	AnsiYellow = "\033[1;33m"
	AnsiRed    = "\033[1;31m"
	AnsiEnd    = "\033[0m"
)

// Init configures logging to stderr and a daily log file.
func Init(logDir string) {
	mu.Lock()
	defer mu.Unlock()
	if logDir == "" {
		return
	}
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return
	}
	filePath = filepath.Join(logDir, time.Now().Format("2006-01-02")+".log")
	f, err := os.OpenFile(filePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err == nil {
		logFile = f
	}
}

func write(level string, msg string) {
	mu.Lock()
	defer mu.Unlock()
	now := time.Now()
	fmt.Fprintf(os.Stderr, "%s %-7s | %s\n", now.Format("2006-01-02 15:04:05"), level, msg)
	if logFile != nil {
		fmt.Fprintf(logFile, "%s %s %s\n", now.Format("2006-01-02 15:04:05"), level, msg)
	}
}

func Info(format string, args ...any) {
	write("INFO", fmt.Sprintf(format, args...))
}

func Success(format string, args ...any) {
	write("INFO", fmt.Sprintf(format, args...))
}

func Warning(format string, args ...any) {
	write("WARNING", fmt.Sprintf(format, args...))
}

func Error(format string, args ...any) {
	write("ERROR", fmt.Sprintf(format, args...))
}

// Fatal logs an error and exits with status 1.
func Fatal(format string, args ...any) {
	Error(format, args...)
	os.Exit(1)
}

// ErrorfAlways logs an error without exiting.
func ErrorfAlways(format string, args ...any) {
	Error(format, args...)
}

// Close flushes the log file.
func Close() {
	mu.Lock()
	defer mu.Unlock()
	if logFile != nil {
		_ = logFile.Close()
		logFile = nil
	}
}

// Writer returns a writer that forwards to the logger (useful for libraries).
func Writer() io.Writer {
	return writer{}
}

type writer struct{}

func (writer) Write(p []byte) (int, error) {
	Info("%s", string(p))
	return len(p), nil
}

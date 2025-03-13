package logger

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

const (
	// ANSI color codes
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorBlue   = "\033[34m"
	colorPurple = "\033[35m"
	colorCyan   = "\033[36m"
	colorGray   = "\033[37m"
	colorWhite  = "\033[97m"
)

var (
	// Default logger instance
	Default = NewLogger("console")
)

// Logger represents a structured logger
type Logger struct {
	*slog.Logger
	format string
}

// NewLogger creates a new logger with the specified format
func NewLogger(format string) *Logger {
	var handler slog.Handler
	if format == "json" {
		handler = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.LevelInfo,
		})
	} else {
		handler = &consoleHandler{
			writer: os.Stdout,
		}
	}

	return &Logger{
		Logger: slog.New(handler),
		format: format,
	}
}

type consoleHandler struct {
	writer io.Writer
}

func (h *consoleHandler) Handle(ctx context.Context, r slog.Record) error {
	// Build the message with color-coded level
	level := r.Level.String()
	levelColor := getLevelColor(r.Level)

	// Format: [TIME] LEVEL: message
	msg := fmt.Sprintf("[%s] %s%s%s: %s\n",
		r.Time.Format("2006-01-02 15:04:05"),
		levelColor,
		strings.ToUpper(level),
		colorReset,
		r.Message)

	// Add attributes if any
	if r.NumAttrs() > 0 {
		attrs := make([]string, 0, r.NumAttrs())
		r.Attrs(func(a slog.Attr) bool {
			attrs = append(attrs, fmt.Sprintf("%s=%v", a.Key, a.Value.Any()))
			return true
		})
		msg += "  " + strings.Join(attrs, " ") + "\n"
	}

	_, err := h.writer.Write([]byte(msg))
	return err
}

func (h *consoleHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return h
}

func (h *consoleHandler) WithGroup(name string) slog.Handler {
	return h
}

func (h *consoleHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return true
}

func getLevelColor(level slog.Level) string {
	switch level {
	case slog.LevelDebug:
		return colorGray
	case slog.LevelInfo:
		return colorGreen
	case slog.LevelWarn:
		return colorYellow
	case slog.LevelError:
		return colorRed
	default:
		return colorWhite
	}
}

// Helper methods for structured logging
func (l *Logger) DebugContext(ctx context.Context, msg string, args ...any) {
	l.Logger.DebugContext(ctx, msg, args...)
}

func (l *Logger) InfoContext(ctx context.Context, msg string, args ...any) {
	l.Logger.InfoContext(ctx, msg, args...)
}

func (l *Logger) WarnContext(ctx context.Context, msg string, args ...any) {
	l.Logger.WarnContext(ctx, msg, args...)
}

func (l *Logger) ErrorContext(ctx context.Context, msg string, args ...any) {
	l.Logger.ErrorContext(ctx, msg, args...)
}

// Non-context versions for backward compatibility
func (l *Logger) Debug(msg string, args ...any) {
	l.Logger.Debug(msg, args...)
}

func (l *Logger) Info(msg string, args ...any) {
	l.Logger.Info(msg, args...)
}

func (l *Logger) Warn(msg string, args ...any) {
	l.Logger.Warn(msg, args...)
}

func (l *Logger) Error(msg string, args ...any) {
	l.Logger.Error(msg, args...)
}

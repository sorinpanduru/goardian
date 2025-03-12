package main

import (
	"context"
	"io"
	"log/slog"
	"os"
)

const (
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorReset  = "\033[0m"
)

type colorHandler struct {
	w io.Writer
}

func (h *colorHandler) Handle(ctx context.Context, r slog.Record) error {
	// Format timestamp
	timestamp := r.Time.Format("2006-01-02 15:04:05.000")

	level := r.Level.String()
	switch r.Level {
	case slog.LevelError:
		level = colorRed + "ERROR" + colorReset
	case slog.LevelWarn:
		level = colorYellow + "WARN" + colorReset
	case slog.LevelInfo:
		level = colorGreen + "INFO" + colorReset
	case slog.LevelDebug:
		level = colorReset + "DEBUG" + colorReset
	}

	msg := r.Message
	attrs := make([]string, 0)

	// Add record attributes
	r.Attrs(func(a slog.Attr) bool {
		attrs = append(attrs, a.Key+"="+a.Value.String())
		return true
	})

	// Build the complete log line
	logLine := timestamp + " level=" + level + " msg=\"" + msg + "\""
	if len(attrs) > 0 {
		logLine += " " + attrs[0]
		for _, attr := range attrs[1:] {
			logLine += " " + attr
		}
	}
	logLine += "\n"

	// Write the complete log line
	_, err := io.WriteString(h.w, logLine)
	return err
}

func (h *colorHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return h
}

func (h *colorHandler) WithGroup(name string) slog.Handler {
	return h
}

func (h *colorHandler) Enabled(_ context.Context, level slog.Level) bool {
	return true
}

var logger = slog.New(&colorHandler{w: os.Stdout})

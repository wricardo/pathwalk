package pathwalk

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// LogEntry is a single captured log record, safe for serialisation.
type LogEntry struct {
	Time    time.Time      `json:"time"`
	Level   string         `json:"level"`
	Message string         `json:"message"`
	Attrs   map[string]any `json:"attrs,omitempty"`
}

// logCapture wraps a slog.Handler and captures every record it handles.
// It is safe for concurrent use.
type logCapture struct {
	inner   slog.Handler
	mu      sync.Mutex
	entries []LogEntry
}

func newLogCapture(inner slog.Handler) *logCapture {
	return &logCapture{inner: inner}
}

func (lc *logCapture) Enabled(ctx context.Context, level slog.Level) bool {
	return lc.inner.Enabled(ctx, level)
}

func (lc *logCapture) Handle(ctx context.Context, r slog.Record) error {
	entry := LogEntry{
		Time:    r.Time,
		Level:   r.Level.String(),
		Message: r.Message,
		Attrs:   make(map[string]any),
	}
	r.Attrs(func(a slog.Attr) bool {
		entry.Attrs[a.Key] = a.Value.Any()
		return true
	})
	lc.mu.Lock()
	lc.entries = append(lc.entries, entry)
	lc.mu.Unlock()
	return lc.inner.Handle(ctx, r)
}

func (lc *logCapture) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &logCapture{inner: lc.inner.WithAttrs(attrs)}
}

func (lc *logCapture) WithGroup(name string) slog.Handler {
	return &logCapture{inner: lc.inner.WithGroup(name)}
}

func (lc *logCapture) flush() []LogEntry {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	out := make([]LogEntry, len(lc.entries))
	copy(out, lc.entries)
	return out
}

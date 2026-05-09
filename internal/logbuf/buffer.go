package logbuf

import (
	"context"
	"log/slog"
	"regexp"
	"strings"
	"sync"
)

const bufCap = 5000

// Buffer is a fixed-size ring buffer of formatted log lines that also implements
// slog.Handler. Safe for concurrent use.
type Buffer struct {
	mu    sync.Mutex
	lines []string
	pos   int
	full  bool
}

// New returns a Buffer pre-allocated for bufCap lines.
func New() *Buffer {
	return &Buffer{lines: make([]string, bufCap)}
}

// Lines returns all buffered lines in chronological order.
// The returned slice is a copy — safe to use without holding any lock.
func (b *Buffer) Lines() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.full {
		out := make([]string, b.pos)
		copy(out, b.lines[:b.pos])
		return out
	}
	out := make([]string, bufCap)
	copy(out, b.lines[b.pos:])
	copy(out[bufCap-b.pos:], b.lines[:b.pos])
	return out
}

// Enabled always returns true so the buffer captures all log levels.
func (b *Buffer) Enabled(_ context.Context, _ slog.Level) bool { return true }

// Handle formats r as a text log line and appends it to the ring buffer.
func (b *Buffer) Handle(ctx context.Context, r slog.Record) error {
	var sb strings.Builder
	if err := slog.NewTextHandler(&sb, nil).Handle(ctx, r); err != nil {
		return err
	}
	line := strings.TrimSuffix(sb.String(), "\n")
	b.mu.Lock()
	b.lines[b.pos] = line
	b.pos = (b.pos + 1) % bufCap
	if !b.full && b.pos == 0 {
		b.full = true
	}
	b.mu.Unlock()
	return nil
}

// WithAttrs returns b unchanged; slog resolves attrs into the Record before Handle is called.
func (b *Buffer) WithAttrs(_ []slog.Attr) slog.Handler { return b }

// WithGroup returns b unchanged; group nesting is not tracked in the ring buffer.
func (b *Buffer) WithGroup(_ string) slog.Handler { return b }

// fanHandler dispatches each log record to all contained handlers.
type fanHandler struct {
	handlers []slog.Handler
}

// NewFanHandler returns a slog.Handler that writes to all provided handlers.
func NewFanHandler(handlers ...slog.Handler) slog.Handler {
	return &fanHandler{handlers: handlers}
}

func (f *fanHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, h := range f.handlers {
		if h.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (f *fanHandler) Handle(ctx context.Context, r slog.Record) error {
	var last error
	for _, h := range f.handlers {
		if h.Enabled(ctx, r.Level) {
			if err := h.Handle(ctx, r.Clone()); err != nil {
				last = err
			}
		}
	}
	return last
}

func (f *fanHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	hs := make([]slog.Handler, len(f.handlers))
	for i, h := range f.handlers {
		hs[i] = h.WithAttrs(attrs)
	}
	return &fanHandler{handlers: hs}
}

func (f *fanHandler) WithGroup(name string) slog.Handler {
	hs := make([]slog.Handler, len(f.handlers))
	for i, h := range f.handlers {
		hs[i] = h.WithGroup(name)
	}
	return &fanHandler{handlers: hs}
}

// Sanitize scrubs sensitive data from a log line before it is served for download:
//   - Xtream stream URL credentials: /movie/user/pass/ → /movie/<redacted>/<redacted>/
//   - Xtream query-string credentials: username=... → username=<redacted>
//   - IPv4 addresses
var (
	reXtreamPath  = regexp.MustCompile(`(?i)/(movie|series)/[^/\s]+/[^/\s]+/`)
	reXtreamQuery = regexp.MustCompile(`(?i)(username|password)=[^&\s"]+`)
	reIPv4        = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)
)

func Sanitize(line string) string {
	line = reXtreamPath.ReplaceAllString(line, "/$1/<redacted>/<redacted>/")
	line = reXtreamQuery.ReplaceAllString(line, "$1=<redacted>")
	line = reIPv4.ReplaceAllString(line, "<ip>")
	return line
}

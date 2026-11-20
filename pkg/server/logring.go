package server

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
)

// LogRing keeps the most recent log lines in memory for the admin overview
// (spec server-runtime / 最近日志缓冲). It formats with the same LogHandler as
// the terminal, without colour, so the console shows exactly what an operator
// would have seen. The logs it holds are the server's normal logs, which by
// spec never contain passwords, tokens, or message bodies.
type LogRing struct {
	mu    sync.Mutex
	lines []string
	next  int
	full  bool
}

// LogRingSize is how many lines the ring keeps.
const LogRingSize = 500

// NewLogRing returns an empty ring.
func NewLogRing() *LogRing { return &LogRing{lines: make([]string, LogRingSize)} }

// Write receives formatted output, one or more whole lines.
func (r *LogRing) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, line := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
		if line == "" {
			continue
		}
		r.lines[r.next] = line
		r.next = (r.next + 1) % len(r.lines)
		if r.next == 0 {
			r.full = true
		}
	}
	return len(p), nil
}

// Lines returns the kept lines, oldest first.
func (r *LogRing) Lines() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.full {
		return append([]string(nil), r.lines[:r.next]...)
	}
	out := make([]string, 0, len(r.lines))
	out = append(out, r.lines[r.next:]...)
	return append(out, r.lines[:r.next]...)
}

// teeHandler passes every record to two handlers.
type teeHandler struct{ a, b slog.Handler }

// NewTeeHandler sends records to both a and b, each applying its own level.
func NewTeeHandler(a, b slog.Handler) slog.Handler { return teeHandler{a, b} }

func (t teeHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return t.a.Enabled(ctx, l) || t.b.Enabled(ctx, l)
}

func (t teeHandler) Handle(ctx context.Context, rec slog.Record) error {
	var err error
	if t.a.Enabled(ctx, rec.Level) {
		err = t.a.Handle(ctx, rec.Clone())
	}
	if t.b.Enabled(ctx, rec.Level) {
		if e := t.b.Handle(ctx, rec.Clone()); err == nil {
			err = e
		}
	}
	return err
}

func (t teeHandler) WithAttrs(as []slog.Attr) slog.Handler {
	return teeHandler{t.a.WithAttrs(as), t.b.WithAttrs(as)}
}

func (t teeHandler) WithGroup(name string) slog.Handler {
	return teeHandler{t.a.WithGroup(name), t.b.WithGroup(name)}
}

// SetLogRing makes the server serve ring's lines on the admin overview.
func (s *Server) SetLogRing(r *LogRing) { s.logRing = r }

// newTeeLogger builds the logger main uses: the terminal format to w and the
// same format, uncoloured, to ring. Shared with tests so they check the real
// assembly.
func newTeeLogger(w io.Writer, ring *LogRing) *slog.Logger {
	return slog.New(NewTeeHandler(NewLogHandler(w, slog.LevelInfo, false), NewLogHandler(ring, slog.LevelInfo, false)))
}

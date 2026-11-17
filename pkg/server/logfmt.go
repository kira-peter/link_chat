package server

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
)

// Log output: one short line per event,
//
//	15:04:05 INF login ok user=alice addr=1.2.3.4:5678
//
// The level is a three-letter tag. Warnings are yellow and errors red when the
// output is a terminal; piped or redirected output, or NO_COLOR, stays plain.

const (
	ansiReset  = "\x1b[0m"
	ansiRed    = "\x1b[31m"
	ansiYellow = "\x1b[33m"
	ansiGreen  = "\x1b[32m"
	ansiGray   = "\x1b[90m"
)

// LogHandler is a compact, levelled slog handler.
type LogHandler struct {
	mu    *sync.Mutex
	w     io.Writer
	level slog.Leveler
	color bool
	attrs []slog.Attr
}

// NewLogHandler writes to w at or above level, in colour when color is true.
func NewLogHandler(w io.Writer, level slog.Leveler, color bool) *LogHandler {
	return &LogHandler{mu: &sync.Mutex{}, w: w, level: level, color: color}
}

// ColorWanted reports whether colour suits f: a terminal, and NO_COLOR unset.
func ColorWanted(f *os.File) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func (h *LogHandler) Enabled(_ context.Context, l slog.Level) bool {
	return l >= h.level.Level()
}

func (h *LogHandler) WithAttrs(as []slog.Attr) slog.Handler {
	n := *h
	n.attrs = append(append([]slog.Attr{}, h.attrs...), as...)
	return &n
}

// WithGroup is not used by the server; groups are flattened.
func (h *LogHandler) WithGroup(string) slog.Handler { return h }

func (h *LogHandler) paint(col, s string) string {
	if !h.color || col == "" {
		return s
	}
	return col + s + ansiReset
}

func (h *LogHandler) Handle(_ context.Context, r slog.Record) error {
	tag, col := "DBG", ansiGray
	switch {
	case r.Level >= slog.LevelError:
		tag, col = "ERR", ansiRed
	case r.Level >= slog.LevelWarn:
		tag, col = "WRN", ansiYellow
	case r.Level >= slog.LevelInfo:
		tag, col = "INF", ansiGreen
	}

	var b strings.Builder
	b.WriteString(h.paint(ansiGray, r.Time.Format("15:04:05")))
	b.WriteByte(' ')
	b.WriteString(h.paint(col, tag))
	b.WriteByte(' ')
	msg := r.Message
	if r.Level >= slog.LevelWarn {
		msg = h.paint(col, msg)
	}
	b.WriteString(msg)

	add := func(a slog.Attr) {
		a.Value = a.Value.Resolve()
		if a.Equal(slog.Attr{}) {
			return
		}
		v := a.Value.String()
		if v == "" || strings.ContainsAny(v, " \t\n\"=") {
			v = strconv.Quote(v)
		}
		b.WriteByte(' ')
		b.WriteString(h.paint(ansiGray, a.Key+"="))
		b.WriteString(v)
	}
	for _, a := range h.attrs {
		add(a)
	}
	r.Attrs(func(a slog.Attr) bool {
		add(a)
		return true
	})
	b.WriteByte('\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := io.WriteString(h.w, b.String())
	return err
}

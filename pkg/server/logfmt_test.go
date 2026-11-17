package server

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestLogLineIsShortAndLevelled(t *testing.T) {
	var b bytes.Buffer
	log := slog.New(NewLogHandler(&b, slog.LevelInfo, false))
	log.Debug("hidden")
	log.Info("login ok", "user", "alice", "addr", "1.2.3.4:5")
	log.Warn("settings rejected", "err", `bad "x"`)
	log.Error("storage error", "err", "disk full")

	lines := strings.Split(strings.TrimSpace(b.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3 (debug filtered out):\n%s", len(lines), b.String())
	}
	want := []string{
		" INF login ok user=alice addr=1.2.3.4:5",
		` WRN settings rejected err="bad \"x\""`,
		` ERR storage error err="disk full"`,
	}
	for i, w := range want {
		// Each line is "HH:MM:SS" then the rest.
		if len(lines[i]) < 8 || lines[i][8:] != w {
			t.Errorf("line %d = %q, want time then %q", i, lines[i], w)
		}
	}
	if strings.Contains(b.String(), "\x1b[") {
		t.Error("colour codes written with colour off")
	}
}

func TestLogColoursWarningsAndErrors(t *testing.T) {
	var b bytes.Buffer
	log := slog.New(NewLogHandler(&b, slog.LevelInfo, true))
	log.Warn("w")
	log.Error("e")
	out := b.String()
	if !strings.Contains(out, ansiYellow+"WRN") || !strings.Contains(out, ansiYellow+"w"+ansiReset) {
		t.Errorf("the warning is not yellow: %q", out)
	}
	if !strings.Contains(out, ansiRed+"ERR") || !strings.Contains(out, ansiRed+"e"+ansiReset) {
		t.Errorf("the error is not red: %q", out)
	}
}

func TestLogWithAttrsCarriesThemOn(t *testing.T) {
	var b bytes.Buffer
	log := slog.New(NewLogHandler(&b, slog.LevelInfo, false)).With("conn", 7)
	log.Info("x", "y", 1)
	if !strings.HasSuffix(strings.TrimSpace(b.String()), "INF x conn=7 y=1") {
		t.Fatalf("got %q", b.String())
	}
}

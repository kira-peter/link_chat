package app

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"link-chat/pkg/tui/views"
)

// The stage's dots are decoration, so on the real main loop they keep a slow
// clock: about ten frames a second. The 250ms housekeeping ticker also wakes the
// loop (up to four times a second), so the allowance is the spec's 12fps for the
// dots plus that ticker.
func TestStageRunsOnItsSlowClock(t *testing.T) {
	scr := newTestScreen(t, 100, 30)
	// No server: the stage is what is on screen before any connection is made.
	a := New(scr, Config{Server: "ws://127.0.0.1:1/ws", Theme: "ink", Language: "zh"}, filepath.Join(t.TempDir(), "s.json"))

	var frames atomic.Int64
	a.onFrame = func() { frames.Add(1) }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.Run(ctx)

	// Keep the button sweep out of it: with focus on the username field only the
	// dots are moving.
	a.mu.Lock()
	a.ui.Dialog = views.DialogLogin
	a.ui.Login.Focus = views.LoginFocusUsername
	a.mu.Unlock()
	a.markDirty()
	time.Sleep(300 * time.Millisecond)

	before := frames.Load()
	time.Sleep(2 * time.Second)
	n := frames.Load() - before
	perSec := float64(n) / 2
	t.Logf("stage only: %d frames in 2s (%.1f/s)", n, perSec)
	if perSec > 12+4 {
		t.Fatalf("the stage draws %.1f frames a second, over its slow clock", perSec)
	}
	if perSec < 6 {
		t.Fatalf("the stage draws only %.1f frames a second: the dots would stutter", perSec)
	}

	// With animation off the stage is still: only the housekeeping ticker wakes it.
	a.mu.Lock()
	a.st.MotionOff = true
	a.mu.Unlock()
	a.markDirty()
	time.Sleep(300 * time.Millisecond)
	before = frames.Load()
	time.Sleep(2 * time.Second)
	if off := frames.Load() - before; off > 12 {
		t.Fatalf("with animation off the stage still drew %d frames in 2s", off)
	}
}

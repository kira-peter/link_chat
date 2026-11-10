package app

import (
	"context"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"link-chat/pkg/client"
	"link-chat/pkg/tui/views"
)

// registerForTest creates an account and signs in, the way a new user would.
func registerForTest(t *testing.T, a *App, ctx context.Context, name string) {
	t.Helper()
	a.mu.Lock()
	a.ui.Register = views.RegisterModel{
		Username: name, Password: "correct horse battery",
		Question: "Q?", Answer: "a",
	}
	a.mu.Unlock()
	a.submitRegister(ctx)
}

// The whole animation budget, measured on the real main loop: a resting screen
// must draw (almost) nothing, and an animating one must stay within the 30fps cap.
func TestMainLoopFrameBudget(t *testing.T) {
	hs := startServer(t)
	url := "ws" + strings.TrimPrefix(hs.URL, "http") + "/ws"

	scr := newTestScreen(t, 100, 30)
	a := New(scr, Config{Server: url, Theme: "ink", Language: "zh"}, filepath.Join(t.TempDir(), "b.json"))

	var frames atomic.Int64
	a.onFrame = func() { frames.Add(1) }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.Run(ctx)

	// Sign in so the composer is live and the hint can shimmer.
	registerForTest(t, a, ctx, "budget")
	waitFor(t, 15*time.Second, "ready", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.st.State == client.StateReady
	})
	time.Sleep(300 * time.Millisecond) // let startup traffic settle

	// 1. Shimmering: the empty composer has the keyboard, so frames are expected,
	//    but never faster than the cap.
	a.mu.Lock()
	a.st.Focus = client.FocusInput
	a.st.RestoreInput("")
	a.mu.Unlock()
	a.markDirty()

	before := frames.Load()
	time.Sleep(2 * time.Second)
	shimmerFrames := frames.Load() - before
	t.Logf("shimmering: %d frames in 2s", shimmerFrames)

	perSec := float64(shimmerFrames) / 2.0
	if perSec > 36 { // 30fps cap plus slack for the 250ms ticker and events
		t.Fatalf("animating at %.0f fps, over the 30fps cap", perSec)
	}
	if perSec < 8 {
		t.Fatalf("animating at only %.0f fps: the shimmer would look like a slideshow", perSec)
	}

	// 2. At rest: move focus to the list. No shimmer, no slide once it settles.
	a.mu.Lock()
	a.moveFocus(client.FocusList)
	a.mu.Unlock()
	a.markDirty()
	time.Sleep(600 * time.Millisecond) // let the 180ms slide finish and settle

	before = frames.Load()
	time.Sleep(2 * time.Second)
	restFrames := frames.Load() - before
	t.Logf("at rest: %d frames in 2s", restFrames)

	// The only legitimate wakeup at rest is the 250ms housekeeping ticker (8 in 2s).
	if restFrames > 12 {
		t.Fatalf("a resting screen drew %d frames in 2s: animation is leaking into idle", restFrames)
	}
}

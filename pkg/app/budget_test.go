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

	// 2. The list has the keyboard: its selected row now sweeps, so frames are
	//    expected again — and, as with the composer, never faster than the cap.
	a.mu.Lock()
	a.moveFocus(client.FocusList)
	a.mu.Unlock()
	a.markDirty()
	time.Sleep(600 * time.Millisecond) // let the 180ms slide finish and settle

	before = frames.Load()
	time.Sleep(2 * time.Second)
	listFrames := frames.Load() - before
	t.Logf("list sweeping: %d frames in 2s", listFrames)

	listPerSec := float64(listFrames) / 2.0
	if listPerSec > 36 {
		t.Fatalf("the list sweep runs at %.0f fps, over the 30fps cap", listPerSec)
	}
	if listPerSec < 8 {
		t.Fatalf("the list sweep runs at only %.0f fps: it would look like a slideshow", listPerSec)
	}

	// 3. At rest: nothing is animating. The composer holds a draft (no hint sweep)
	//    and animation is the only thing that could still be drawing, so this is
	//    the state the "static screen costs nothing" rule is really about.
	a.mu.Lock()
	a.st.Focus = client.FocusInput
	a.st.RestoreInput("a draft, so the empty-composer hint is not sweeping")
	a.mu.Unlock()
	a.markDirty()
	time.Sleep(600 * time.Millisecond) // let the slide finish and settle

	before = frames.Load()
	time.Sleep(2 * time.Second)
	restFrames := frames.Load() - before
	t.Logf("at rest: %d frames in 2s", restFrames)

	// The only legitimate wakeup at rest is the 250ms housekeeping ticker (8 in 2s).
	if restFrames > 12 {
		t.Fatalf("a resting screen drew %d frames in 2s: animation is leaking into idle", restFrames)
	}

	// 4. Switching animation off silences the list sweep too: one switch controls
	//    every animation.
	a.mu.Lock()
	a.moveFocus(client.FocusList)
	a.st.MotionOff = true
	a.mu.Unlock()
	a.markDirty()
	time.Sleep(600 * time.Millisecond)

	before = frames.Load()
	time.Sleep(2 * time.Second)
	offFrames := frames.Load() - before
	t.Logf("list focused, animation off: %d frames in 2s", offFrames)
	if offFrames > 12 {
		t.Fatalf("with animation off the list still drew %d frames in 2s", offFrames)
	}
}

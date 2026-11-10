package app

import (
	"context"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/client"
	"link-chat/pkg/tui/views"
)

// ---------- 4.8 the sidebar sweep in the frame budget ----------

// settled returns an app on the main interface with the keyboard on the list, the
// focus marker at rest, and the clock frozen, so only the list sweep is under test.
func settled(t *testing.T) (*App, *time.Time) {
	t.Helper()
	a, _ := newSidebarApp(t)
	now := freezeClock(t, time.Unix(9000, 0))
	a.st.State = client.StateReady
	a.st.Focus = client.FocusList
	a.ui.Dialog = views.DialogNone
	a.st.MotionOff = false
	a.anim.slide.MoveTo(a.regionX(client.FocusList), *now, focusSlideDuration, false)
	return a, now
}

// The sweep asks for frames only while the list is on screen with the keyboard and
// a row to light; in every other state the screen is still and costs nothing.
func TestNeedsFramesForTheSidebarSweep(t *testing.T) {
	cases := []struct {
		name  string
		setup func(a *App)
		want  bool
	}{
		{"list focused, animation on", func(a *App) {}, true},
		{"focus in the composer stops it", func(a *App) {
			a.st.Focus = client.FocusInput
			// An empty composer runs its own hint sweep, which is a different
			// animation; give it a draft so only the list's is being measured.
			a.st.RestoreInput("draft")
		}, false},
		{"animation off stops it", func(a *App) { a.st.MotionOff = true }, false},
		{"a dialog over the list stops it", func(a *App) { a.ui.Dialog = views.DialogHelp }, false},
		{"the about dialog stops it", func(a *App) { a.ui.Dialog = views.DialogAbout }, false},
		{"offline stops it", func(a *App) { a.st.State = client.StateDisconnected }, false},
		{"cursor on a foot entry still runs", func(a *App) {
			for i := 0; i < 4; i++ {
				a.handleKey(context.Background(), specialKey(tcell.KeyDown))
			}
		}, true},
	}
	for _, c := range cases {
		a, now := settled(t)
		c.setup(a)
		if got := a.needsFrames(*now); got != c.want {
			t.Errorf("%s: needsFrames = %v, want %v", c.name, got, c.want)
		}
	}
}

// Once focus leaves the list, no further frames are requested on its account.
func TestSidebarSweepStopsWhenFocusLeaves(t *testing.T) {
	a, now := settled(t)
	if !a.needsFrames(*now) {
		t.Fatal("precondition: the sweep should be running")
	}
	a.st.Focus = client.FocusInput
	a.anim.slide.MoveTo(a.regionX(client.FocusInput), *now, focusSlideDuration, false)
	// The composer has its own hint sweep, so isolate the list's by emptying and
	// un-readying the composer's conditions.
	a.st.RestoreInput("typing")
	if a.needsFrames(*now) {
		t.Fatal("frames are still being requested after focus left the list")
	}
}

// The render path is driven by the same predicate: the model asks for a sweep
// exactly when needsFrames does, so what is drawn and what is scheduled agree.
func TestListModelShimmerMatchesTheBudget(t *testing.T) {
	a, now := settled(t)
	if !a.listModel(client.FocusList).Shimmer {
		t.Fatal("a focused list is scheduled for frames but is not drawn with the band")
	}
	a.st.MotionOff = true
	if a.listModel(client.FocusList).Shimmer {
		t.Fatal("the band is drawn with animation off")
	}
	_ = now
}

// Moving the cursor restarts the band from the left, so a newly selected row is
// entered from its start rather than caught mid-pass.
func TestCursorMoveRestartsTheSweep(t *testing.T) {
	a, now := settled(t)
	a.anim.listStart = now.Add(-time.Hour)

	*now = now.Add(5 * time.Second)
	a.handleKey(context.Background(), specialKey(tcell.KeyDown))
	if !a.anim.listStart.Equal(*now) {
		t.Fatalf("the sweep did not restart on a new row: started %v, now %v", a.anim.listStart, *now)
	}

	// Pressing down at the very end moves nothing, so it must not restart it.
	for i := 0; i < 10; i++ {
		a.handleKey(context.Background(), specialKey(tcell.KeyDown))
	}
	*now = now.Add(5 * time.Second)
	before := a.anim.listStart
	a.handleKey(context.Background(), specialKey(tcell.KeyDown))
	if !a.anim.listStart.Equal(before) {
		t.Fatal("a no-op key press at the end of the list restarted the sweep")
	}
}

// Coming back to the list from the composer restarts the band too.
func TestRegainingListFocusRestartsTheSweep(t *testing.T) {
	a, now := settled(t)
	a.st.Focus = client.FocusInput
	a.anim.listStart = now.Add(-time.Hour)

	*now = now.Add(3 * time.Second)
	a.moveFocus(client.FocusList)
	if !a.anim.listStart.Equal(*now) {
		t.Fatalf("regaining focus did not restart the band: %v vs %v", a.anim.listStart, *now)
	}
}

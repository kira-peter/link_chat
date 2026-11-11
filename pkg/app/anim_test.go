package app

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/client"
	"link-chat/pkg/i18n"
	"link-chat/pkg/tui/layout"
	"link-chat/pkg/tui/views"
)

// freezeClock pins the animation clock so a test can assert exactly what is on
// screen at a chosen moment, instead of racing a real timer.
func freezeClock(t *testing.T, at time.Time) *time.Time {
	t.Helper()
	cur := at
	prev := nowTime
	nowTime = func() time.Time { return cur }
	t.Cleanup(func() { nowTime = prev })
	return &cur
}

// ---------- the animation budget ----------

// A resting interface must cost nothing: no timer, no repainting. This is the
// guarantee that keeps the client usable over SSH (design.md decision 4).
func TestNeedsFramesIsFalseAtRest(t *testing.T) {
	a, _ := newKeyTestApp(t)
	now := freezeClock(t, time.Unix(5000, 0))

	// Focus on the list, so no shimmer; no slide in flight.
	a.st.Focus = client.FocusList
	if a.needsFrames(*now) {
		t.Fatal("a resting interface asked for animation frames")
	}
}

// The hint sweep runs only while the empty composer has the keyboard.
func TestNeedsFramesForShimmerConditions(t *testing.T) {
	a, _ := newKeyTestApp(t)
	now := freezeClock(t, time.Unix(5000, 0))
	// A fresh app's marker is already resting on the focused region, so no slide
	// is in flight and only the shimmer conditions are being exercised.

	cases := []struct {
		name  string
		setup func()
		want  bool
	}{
		{"empty focused composer shimmers", func() {}, true},
		{"typing stops it", func() { a.st.RestoreInput("x") }, false},
		{"list focus stops it", func() { a.st.Focus = client.FocusList }, false},
		{"a dialog stops it", func() { a.ui.Dialog = views.DialogHelp }, false},
		{"offline stops it", func() { a.st.State = client.StateDisconnected }, false},
		{"animation off stops it", func() { a.st.MotionOff = true }, false},
	}
	for _, c := range cases {
		// Reset to the shimmering baseline each time.
		a.st.Focus = client.FocusInput
		a.st.RestoreInput("")
		a.ui.Dialog = views.DialogNone
		a.st.State = client.StateReady
		a.st.MotionOff = false

		c.setup()
		if got := a.needsFrames(*now); got != c.want {
			t.Errorf("%s: needsFrames = %v, want %v", c.name, got, c.want)
		}
	}
}

// ---------- focus transitions ----------

// A focus change starts a slide that settles within the time budget, then stops
// asking for frames.
func TestFocusTransitionSettlesAndStopsAnimating(t *testing.T) {
	a, ctx := newKeyTestApp(t)
	cur := freezeClock(t, time.Unix(5000, 0))
	a.st.Focus = client.FocusInput
	a.anim.slide.MoveTo(a.regionX(client.FocusInput), *cur, focusSlideDuration, false)

	a.handleKey(ctx, specialKey(tcell.KeyLeft)) // → list

	if a.anim.slide.Done(*cur) {
		t.Fatal("transition did not start")
	}
	if !a.needsFrames(*cur) {
		t.Fatal("an in-flight slide must request frames")
	}

	// Within the 300ms ceiling the spec allows, it must be finished.
	end := cur.Add(300 * time.Millisecond)
	if !a.anim.slide.Done(end) {
		t.Fatal("slide still running after 300ms")
	}
	if got, want := a.anim.slide.At(end), a.regionX(client.FocusList); got != want {
		t.Fatalf("marker settled at %v, want the list's edge %v", got, want)
	}
}

// With animation off, the marker lands instantly with no in-between frames.
func TestFocusTransitionIsInstantWhenAnimationIsOff(t *testing.T) {
	a, ctx := newKeyTestApp(t)
	cur := freezeClock(t, time.Unix(5000, 0))
	a.st.MotionOff = true
	a.st.Focus = client.FocusInput

	a.handleKey(ctx, specialKey(tcell.KeyLeft))

	if !a.anim.slide.Done(*cur) {
		t.Fatal("a transition ran with animation switched off")
	}
	if got, want := a.anim.slide.At(*cur), a.regionX(client.FocusList); got != want {
		t.Fatalf("marker at %v, want %v immediately", got, want)
	}
	if a.needsFrames(*cur) {
		t.Fatal("requested frames with animation off")
	}
}

// Pressing keys mid-transition must keep the marker where it is and turn it
// around, not snap it back to an old start.
func TestFocusRetargetContinuesFromCurrentPosition(t *testing.T) {
	a, ctx := newOpenKeyTestApp(t)
	cur := freezeClock(t, time.Unix(5000, 0))
	a.st.Focus = client.FocusInput
	a.anim.slide.MoveTo(a.regionX(client.FocusInput), *cur, focusSlideDuration, false)

	a.handleKey(ctx, specialKey(tcell.KeyLeft)) // heading to the list
	*cur = cur.Add(60 * time.Millisecond)
	mid := a.anim.slide.At(*cur)

	a.handleKey(ctx, specialKey(tcell.KeyRight)) // change of mind
	if got := a.anim.slide.At(*cur); got != mid {
		t.Fatalf("retarget jumped from %v to %v", mid, got)
	}
	if a.anim.slide.Target() != a.regionX(client.FocusInput) {
		t.Fatal("slide is not heading back to the composer")
	}
}

// Input must never wait for an animation: the key takes effect the instant it is
// pressed, even though the marker is still catching up.
func TestKeysAreNotBlockedByTransition(t *testing.T) {
	a, ctx := newKeyTestApp(t)
	freezeClock(t, time.Unix(5000, 0))
	a.st.Focus = client.FocusInput

	a.handleKey(ctx, specialKey(tcell.KeyLeft)) // starts a slide
	a.st.Focus = client.FocusList               // (we are now in the list)
	a.handleKey(ctx, key('x'))                  // a key while the slide is in flight

	// In the list, 'x' is not a command, so nothing should have been typed, and the
	// key must at least have been processed without waiting.
	if a.st.InputText() != "" {
		t.Fatalf("a letter reached the composer while the list had focus: %q", a.st.InputText())
	}
}

// ---------- rendering ----------

// rowString reads a screen row back as text, treating a wide rune's second cell as
// part of it.
func rowString(a *App, y int) string {
	a.scr.Show()
	var b strings.Builder
	w, _ := a.scr.Size()
	skip := 0
	for x := 0; x < w; x++ {
		if skip > 0 {
			skip--
			continue
		}
		r, _, _, width := a.scr.GetContent(x, y)
		if r == 0 {
			r = ' '
		}
		b.WriteRune(r)
		if width == 2 {
			skip = 1
		}
	}
	return b.String()
}

// The focus bar must appear on the focused region's title row and move with the
// focus. It is a glyph, so it survives on a monochrome terminal.
func TestFocusMarkerIsDrawnAndFollowsFocus(t *testing.T) {
	a, _ := newKeyTestApp(t)
	freezeClock(t, time.Unix(5000, 0))
	a.st.MotionOff = true // instant, so the marker is exactly at its target

	// Give the screen a real layout to draw into, opening the conversation the way
	// the app does: that is what re-aims the marker at the title's new position.
	cv := a.st.EnsureConversation("c1")
	cv.Name = "general"
	a.openConversation("c1")

	for _, c := range []struct {
		focus client.Focus
		name  string
	}{
		{client.FocusInput, "composer"},
		{client.FocusList, "list"},
	} {
		a.moveFocus(c.focus)
		a.render()

		// The marker row is the first row of the content area.
		w, h := a.scr.Size()
		y := views.ContentRect(w, h).Y
		row := rowString(a, y)
		if !strings.ContainsRune(row, views.FocusMarkerRune) {
			t.Fatalf("%s focus: no marker on the title row: %q", c.name, row)
		}

		// And it sits at the focused region's edge, not the other one's.
		idx := strings.IndexRune(row, views.FocusMarkerRune)
		want := int(a.regionX(c.focus)+0.5) - 1
		if want < 0 {
			want = 0
		}
		if got := layout.StringWidth(row[:idx]); got != want {
			t.Fatalf("%s focus: marker at column %d, want %d", c.name, got, want)
		}
	}
}

// The empty composer shows the long hint, and typing replaces it in the same
// frame.
func TestComposerHintShowsAndClears(t *testing.T) {
	a, _ := newKeyTestApp(t)
	freezeClock(t, time.Unix(5000, 0))
	a.st.Focus = client.FocusInput
	cv := a.st.EnsureConversation("c1")
	cv.Name = "general"
	a.st.ActiveID = "c1"

	hint := i18n.ComposeHint.Get(a.lang)

	whole := func() string {
		a.render()
		_, h := a.scr.Size()
		var b strings.Builder
		for y := 0; y < h; y++ {
			b.WriteString(rowString(a, y))
			b.WriteByte('\n')
		}
		return b.String()
	}

	if !strings.Contains(whole(), hint) {
		t.Fatalf("empty composer does not show its hint %q", hint)
	}

	a.st.RestoreInput("h")
	if strings.Contains(whole(), hint) {
		t.Fatal("hint still visible after typing")
	}

	a.st.RestoreInput("")
	if !strings.Contains(whole(), hint) {
		t.Fatal("hint did not return after clearing the draft")
	}
}

// Both languages must be long enough for the band to read as travelling. A shorter
// rewording would quietly turn the sweep into a flash.
func TestComposeHintIsLongEnoughToSweep(t *testing.T) {
	for _, lang := range []i18n.Lang{i18n.ZH, i18n.EN} {
		hint := i18n.ComposeHint.Get(lang)
		if got := layout.StringWidth(hint); got < i18n.ComposeHintMinCells {
			t.Errorf("%s hint %q is %d cells, want at least %d",
				lang, hint, got, i18n.ComposeHintMinCells)
		}
	}
	if got := i18n.ComposeHint.Get(i18n.EN); got != "type in a message" {
		t.Errorf("English hint = %q, want the requested wording", got)
	}
}

// Switching animation off mid-slide must settle the marker at once. Leaving it
// stranded half-way between the two regions would be worse than no animation.
func TestTurningMotionOffSettlesARunningSlide(t *testing.T) {
	a, ctx := newKeyTestApp(t)
	cur := freezeClock(t, time.Unix(5000, 0))
	a.st.Focus = client.FocusInput
	a.anim.slide.MoveTo(a.regionX(client.FocusInput), *cur, focusSlideDuration, false)

	a.handleKey(ctx, specialKey(tcell.KeyLeft)) // slide towards the list begins
	*cur = cur.Add(40 * time.Millisecond)
	if a.anim.slide.Done(*cur) {
		t.Fatal("test setup: the slide should still be running")
	}

	a.toggleMotion() // user turns animation off

	if !a.st.MotionOff {
		t.Fatal("toggle did not switch animation off")
	}
	if !a.anim.slide.Done(*cur) {
		t.Fatal("a slide was left running after animation was switched off")
	}
	if got, want := a.anim.slide.At(*cur), a.regionX(client.FocusList); got != want {
		t.Fatalf("marker left at %v, want settled on the list at %v", got, want)
	}
	if a.needsFrames(*cur) {
		t.Fatal("frames still requested with animation off")
	}
}

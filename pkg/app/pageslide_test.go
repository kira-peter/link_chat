package app

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/tui/views"
)

// mainText is the text of the page area beside the open list.
func mainText(a *App) string {
	scr := a.scr.(tcell.SimulationScreen)
	r := a.pageSlideRect()
	var b strings.Builder
	for y := r.Y; y < r.Y+r.H; y++ {
		for x := r.X; x < r.X+r.W; x++ {
			c, _, _, w := scr.GetContent(x, y)
			b.WriteRune(c)
			if w == 2 {
				x++ // the second cell of a wide character
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// Moving the cursor slides the pages: half-way the old page and the new one are
// both on screen, the old one moving out at the top; at the end only the new one
// is, and the frame budget goes quiet again.
func TestMovingTheCursorSlidesThePage(t *testing.T) {
	a, now := pagesApp(t)
	down(a) // the new-channel page
	advance(now, time.Second)
	a.render()
	if !strings.Contains(mainText(a), "建一个频道") {
		t.Fatalf("the new-channel page is not showing:\n%s", mainText(a))
	}

	down(a) // general
	if !a.pageSliding(*now) || !a.needsFrames(*now) {
		t.Fatal("moving the cursor started no slide")
	}
	advance(now, 20*time.Millisecond)
	a.render()
	mid := mainText(a)
	if !strings.Contains(mid, "建一个频道") {
		t.Fatalf("early in the slide the old page is gone:\n%s", mid)
	}
	// The old page has moved up: its blurb is higher than at rest.
	r := a.pageSlideRect()
	if y := strings.Index(mid, "建一个频道"); y < 0 || strings.Count(mid[:y], "\n") >= r.H/2-3 {
		t.Fatalf("the old page did not move up:\n%s", mid)
	}

	advance(now, pageSlideDuration)
	a.render()
	if strings.Contains(mainText(a), "建一个频道") {
		t.Fatalf("after the slide the old page is still showing:\n%s", mainText(a))
	}
	if a.pageSliding(*now) {
		t.Fatal("the slide did not end")
	}
}

// Moving up slides the other way: the old page leaves at the bottom.
func TestMovingUpSlidesDown(t *testing.T) {
	a, now := pagesApp(t)
	down(a)
	down(a) // general
	advance(now, time.Second)
	a.render()
	a.handleKey(nil, specialKey(tcell.KeyUp)) // the new-channel page
	if a.anim.page == nil || a.anim.page.down {
		t.Fatal("moving up did not start a downward slide")
	}
	advance(now, pageSlideDuration/4)
	a.render()
	mid := mainText(a)
	r := a.pageSlideRect()
	if y := strings.Index(mid, "建一个频道"); y >= 0 && strings.Count(mid[:y], "\n") >= r.H/2-3 {
		t.Fatalf("the new page came in from below, not from above:\n%s", mid)
	}
}

// With animation off the page simply changes.
func TestNoSlideWithAnimationOff(t *testing.T) {
	a, now := pagesApp(t)
	a.st.MotionOff = true
	a.render()
	down(a)
	if a.anim.page != nil || a.pageSliding(*now) {
		t.Fatal("a slide started with animation off")
	}
	a.render()
	if !strings.Contains(mainText(a), "建一个频道") {
		t.Fatalf("the new page is not showing at once:\n%s", mainText(a))
	}
}

// Entering a page folds the list instead: no slide runs over the fold.
func TestEnteringDoesNotSlide(t *testing.T) {
	a, now := pagesApp(t)
	down(a)
	advance(now, time.Second)
	a.render()
	a.handleKey(nil, specialKey(tcell.KeyRight))
	if a.pageSliding(*now) {
		t.Fatal("entering a page started a slide")
	}
	if a.ui.Dialog != views.DialogNone {
		t.Fatal("entering the new page raised a dialog")
	}
}

package app

import (
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/client"
	"link-chat/pkg/tui/layout"
	"link-chat/pkg/tui/views"
)

// markerColumn finds the focus marker on the first content row, or -1.
func markerColumn(a *App) int {
	s := a.scr.(tcell.SimulationScreen)
	s.Show()
	w, h := s.Size()
	y := views.ContentRect(w, h).Y
	for x := 0; x < w; x++ {
		if r, _, _, _ := s.GetContent(x, y); r == views.FocusMarkerRune {
			return x
		}
	}
	return -1
}

// ---------- 5.7 where the marker rests ----------

// Folded, the marker sits beside the title at the left gutter: the title is the
// first thing on the row, so the bar is one cell left of it.
func TestMarkerRestsAtTheLeftGutterWhenFolded(t *testing.T) {
	a, _, now := foldApp(t)
	a.moveFocus(client.FocusInput)
	advance(now, time.Second)
	a.render()

	want := layout.GutterX - 1
	if got := markerColumn(a); got != want {
		t.Fatalf("folded: marker at column %d, want %d (one cell left of the title)", got, want)
	}
}

// Open, with the list focused, it sits beside the sidebar's title.
func TestMarkerRestsBesideTheSidebarTitleWhenOpen(t *testing.T) {
	a, _, now := foldApp(t)
	advance(now, time.Second)
	a.render()

	want := int(views.SidebarRectAt(views.ContentRect(a.scr.Size()), 0).X) - 1
	if got := markerColumn(a); got != want {
		t.Fatalf("open: marker at column %d, want %d", got, want)
	}
}

// Whatever the route, the marker ends up beside the focused title once everything
// has settled — and it never sits over the conversation's text.
func TestMarkerSettlesBesideTheFocusedTitleOnEveryRoute(t *testing.T) {

	for name, do := range map[string]func(a *App){
		"focus the composer": func(a *App) { a.moveFocus(client.FocusInput) },
		"open from the list": func(a *App) {
			a.ui.Selected = client.IndexOfConversation(a.st.SidebarRows(), "c2")
			a.openConversation("c2")
			a.moveFocus(client.FocusInput)
		},
	} {
		a, _, now := foldApp(t)
		do(a)
		advance(now, time.Second)
		a.render()
		if got, want := markerColumn(a), layout.GutterX-1; got != want {
			t.Errorf("%s: marker at column %d, want %d", name, got, want)
		}
	}
}

// Opening a conversation while the composer already has the keyboard moves the
// title without any focus change; the marker must follow it.
func TestMarkerFollowsATitleThatMovesWithoutAFocusChange(t *testing.T) {
	a, _, now := foldApp(t)
	a.st.ActiveID = ""
	a.st.Focus = client.FocusInput // e.g. right after creating a channel
	a.settleFold()
	a.anim.slide.MoveTo(a.regionX(client.FocusInput), *now, 0, false)
	before := a.anim.slide.Target()

	a.openConversation("c2") // now there is a conversation: the list folds
	advance(now, time.Second)
	a.render()

	if a.anim.slide.Target() == before {
		t.Fatalf("the marker did not move though the title did (still %v)", before)
	}
	if got, want := markerColumn(a), layout.GutterX-1; got != want {
		t.Fatalf("marker at column %d, want %d beside the title", got, want)
	}
}

// Reading the animation state must never start an animation: asking whether frames
// are needed, or drawing a frame, on a screen at rest leaves it at rest.
func TestReadingTheBudgetDoesNotStartAnAnimation(t *testing.T) {
	a, _, now := foldApp(t)
	a.moveFocus(client.FocusInput)
	advance(now, time.Second)
	a.st.RestoreInput("a draft")
	a.render()

	for i := 0; i < 5; i++ {
		if a.needsFrames(*now) {
			t.Fatalf("a settled screen asked for frames on read %d", i)
		}
		a.render()
	}
}

// ---------- 5.9 the narrow window ----------

// In the single-pane layout the marker is the static one at the pane's edge and the
// fold stays 0, whichever pane is shown.
func TestNarrowWindowMarkerIsStaticAndNothingFolds(t *testing.T) {
	a, _ := newKeyTestApp(t)
	a.scr.(tcell.SimulationScreen).SetSize(40, 20)
	freezeClock(t, time.Unix(22000, 0))
	c := a.st.EnsureConversation("c1")
	c.Name = "general"
	a.openConversation("c1")

	for _, f := range []client.Focus{client.FocusInput, client.FocusList} {
		a.moveFocus(f)
		a.render()
		if a.foldNow(nowTime()) != 0 {
			t.Fatalf("focus %v: a narrow window folded", f)
		}
		if got := markerColumn(a); got != layout.GutterX-1 {
			t.Fatalf("focus %v: marker at column %d, want the pane's edge %d", f, got, layout.GutterX-1)
		}
	}
}

package views

import (
	"testing"

	"link-chat/pkg/client"
	"link-chat/pkg/i18n"
	"link-chat/pkg/protocol"
	"link-chat/pkg/tui/widget"
)

// anchorState has DMs above the channels, so the open conversation's row is well
// down the sidebar and a wrong row convention cannot hide behind a row near the top.
func anchorState() *client.AppState {
	st := client.NewAppState()
	add := func(id, name, kind string, at int64) {
		c := st.EnsureConversation(id)
		c.Name, c.Kind, c.LastMessageAt = name, kind, at
	}
	add("d1", "carol", protocol.KindDM, 500)
	add("d2", "bobby", protocol.KindDM, 400)
	add("c1", "openchan", protocol.KindChannel, 300)
	add("c2", "random", protocol.KindChannel, 200)
	st.ActiveID = "c1"
	st.State = client.StateReady
	return st
}

// The flight's two ends are checked against what the real renderers put on the
// screen, not against the formulas that compute them. A formula checked against
// itself cannot tell a wrong origin from a right one.
func TestFlightEndsMatchWhereTheRenderersDrawTheName(t *testing.T) {
	const w, h = 92, 28
	st := anchorState()
	content := ContentRect(w, h)
	rows := st.SidebarRows()
	sel := client.IndexOfConversation(rows, "c1")

	// Where the sidebar really draws the row's name when fully open.
	sideScreen := newScreen(t, w, h)
	List{T: toks()}.Render(sideScreen, SidebarRectAt(content, 0), ListModel{
		Rows: rows, Selected: sel, ActiveID: "c1", Focus: client.FocusList, Now: 1000,
	}, i18n.ZH)
	sx, sy, ok := findText(sideScreen, "openchan")
	if !ok {
		t.Fatal("the sidebar did not draw the open conversation's name")
	}

	// Where the transcript really draws its title when fully folded.
	titleScreen := newScreen(t, w, h)
	trRect, _ := ComposerRect(MainRectAt(content, 1), 1)
	Transcript{T: toks()}.Render(titleScreen, trRect, TranscriptModel{
		Conversation: st.Active(), UserID: "u", Now: 1000, Focus: client.FocusInput,
	}, i18n.ZH)
	tx, ty, ok := findText(titleScreen, "openchan")
	if !ok {
		t.Fatal("the transcript did not draw the title")
	}

	// The flight, built the way the app builds it.
	line, onScreen := RowLine(rows, sel, "c1", content.H, false, i18n.ZH)
	if !onScreen {
		t.Fatal("the open conversation's row is not on screen")
	}
	fx, fy := SidebarRowAnchor(content, line)
	ax, ay := TitleAnchor(content, 1)

	if fx != sx || fy != sy {
		t.Errorf("the flight starts at (%d,%d) but the sidebar draws the name at (%d,%d)", fx, fy, sx, sy)
	}
	if ax != tx || ay != ty {
		t.Errorf("the flight ends at (%d,%d) but the title is drawn at (%d,%d)", ax, ay, tx, ty)
	}

	// And at the very edges of the flight the name is on those same cells.
	f := NameFlight{Name: "openchan", FromX: fx, FromY: fy, ToX: ax, ToY: ay}
	f.Fold = 0.001
	if x, y := f.Position(); x != sx || y != sy {
		t.Errorf("just after leaving, the name is at (%d,%d), want the row's cell (%d,%d)", x, y, sx, sy)
	}
	f.Fold = 0.999
	if x, y := f.Position(); x != tx || y != ty {
		t.Errorf("just before landing, the name is at (%d,%d), want the title's cell (%d,%d)", x, y, tx, ty)
	}
}

// The same holds with a filter showing (the list's header is a row taller) and with
// the row scrolled to the bottom of a long list.
func TestFlightStartTracksTheRowWithAFilterAndAfterScrolling(t *testing.T) {
	const w, h = 92, 28
	content := ContentRect(w, h)

	for _, filtering := range []bool{false, true} {
		st := anchorState()
		for i := 0; i < 30; i++ {
			c := st.EnsureConversation("z" + string(rune('a'+i%26)) + string(rune('a'+i/26)))
			c.Name, c.Kind, c.LastMessageAt = "pad", protocol.KindChannel, int64(1000+i)
		}
		rows := st.SidebarRows()
		sel := client.IndexOfConversation(rows, "c1")

		var flt []rune
		if filtering {
			flt = []rune{}
		}
		s := newScreen(t, w, h)
		List{T: toks()}.Render(s, SidebarRectAt(content, 0), ListModel{
			Rows: rows, Selected: sel, ActiveID: "c1", Focus: client.FocusList, Filter: flt, Now: 1000,
		}, i18n.ZH)
		sx, sy, ok := findText(s, "openchan")
		if !ok {
			t.Fatalf("filtering=%v: the row is not drawn", filtering)
		}

		line, on := RowLine(rows, sel, "c1", content.H, filtering, i18n.ZH)
		if !on {
			t.Fatalf("filtering=%v: RowLine says the row is off screen but it is drawn", filtering)
		}
		fx, fy := SidebarRowAnchor(content, line)
		if fx != sx || fy != sy {
			t.Errorf("filtering=%v: the flight starts at (%d,%d), the row is at (%d,%d)", filtering, fx, fy, sx, sy)
		}
	}
	_ = widget.Rect{}
}

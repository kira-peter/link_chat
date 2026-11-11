package views

import (
	"strings"
	"testing"

	"link-chat/pkg/client"
	"link-chat/pkg/i18n"
	"link-chat/pkg/protocol"
	"link-chat/pkg/tui/widget"
)

const (
	noteW = 40
	noteH = 14 // 2 header rows + 12 body rows
)

// renderNote draws a conversation of three messages, the last padded to extra+1
// lines so the total height can be walked across the window's height, and returns
// the body rows as text.
func renderNote(t *testing.T, extra int, hasMore, loading bool) (rows []string, total int) {
	t.Helper()
	cv := &client.Conversation{}
	cv.ID, cv.Name, cv.Kind, cv.HasMore = "c1", "g", protocol.KindChannel, hasMore
	cv.Messages = []protocol.Message{
		{ID: "1", AuthorID: "me", AuthorName: "me", Body: "FIRST", CreatedAt: at(10, 0, 0)},
		{ID: "2", AuthorID: "me", AuthorName: "me", Body: "b", CreatedAt: at(10, 0, 5)},
		{ID: "3", AuthorID: "me", AuthorName: "me", Body: "c" + strings.Repeat("\nx", extra), CreatedAt: at(10, 0, 9)},
	}
	s := newScreen(t, noteW, noteH)
	total = Transcript{T: toks()}.Render(s, widget.Rect{X: 0, Y: 0, W: noteW, H: noteH}, TranscriptModel{
		Conversation: cv, UserID: "me", Now: at(23, 0, 0), Focus: client.FocusInput, LoadingOlder: loading,
	}, i18n.ZH)
	for y := 2; y < noteH; y++ {
		rows = append(rows, rowText(s, y, noteW))
	}
	return rows, total
}

func has(rows []string, want string) bool {
	return strings.Contains(strings.Join(rows, "\n"), want)
}

// Whatever the conversation's height relative to the window, the first thing in it
// — its opening time line — is never pushed out of view by the "start of
// conversation" note. Walked across the exact-fit boundary, where it used to go.
func TestStartNoteNeverCostsTheOpeningTimeLine(t *testing.T) {
	const bodyH = noteH - 2
	clock := i18n.ClockOnly(at(10, 0, 0))

	for extra := 0; extra < 6; extra++ {
		rows, total := renderNote(t, extra, false, false)
		if total > bodyH {
			continue // scrolled: the top is not on screen, nothing to assert about it
		}
		// The whole conversation fits, so the opening time line (the clock, alone on
		// its row, centred) must be on screen.
		found := false
		for _, r := range rows {
			if strings.TrimSpace(r) == clock {
				found = true
			}
		}
		if !found {
			t.Errorf("total %d fits the %d-row body, but the opening time line %q is missing:\n%s",
				total, bodyH, clock, strings.Join(rows, "\n"))
		}
	}
}

// The note is only ever shown when the start really is on screen.
func TestStartNoteOnlyWhenTheStartIsVisible(t *testing.T) {
	for extra := 0; extra < 8; extra++ {
		rows, total := renderNote(t, extra, false, false)
		if has(rows, "到达会话开头") && !has(rows, "FIRST") {
			t.Errorf("total %d: the note says this is the start, but the first message is not shown:\n%s",
				total, strings.Join(rows, "\n"))
		}
	}
}

// And it is shown when there is room: a short conversation gets it.
func TestStartNoteShowsForAShortConversation(t *testing.T) {
	rows, _ := renderNote(t, 0, false, false)
	if !has(rows, "到达会话开头") {
		t.Fatalf("a short conversation with nothing earlier should say it has reached the start:\n%s", strings.Join(rows, "\n"))
	}
}

// With earlier history still to load there is no "start" to announce.
func TestNoStartNoteWhileMoreHistoryExists(t *testing.T) {
	for extra := 0; extra < 4; extra++ {
		rows, _ := renderNote(t, extra, true, false)
		if has(rows, "到达会话开头") {
			t.Fatalf("extra %d: announced the start though more history exists", extra)
		}
	}
}

// The note sits on its own row above the messages: it never shares a row with a
// bubble, border or time line, so it cannot erase one. Checked at every height
// including the exact fit, where the conversation's first row is on the body's
// first line and a note painted there would sit directly on top of it. (In a short
// conversation the first line is empty, so a note drawn over the content would
// look identical — that case proves nothing, which is why the exact fit matters.)
func TestNoteIsOnItsOwnRow(t *testing.T) {
	const bodyH = noteH - 2
	sawExactFit := false

	for extra := 0; extra < 6; extra++ {
		rows, total := renderNote(t, extra, false, false)
		if total == bodyH {
			sawExactFit = true
		}
		for i, r := range rows {
			if !strings.Contains(r, "到达会话开头") {
				continue
			}
			if strings.ContainsAny(r, "┌┐└┘│") || strings.Contains(r, "FIRST") {
				t.Fatalf("total %d: the note shares row %d with content: %q", total, i, r)
			}
		}
		// Whenever the note is shown, every row of the conversation is also shown:
		// the note took a blank row, not a content one.
		if has(rows, "到达会话开头") {
			for _, want := range []string{"FIRST", i18n.ClockOnly(at(10, 0, 0))} {
				if !has(rows, want) {
					t.Errorf("total %d: with the note shown, %q has gone missing:\n%s", total, want, strings.Join(rows, "\n"))
				}
			}
		}
	}
	if !sawExactFit {
		t.Fatal("the sweep never produced a conversation exactly as tall as the body; the test is not exercising the boundary")
	}
}

// The exact fit is the awkward height: the conversation fills the body to the last
// row, so room for the note has to come from somewhere. It comes from the
// conversation's own blank breathing row, never from a content row — and when
// there is nowhere to take it from, the note is dropped rather than the content.
// At this height both things must hold: the opening time line is on screen, and the
// note is either shown on a row of its own or absent, never half-drawn.
//
// A conversation one row shorter has a spare blank row and shows both; this one has
// none to spare. What it must never do is lose the time line to keep the note.
func TestExactFitKeepsTheTimeLineAndNeverHalfDrawsTheNote(t *testing.T) {
	const bodyH = noteH - 2
	clock := i18n.ClockOnly(at(10, 0, 0))

	var rows []string
	var total int
	for extra := 0; extra < 6; extra++ {
		r, tot := renderNote(t, extra, false, false)
		if tot == bodyH {
			rows, total = r, tot
		}
	}
	if total != bodyH {
		t.Fatalf("the sweep never produced an exact fit (last total %d, body %d)", total, bodyH)
	}

	timeShown := false
	for _, r := range rows {
		if strings.TrimSpace(r) == clock {
			timeShown = true
		}
	}
	if !timeShown {
		t.Fatalf("at the exact fit the opening time line %q is missing:\n%s", clock, strings.Join(rows, "\n"))
	}
	if !has(rows, "FIRST") {
		t.Fatalf("at the exact fit the first message is missing:\n%s", strings.Join(rows, "\n"))
	}
	// If the note is there at all it is whole.
	if strings.Contains(strings.Join(rows, ""), "到达") && !has(rows, "到达会话开头") {
		t.Fatalf("the note is half drawn:\n%s", strings.Join(rows, "\n"))
	}
}

// Walking the whole range of heights, a note that is shown is always whole and
// always on a row with nothing else on it.
func TestNoteIsWholeAndAloneWheneverShown(t *testing.T) {
	shown := 0
	for extra := 0; extra < 8; extra++ {
		rows, _ := renderNote(t, extra, false, false)
		for i, r := range rows {
			if !strings.Contains(r, "到达") {
				continue
			}
			shown++
			if !strings.Contains(r, "到达会话开头") {
				t.Errorf("extra %d row %d: a note was cut: %q", extra, i, r)
			}
			if strings.TrimSpace(strings.Replace(r, "到达会话开头", "", 1)) != "" {
				t.Errorf("extra %d row %d: the note shares its row with %q", extra, i, r)
			}
		}
	}
	if shown == 0 {
		t.Fatal("the note was never shown at any height; the test checks nothing")
	}
}

// The loading note follows the same rule and also never hides the opening time line
// of what is loaded when everything fits.
func TestLoadingNoteDoesNotEraseTheTopOfTheList(t *testing.T) {
	const bodyH = noteH - 2
	for extra := 0; extra < 6; extra++ {
		rows, total := renderNote(t, extra, true, true)
		if total > bodyH {
			continue
		}
		if !has(rows, "FIRST") {
			t.Errorf("total %d: the loading note pushed the first message out of view:\n%s", total, strings.Join(rows, "\n"))
		}
	}
}

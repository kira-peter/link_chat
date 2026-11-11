package app

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/client"
	"link-chat/pkg/tui/views"
)

// key builds a rune key event, optionally with Alt.
func key(r rune) *tcell.EventKey {
	return tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone)
}

func altKey(r rune) *tcell.EventKey {
	return tcell.NewEventKey(tcell.KeyRune, r, tcell.ModAlt)
}

func specialKey(k tcell.Key) *tcell.EventKey {
	return tcell.NewEventKey(k, 0, tcell.ModNone)
}

// newKeyTestApp builds an app whose composer is ready to accept typing.
func newKeyTestApp(t *testing.T) (*App, context.Context) {
	t.Helper()
	scr := newTestScreen(t, 100, 30)
	a := New(scr, Config{
		Server:   "ws://127.0.0.1:1/ws",
		Theme:    "ink",
		Language: "zh",
	}, filepath.Join(t.TempDir(), "k.json"))
	a.ui.Dialog = views.DialogNone
	a.st.State = client.StateReady
	return a, context.Background()
}

// newOpenKeyTestApp is newKeyTestApp with a conversation already open. Moving the
// keyboard to the composer is only possible when there is a conversation to type
// into (spec tui-chat-client / 没有打开的会话时向右), so the tests that cross
// between the list and the composer need one.
func newOpenKeyTestApp(t *testing.T) (*App, context.Context) {
	t.Helper()
	a, ctx := newKeyTestApp(t)
	cv := a.st.EnsureConversation("open1")
	cv.Name, cv.Kind = "general", "channel"
	a.st.ActiveID = "open1"
	return a, ctx
}

// The composer owns the keyboard from the start: a chat client exists to be typed
// into, and requiring a click before the first keystroke makes Enter appear
// broken.
func TestComposerHasFocusByDefault(t *testing.T) {
	a, _ := newKeyTestApp(t)
	if a.st.Focus != client.FocusInput {
		t.Fatalf("default focus = %v, want FocusInput", a.st.Focus)
	}
	if client.NewAppState().Focus != client.FocusInput {
		t.Fatal("NewAppState does not focus the composer")
	}
}

// Printable letters must reach the draft. This is the regression that made "s"
// open settings instead of typing an s.
func TestLettersTypeInsteadOfRunningCommands(t *testing.T) {
	a, ctx := newKeyTestApp(t)

	for _, r := range "sqntom?/" {
		a.handleKey(ctx, key(r))
	}
	want := "sqntom?/"
	if got := a.st.InputText(); got != want {
		t.Fatalf("typed %q, want %q", got, want)
	}
	if a.ui.Dialog != views.DialogNone {
		t.Fatalf("a letter opened dialog %v", a.ui.Dialog)
	}
	if a.ui.Quitting {
		t.Fatal("a letter quit the app")
	}
}

// Every command lives on Alt, and works regardless of which pane has focus.
func TestAltCommandsWorkFromAnyFocus(t *testing.T) {
	cases := []struct {
		r    rune
		want views.DialogKind
	}{
		{'s', views.DialogSettings},
		{'h', views.DialogHelp},
		{'n', views.DialogChannel},
		{'o', views.DialogDM},
	}

	for _, c := range cases {
		for _, focus := range []client.Focus{client.FocusInput, client.FocusList} {
			a, ctx := newKeyTestApp(t)
			a.st.Focus = focus
			a.handleKey(ctx, altKey(c.r))
			if a.ui.Dialog != c.want {
				t.Fatalf("Alt+%c from focus %v opened %v, want %v", c.r, focus, a.ui.Dialog, c.want)
			}
			// The command must not have typed anything.
			if a.st.InputText() != "" {
				t.Fatalf("Alt+%c typed into the draft: %q", c.r, a.st.InputText())
			}
		}
	}
}

// Alt+Q quits; a bare q types.
func TestAltQQuitsButQTypes(t *testing.T) {
	a, ctx := newKeyTestApp(t)
	a.handleKey(ctx, key('q'))
	if a.ui.Quitting {
		t.Fatal("bare q quit the app")
	}
	if a.st.InputText() != "q" {
		t.Fatalf("q did not type: %q", a.st.InputText())
	}

	a.handleKey(ctx, altKey('q'))
	if !a.ui.Quitting {
		t.Fatal("Alt+Q did not quit")
	}
}

// Tab switches region both ways, so arrows are not the only route.
func TestTabCyclesFocus(t *testing.T) {
	a, ctx := newOpenKeyTestApp(t)
	if a.st.Focus != client.FocusInput {
		t.Fatal("expected input focus to start")
	}
	a.handleKey(ctx, specialKey(tcell.KeyTab))
	if a.st.Focus != client.FocusList {
		t.Fatalf("Tab gave focus %v, want FocusList", a.st.Focus)
	}
	a.handleKey(ctx, specialKey(tcell.KeyTab))
	if a.st.Focus != client.FocusInput {
		t.Fatalf("Tab gave focus %v, want FocusInput", a.st.Focus)
	}
}

// Enter sends when the composer has focus, and does not open a conversation.
func TestEnterSendsFromComposer(t *testing.T) {
	a, ctx := newKeyTestApp(t)
	// Without a transport or conversation a send is a no-op, so what is asserted
	// is that Enter was treated as "send" rather than as a list action: the draft
	// is consumed either way, which distinguishes it from typing a literal newline.
	a.st.EnsureConversation("c1")
	a.st.ActiveID = "c1"
	a.st.RestoreInput("hello")

	a.handleKey(ctx, specialKey(tcell.KeyEnter))

	if got := a.st.InputText(); got == "hello\n" {
		t.Fatal("Enter inserted a newline instead of sending")
	}
}

// Shift+Enter inserts a newline rather than sending.
func TestShiftEnterInsertsNewline(t *testing.T) {
	a, _ := newKeyTestApp(t)
	a.st.RestoreInput("line one")
	ev := tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModShift)
	a.handleKey(context.Background(), ev)
	if got := a.st.InputText(); got != "line one\n" {
		t.Fatalf("Shift+Enter gave %q", got)
	}
}

// In the list, Enter opens the selected conversation and hands the keyboard back
// to the composer, so the user can immediately type.
func TestEnterInListOpensAndRefocusesComposer(t *testing.T) {
	a, ctx := newKeyTestApp(t)
	a.st.Focus = client.FocusList
	cv := a.st.EnsureConversation("c1")
	cv.Name = "general"
	cv.Kind = "channel"
	a.ui.Selected = 0

	a.handleKey(ctx, specialKey(tcell.KeyEnter))

	if a.st.ActiveID != "c1" {
		t.Fatalf("Enter did not open the conversation: %q", a.st.ActiveID)
	}
	if a.st.Focus != client.FocusInput {
		t.Fatalf("focus after opening = %v, want FocusInput", a.st.Focus)
	}
}

// The list sits on the LEFT and the composer on the RIGHT, so ← goes to the list
// and → goes to the composer. The earlier implementation had these reversed
// because focus was a symmetric toggle; this test asserts the screen geometry.
func TestArrowKeysFollowScreenPosition(t *testing.T) {
	a, ctx := newOpenKeyTestApp(t)

	// From the composer (right), ← lands in the list (left) — once the caret has
	// nowhere further left to go, which is trivially true for an empty draft.
	a.st.Focus = client.FocusInput
	a.handleKey(ctx, specialKey(tcell.KeyLeft))
	if a.st.Focus != client.FocusList {
		t.Fatalf("← from the composer gave %v, want FocusList", a.st.Focus)
	}

	// From the list (left), → lands in the composer (right).
	a.handleKey(ctx, specialKey(tcell.KeyRight))
	if a.st.Focus != client.FocusInput {
		t.Fatalf("→ from the list gave %v, want FocusInput", a.st.Focus)
	}
}

// Pressing towards an edge you are already at must do nothing; it must never wrap
// round to the far side (spec tui-chat-client / 边界上继续按同方向键).
func TestArrowKeysDoNotWrapAtEdges(t *testing.T) {
	a, ctx := newKeyTestApp(t)

	// Already leftmost: ← stays put.
	a.st.Focus = client.FocusList
	a.handleKey(ctx, specialKey(tcell.KeyLeft))
	if a.st.Focus != client.FocusList {
		t.Fatalf("← at the left edge moved focus to %v", a.st.Focus)
	}

	// Already rightmost: → stays put, even with the caret at the end of a draft.
	a.st.Focus = client.FocusInput
	a.st.RestoreInput("hello")
	a.st.CursorEnd()
	a.handleKey(ctx, specialKey(tcell.KeyRight))
	if a.st.Focus != client.FocusInput {
		t.Fatalf("→ at the right edge moved focus to %v", a.st.Focus)
	}
	if a.st.InputText() != "hello" {
		t.Fatalf("draft changed: %q", a.st.InputText())
	}
}

// With a draft, the arrows edit the caret first; focus only moves once the caret
// has no further to go.
func TestArrowKeysPreferCaretOverFocus(t *testing.T) {
	a, ctx := newKeyTestApp(t)
	a.st.Focus = client.FocusInput
	a.st.RestoreInput("abc")
	a.st.CursorEnd() // caret after "c"

	a.handleKey(ctx, specialKey(tcell.KeyLeft))
	if a.st.Focus != client.FocusInput {
		t.Fatal("← moved focus while the caret still had room to move")
	}
	if a.st.InputCursor != 2 {
		t.Fatalf("caret at %d, want 2", a.st.InputCursor)
	}

	// → edits the caret too and never leaves the rightmost region.
	a.handleKey(ctx, specialKey(tcell.KeyRight))
	if a.st.Focus != client.FocusInput || a.st.InputCursor != 3 {
		t.Fatalf("→ gave focus %v caret %d", a.st.Focus, a.st.InputCursor)
	}
}

// Once the caret reaches the start, ← hands focus to the list, and the draft and
// caret survive the trip so → brings the user back exactly where they were.
func TestHandOffLeftKeepsDraftAndCaret(t *testing.T) {
	a, ctx := newOpenKeyTestApp(t)
	a.st.Focus = client.FocusInput
	a.st.RestoreInput("draft text")
	a.st.CursorHome()

	a.handleKey(ctx, specialKey(tcell.KeyLeft))
	if a.st.Focus != client.FocusList {
		t.Fatalf("← at the start of a draft gave %v, want FocusList", a.st.Focus)
	}
	if a.st.InputText() != "draft text" {
		t.Fatalf("draft lost on hand-off: %q", a.st.InputText())
	}
	if a.st.InputCursor != 0 {
		t.Fatalf("caret moved on hand-off: %d", a.st.InputCursor)
	}

	a.handleKey(ctx, specialKey(tcell.KeyRight))
	if a.st.Focus != client.FocusInput {
		t.Fatalf("→ did not return to the composer: %v", a.st.Focus)
	}
	if a.st.InputText() != "draft text" || a.st.InputCursor != 0 {
		t.Fatalf("state not restored: %q caret %d", a.st.InputText(), a.st.InputCursor)
	}
}

// Tab cycles and, unlike the arrows, is allowed to wrap: it means "next", not a
// direction.
func TestTabStillCycles(t *testing.T) {
	a, ctx := newOpenKeyTestApp(t)
	a.st.Focus = client.FocusInput
	a.handleKey(ctx, specialKey(tcell.KeyTab))
	if a.st.Focus != client.FocusList {
		t.Fatalf("Tab gave %v", a.st.Focus)
	}
	a.handleKey(ctx, specialKey(tcell.KeyTab))
	if a.st.Focus != client.FocusInput {
		t.Fatalf("second Tab gave %v", a.st.Focus)
	}
}

// Filter mode captures letters for narrowing rather than typing into the draft.
func TestFilterModeCapturesLetters(t *testing.T) {
	a, ctx := newKeyTestApp(t)
	cv := a.st.EnsureConversation("c1")
	cv.Name = "general"

	a.handleKey(ctx, altKey('f'))
	if a.st.Filter == nil {
		t.Fatal("Alt+F did not start filtering")
	}
	a.handleKey(ctx, key('g'))
	a.handleKey(ctx, key('e'))
	if got := string(a.st.Filter); got != "ge" {
		t.Fatalf("filter = %q, want %q", got, "ge")
	}
	if a.st.InputText() != "" {
		t.Fatalf("filter letters leaked into the draft: %q", a.st.InputText())
	}

	a.handleKey(ctx, specialKey(tcell.KeyEscape))
	if a.st.Filter != nil {
		t.Fatal("Esc did not end filtering")
	}
}

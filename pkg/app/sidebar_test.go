package app

import (
	"context"
	"testing"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/client"
	"link-chat/pkg/protocol"
	"link-chat/pkg/tui/views"
)

// newSidebarApp builds a ready app showing the main interface with the keyboard on
// the list and two DMs plus two channels:
//
//	私聊: bob (newest), carol    群聊: design (newest), general
//
// Rows, in order: H bob carol H design general settings help about.
func newSidebarApp(t *testing.T) (*App, context.Context) {
	t.Helper()
	a, ctx := newKeyTestApp(t)
	add := func(id, name, kind string, at int64) {
		c := a.st.EnsureConversation(id)
		c.Name, c.Kind, c.LastMessageAt = name, kind, at
	}
	add("d1", "carol", protocol.KindDM, 100)
	add("d2", "bob", protocol.KindDM, 300)
	add("c1", "general", protocol.KindChannel, 200)
	add("c2", "design", protocol.KindChannel, 400)
	a.st.Focus = client.FocusList
	a.ui.Selected = client.FirstSelectable(a.st.SidebarRows())
	return a, ctx
}

// selectedLabel names the row under the cursor, for readable failures.
func selectedLabel(a *App) string {
	rows := a.st.SidebarRows()
	if a.ui.Selected < 0 || a.ui.Selected >= len(rows) {
		return "<out of range>"
	}
	r := rows[a.ui.Selected]
	switch r.Kind {
	case client.RowConversation:
		return r.Conversation.Name
	case client.RowEntry:
		return map[client.EntryID]string{
			client.EntrySettings: "settings", client.EntryHelp: "help", client.EntryAbout: "about",
		}[r.Entry]
	}
	return "<unselectable>"
}

func press(a *App, ctx context.Context, k tcell.Key) { a.handleKey(ctx, specialKey(k)) }

// ---------- 4.2 walking the cursor ----------

// Down walks DMs, then channels, then the three entries, in one unbroken run.
func TestDownWalksAcrossSectionsToTheFoot(t *testing.T) {
	a, ctx := newSidebarApp(t)
	want := []string{"bob", "carol", "design", "general", "settings", "help", "about"}
	for i, w := range want {
		if got := selectedLabel(a); got != w {
			t.Fatalf("step %d: cursor on %q, want %q", i, got, w)
		}
		press(a, ctx, tcell.KeyDown)
	}
	// Down at "about" does nothing.
	if got := selectedLabel(a); got != "about" {
		t.Fatalf("down past the end moved the cursor to %q", got)
	}
}

// Up is the mirror, and stops at the first conversation instead of wrapping.
func TestUpWalksBackAndStopsAtTheTop(t *testing.T) {
	a, ctx := newSidebarApp(t)
	for i := 0; i < 10; i++ {
		press(a, ctx, tcell.KeyDown)
	}
	want := []string{"about", "help", "settings", "general", "design", "carol", "bob"}
	for i, w := range want {
		if got := selectedLabel(a); got != w {
			t.Fatalf("step %d: cursor on %q, want %q", i, got, w)
		}
		press(a, ctx, tcell.KeyUp)
	}
	if got := selectedLabel(a); got != "bob" {
		t.Fatalf("up past the top moved the cursor to %q", got)
	}
}

// j and k do the same as the arrows.
func TestJKMatchArrows(t *testing.T) {
	a, ctx := newSidebarApp(t)
	a.handleKey(ctx, key('j'))
	a.handleKey(ctx, key('j'))
	if got := selectedLabel(a); got != "design" {
		t.Fatalf("two j presses reached %q, want the first channel (skipping the header)", got)
	}
	a.handleKey(ctx, key('k'))
	if got := selectedLabel(a); got != "carol" {
		t.Fatalf("k from the first channel reached %q, want the last DM", got)
	}
}

// The cursor never rests on a header or a placeholder, whatever the stored index.
func TestStaleCursorIsPulledOntoASelectableRow(t *testing.T) {
	a, ctx := newSidebarApp(t)
	for _, stale := range []int{0, 3, -1, 999} { // header, header, negative, past the end
		a.ui.Selected = stale
		press(a, ctx, tcell.KeyDown)
		rows := a.st.SidebarRows()
		if a.ui.Selected < 0 || a.ui.Selected >= len(rows) || !rows[a.ui.Selected].Selectable() {
			t.Errorf("stale %d left the cursor on row %d, which cannot be selected", stale, a.ui.Selected)
		}
	}
}

// With no conversations at all the cursor still has somewhere to be: Settings.
func TestEmptySidebarCursorStartsOnSettings(t *testing.T) {
	a, ctx := newKeyTestApp(t)
	a.st.Focus = client.FocusList
	a.ui.Selected = 0
	press(a, ctx, tcell.KeyDown)
	if got := selectedLabel(a); got != "settings" && got != "help" {
		t.Fatalf("cursor on %q in an empty sidebar", got)
	}
}

// ---------- 4.4 activating a row ----------

func TestEnterOnAConversationOpensItAndFocusesTheComposer(t *testing.T) {
	a, ctx := newSidebarApp(t)
	press(a, ctx, tcell.KeyDown) // carol
	press(a, ctx, tcell.KeyDown) // design
	press(a, ctx, tcell.KeyEnter)

	if a.st.ActiveID != "c2" {
		t.Fatalf("opened %q, want design (c2)", a.st.ActiveID)
	}
	if a.st.Focus != client.FocusInput {
		t.Fatalf("focus after opening = %v, want the composer", a.st.Focus)
	}
}

// Each foot entry opens its dialog, and Esc returns with the cursor where it was.
func TestEnterOnEachFootEntryOpensItsDialog(t *testing.T) {
	cases := []struct {
		downs int
		want  views.DialogKind
		name  string
	}{
		{4, views.DialogSettings, "settings"},
		{5, views.DialogHelp, "help"},
		{6, views.DialogAbout, "about"},
	}
	for _, c := range cases {
		a, ctx := newSidebarApp(t)
		for i := 0; i < c.downs; i++ {
			press(a, ctx, tcell.KeyDown)
		}
		if got := selectedLabel(a); got != c.name {
			t.Fatalf("%s: cursor on %q before Enter", c.name, got)
		}
		before := a.ui.Selected
		press(a, ctx, tcell.KeyEnter)
		if a.ui.Dialog != c.want {
			t.Fatalf("%s: Enter opened dialog %v, want %v", c.name, a.ui.Dialog, c.want)
		}

		press(a, ctx, tcell.KeyEscape)
		if a.ui.Dialog != views.DialogNone {
			t.Fatalf("%s: Esc did not close the dialog", c.name)
		}
		if a.ui.Selected != before || selectedLabel(a) != c.name {
			t.Fatalf("%s: after closing, the cursor moved to %q", c.name, selectedLabel(a))
		}
		if a.st.ActiveID != "" {
			t.Fatalf("%s: opening an entry must not open a conversation (got %q)", c.name, a.st.ActiveID)
		}
	}
}

// The Alt shortcuts open the same dialogs from any focus, so the two routes agree.
func TestAltShortcutsMatchTheEntries(t *testing.T) {
	for _, focus := range []client.Focus{client.FocusList, client.FocusInput} {
		a, ctx := newSidebarApp(t)
		a.st.Focus = focus
		a.handleKey(ctx, altKey('s'))
		if a.ui.Dialog != views.DialogSettings {
			t.Errorf("focus %v: Alt+S opened %v", focus, a.ui.Dialog)
		}
		a.ui.Dialog = views.DialogNone
		a.handleKey(ctx, altKey('h'))
		if a.ui.Dialog != views.DialogHelp {
			t.Errorf("focus %v: Alt+H opened %v", focus, a.ui.Dialog)
		}
	}
}

// About closes on Esc and on Enter, and nothing else leaks through to the main UI.
func TestAboutDialogCloses(t *testing.T) {
	for _, k := range []tcell.Key{tcell.KeyEscape, tcell.KeyEnter} {
		a, ctx := newSidebarApp(t)
		a.ui.Dialog = views.DialogAbout
		press(a, ctx, k)
		if a.ui.Dialog != views.DialogNone {
			t.Errorf("key %v did not close About", k)
		}
	}
	a, ctx := newSidebarApp(t)
	a.ui.Dialog = views.DialogAbout
	a.handleKey(ctx, key('x'))
	if a.ui.Dialog != views.DialogAbout {
		t.Error("a stray letter closed About")
	}
	if a.st.InputText() != "" {
		t.Error("a letter typed in About leaked into the composer")
	}
}

// ---------- the filter ----------

// Filtering narrows both sections together; the cursor lands on a real match and
// Enter opens it.
func TestFilterThenEnterOpensTheMatch(t *testing.T) {
	a, ctx := newSidebarApp(t)
	a.handleKey(ctx, altKey('f'))
	for _, r := range "des" { // only the channel "design"
		a.handleKey(ctx, key(r))
	}
	rows := a.st.SidebarRows()
	convs := 0
	for _, r := range rows {
		if r.Kind == client.RowConversation {
			convs++
		}
	}
	if convs != 1 {
		t.Fatalf("filter 'des' left %d conversations, want 1", convs)
	}

	press(a, ctx, tcell.KeyEnter)
	if a.st.ActiveID != "c2" {
		t.Fatalf("Enter in the filter opened %q, want design (c2)", a.st.ActiveID)
	}
	if a.st.Filter != nil {
		t.Fatal("the filter was not cleared after accepting it")
	}
}

// With several matches the cursor lands on the first one of them, not on whatever
// row the old index happened to point at once the list shrank.
func TestFilterWithSeveralMatchesLandsOnTheFirst(t *testing.T) {
	a, ctx := newSidebarApp(t)
	// Park the cursor deep in the list, on "general": bob -> carol -> design ->
	// general is three presses.
	for i := 0; i < 3; i++ {
		press(a, ctx, tcell.KeyDown)
	}
	if got := selectedLabel(a); got != "general" {
		t.Fatalf("precondition: cursor on %q, want general", got)
	}

	// "e" matches design and general; bob and carol have none.
	a.handleKey(ctx, altKey('f'))
	a.handleKey(ctx, key('e'))

	// Any cursor movement resolves the stored index against the narrowed rows. Down
	// then up returns to wherever the cursor really was.
	press(a, ctx, tcell.KeyDown)
	press(a, ctx, tcell.KeyUp)
	rows := a.st.SidebarRows()
	if !rows[a.ui.Selected].Selectable() {
		t.Fatalf("the cursor is on an unselectable row after filtering")
	}
	if got := selectedLabel(a); got != "design" {
		t.Fatalf("after filtering the cursor is on %q, want the first match, design", got)
	}
}

// A filter that matches nothing leaves the foot, and Enter on it must not open a
// conversation or crash.
func TestFilterWithNoMatchIsSafe(t *testing.T) {
	a, ctx := newSidebarApp(t)
	a.handleKey(ctx, altKey('f'))
	for _, r := range "zzzz" {
		a.handleKey(ctx, key(r))
	}
	press(a, ctx, tcell.KeyEnter)
	if a.st.ActiveID != "" {
		t.Fatalf("Enter with no match opened %q", a.st.ActiveID)
	}
}

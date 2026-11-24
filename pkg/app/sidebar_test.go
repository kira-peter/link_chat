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
// Rows, in order: H +dm bob carol H +ch design general settings help about.
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
	case client.RowNew:
		if r.Section == client.SectionDM {
			return "+dm"
		}
		return "+ch"
	case client.RowEntry:
		return map[client.EntryID]string{
			client.EntryFriends: "friends", client.EntryMoments: "moments", client.EntryMe: "me",
		}[r.Entry]
	}
	return "<unselectable>"
}

func press(a *App, ctx context.Context, k tcell.Key) { a.handleKey(ctx, specialKey(k)) }

// ---------- 4.2 walking the cursor ----------

// Down walks DMs, then the channels' "new" button and the channels, then the
// three entries, in one unbroken run.
func TestDownWalksAcrossSectionsToTheFoot(t *testing.T) {
	a, ctx := newSidebarApp(t)
	want := []string{"bob", "carol", "+ch", "design", "general", "friends", "moments", "me"}
	for i, w := range want {
		if got := selectedLabel(a); got != w {
			t.Fatalf("step %d: cursor on %q, want %q", i, got, w)
		}
		press(a, ctx, tcell.KeyDown)
	}
	// Down at "me" wraps round to the top: the direct messages' "new" button.
	if got := selectedLabel(a); got != "+dm" {
		t.Fatalf("down past the end moved the cursor to %q, want the top", got)
	}
}

// Up is the mirror, and from the top wraps round to the last entry.
func TestUpWalksBackAndWrapsAtTheTop(t *testing.T) {
	a, ctx := newSidebarApp(t)
	for i := 0; i < 7; i++ {
		press(a, ctx, tcell.KeyDown)
	}
	want := []string{"me", "moments", "friends", "general", "design", "+ch", "carol", "bob", "+dm"}
	for i, w := range want {
		if got := selectedLabel(a); got != w {
			t.Fatalf("step %d: cursor on %q, want %q", i, got, w)
		}
		press(a, ctx, tcell.KeyUp)
	}
	if got := selectedLabel(a); got != "me" {
		t.Fatalf("up past the top moved the cursor to %q, want me", got)
	}
}

// j and k do the same as the arrows.
func TestJKMatchArrows(t *testing.T) {
	a, ctx := newSidebarApp(t)
	a.handleKey(ctx, key('j'))
	a.handleKey(ctx, key('j'))
	if got := selectedLabel(a); got != "+ch" {
		t.Fatalf("two j presses reached %q, want the channels' new button (skipping the header)", got)
	}
	a.handleKey(ctx, key('k'))
	if got := selectedLabel(a); got != "carol" {
		t.Fatalf("k from the channels' new button reached %q, want the last DM", got)
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

// With no conversations at all the cursor still has somewhere to be: the "new"
// buttons.
func TestEmptySidebarCursorStartsOnANewButton(t *testing.T) {
	a, ctx := newKeyTestApp(t)
	a.st.Focus = client.FocusList
	a.ui.Selected = 0
	press(a, ctx, tcell.KeyDown)
	if got := selectedLabel(a); got != "+dm" && got != "+ch" {
		t.Fatalf("cursor on %q in an empty sidebar", got)
	}
}

// ---------- 4.4 activating a row ----------

func TestEnterOnAConversationOpensItAndFocusesTheComposer(t *testing.T) {
	a, ctx := newSidebarApp(t)
	press(a, ctx, tcell.KeyDown) // carol
	press(a, ctx, tcell.KeyDown) // + new channel
	press(a, ctx, tcell.KeyDown) // design
	press(a, ctx, tcell.KeyEnter)

	if a.st.ActiveID != "c2" {
		t.Fatalf("opened %q, want design (c2)", a.st.ActiveID)
	}
	if a.st.Focus != client.FocusInput {
		t.Fatalf("focus after opening = %v, want the composer", a.st.Focus)
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
	// Park the cursor deep in the list, on "general": bob -> carol -> +ch ->
	// design -> general is four presses.
	for i := 0; i < 4; i++ {
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

// Each section opens with a "new" button the arrows reach. Resting on it shows
// its page on the right; → or Enter enters the page like a conversation — the
// list folds and the keyboard goes to the page — and Esc comes back.
func TestNewButtonsShowAndEnterTheirPages(t *testing.T) {
	for _, c := range []struct {
		ups   int // from bob, the first conversation
		downs int
		label string
		page  client.Page
		key   tcell.Key
	}{
		{1, 0, "+dm", client.PageNewDM, tcell.KeyEnter},
		{0, 2, "+ch", client.PageNewChannel, tcell.KeyRight},
	} {
		a, ctx := newSidebarApp(t)
		a.st.State = client.StateReady
		for i := 0; i < c.ups; i++ {
			press(a, ctx, tcell.KeyUp)
		}
		for i := 0; i < c.downs; i++ {
			press(a, ctx, tcell.KeyDown)
		}
		if got := selectedLabel(a); got != c.label {
			t.Fatalf("cursor on %q, want %q", got, c.label)
		}
		if a.page() != c.page || a.st.Focus != client.FocusList {
			t.Fatalf("%s: resting on the button gave page %v focus %v", c.label, a.page(), a.st.Focus)
		}
		press(a, ctx, c.key)
		if a.page() != c.page || a.st.Focus != client.FocusInput || a.ui.Dialog != views.DialogNone {
			t.Fatalf("%s: the key gave page %v focus %v dialog %v", c.label, a.page(), a.st.Focus, a.ui.Dialog)
		}
		if a.foldTarget() != 1 {
			t.Fatalf("%s: entering the page does not fold the list", c.label)
		}
		// The channel page has a name field; the direct-message page is a list
		// of friends to pick from, with nothing to type into.
		if c.page == client.PageNewChannel {
			for _, r := range "zed" {
				a.handleKey(ctx, key(r))
			}
			if a.ui.NewPage.Value != "zed" || a.st.InputText() != "" {
				t.Fatalf("%s: typing went to %q (draft %q)", c.label, a.ui.NewPage.Value, a.st.InputText())
			}
		}
		press(a, ctx, tcell.KeyEscape)
		if selectedLabel(a) != c.label || a.st.Focus != client.FocusList || a.foldTarget() != 0 {
			t.Fatalf("%s: Esc left cursor %q, focus %v", c.label, selectedLabel(a), a.st.Focus)
		}
	}
}

package client

import (
	"testing"

	"link-chat/pkg/protocol"
)

// sidebarFixture builds a state with two DMs and three channels, each section
// out of order on purpose so the sort is what puts them right.
func sidebarFixture() *AppState {
	s := NewAppState()
	add := func(id, name, kind string, at int64) {
		c := s.EnsureConversation(id)
		c.Name, c.Kind, c.LastMessageAt = name, kind, at
	}
	add("d1", "carol", protocol.KindDM, 100)
	add("c1", "general", protocol.KindChannel, 500)
	add("d2", "bob", protocol.KindDM, 300)
	add("c2", "random", protocol.KindChannel, 200)
	add("c3", "design", protocol.KindChannel, 900)
	return s
}

// names lists what a row slice shows, in order, as "kind:label".
func describe(rows []Row) []string {
	var out []string
	for _, r := range rows {
		switch r.Kind {
		case RowHeader:
			if r.Section == SectionDM {
				out = append(out, "H:dm")
			} else {
				out = append(out, "H:chan")
			}
		case RowNew:
			if r.Section == SectionDM {
				out = append(out, "N:dm")
			} else {
				out = append(out, "N:chan")
			}
		case RowConversation:
			out = append(out, "C:"+r.Conversation.Name)
		case RowEntry:
			out = append(out, map[EntryID]string{EntryFriends: "S:friends", EntryMoments: "S:moments", EntryMe: "S:me"}[r.Entry])
		}
	}
	return out
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Direct messages first, channels second, the foot last; each section is ordered
// by recent activity on its own and nothing crosses over.
func TestSidebarRowsSectionsAndOrder(t *testing.T) {
	got := describe(sidebarFixture().SidebarRows())
	want := []string{
		"H:dm", "N:dm", "C:bob", "C:carol", // 300 then 100
		"H:chan", "N:chan", "C:design", "C:general", "C:random", // 900, 500, 200
		"S:friends", "S:moments", "S:me",
	}
	if !eq(got, want) {
		t.Fatalf("rows = %v\nwant   %v", got, want)
	}
}

// A new message moves a conversation to the top of its own section only.
func TestActivityReordersWithinTheSection(t *testing.T) {
	s := sidebarFixture()
	s.Conversations["d1"].LastMessageAt = 10_000 // carol was the quiet DM

	got := describe(s.SidebarRows())
	if got[2] != "C:carol" {
		t.Fatalf("carol did not reach the top of the DMs: %v", got)
	}
	// Carol did not jump above the channel header, and the channels are unchanged.
	if got[4] != "H:chan" || got[6] != "C:design" {
		t.Fatalf("a DM's activity disturbed the channels: %v", got)
	}
}

// Each section keeps its header and its "new" button even with nothing in it;
// the other section is untouched.
func TestEmptySectionsKeepTheirHeader(t *testing.T) {
	s := NewAppState()
	c := s.EnsureConversation("c1")
	c.Name, c.Kind = "general", protocol.KindChannel

	got := describe(s.SidebarRows())
	want := []string{"H:dm", "N:dm", "H:chan", "N:chan", "C:general", "S:friends", "S:moments", "S:me"}
	if !eq(got, want) {
		t.Fatalf("no DMs: rows = %v\nwant %v", got, want)
	}

	s = NewAppState()
	d := s.EnsureConversation("d1")
	d.Name, d.Kind = "carol", protocol.KindDM
	got = describe(s.SidebarRows())
	want = []string{"H:dm", "N:dm", "C:carol", "H:chan", "N:chan", "S:friends", "S:moments", "S:me"}
	if !eq(got, want) {
		t.Fatalf("no channels: rows = %v\nwant %v", got, want)
	}

	got = describe(NewAppState().SidebarRows())
	want = []string{"H:dm", "N:dm", "H:chan", "N:chan", "S:friends", "S:moments", "S:me"}
	if !eq(got, want) {
		t.Fatalf("nothing at all: rows = %v\nwant %v", got, want)
	}
}

// The filter narrows both sections together and never removes a foot entry; the
// "new" buttons are not matches and go while it has text.
func TestFilterNarrowsBothSectionsAndSparesTheFoot(t *testing.T) {
	s := sidebarFixture()
	// "r" is in carol, general and random; bob and design do not have one.
	s.Filter = []rune("r")

	got := describe(s.SidebarRows())
	want := []string{
		"H:dm", "C:carol",
		"H:chan", "C:general", "C:random",
		"S:friends", "S:moments", "S:me",
	}
	if !eq(got, want) {
		t.Fatalf("filter 'r': rows = %v\nwant %v", got, want)
	}

	s.Filter = []rune("zzz")
	got = describe(s.SidebarRows())
	want = []string{"H:dm", "H:chan", "S:friends", "S:moments", "S:me"}
	if !eq(got, want) {
		t.Fatalf("filter matching nothing: rows = %v\nwant %v", got, want)
	}
}

// Headers and placeholders cannot hold the cursor; conversations and entries can.
func TestSelectableRows(t *testing.T) {
	for _, r := range describeKinds() {
		if r.sel != r.row.Selectable() {
			t.Errorf("%s: Selectable() = %v, want %v", r.name, r.row.Selectable(), r.sel)
		}
	}
}

type kindCase struct {
	name string
	row  Row
	sel  bool
}

func describeKinds() []kindCase {
	return []kindCase{
		{"header", Row{Kind: RowHeader}, false},
		{"new button", Row{Kind: RowNew}, true},
		{"conversation", Row{Kind: RowConversation}, true},
		{"entry", Row{Kind: RowEntry}, true},
	}
}

// The cursor starts on the first conversation, not on a header or a "new"
// button.
func TestFirstSelectableSkipsHeaders(t *testing.T) {
	rows := sidebarFixture().SidebarRows()
	i := FirstSelectable(rows)
	if i != 2 || rows[i].Conversation.Name != "bob" {
		t.Fatalf("first selectable = %d (%v)", i, describe(rows))
	}

	// With no conversation it starts on the first "new" button.
	empty := NewAppState().SidebarRows()
	i = FirstSelectable(empty)
	if empty[i].Kind != RowNew || empty[i].Section != SectionDM {
		t.Fatalf("with no conversations the cursor should start on the new-DM button, got row %d %v", i, describe(empty))
	}
}

// Down goes DMs -> channels -> settings -> help -> about, jumping the headers,
// and wraps round to the top; up is the mirror image.
func TestCursorWalksAcrossSectionsAndWraps(t *testing.T) {
	rows := sidebarFixture().SidebarRows()

	i := FirstSelectable(rows)
	var seen []string
	for {
		seen = append(seen, describe(rows[i : i+1])[0])
		if rows[i].Kind == RowEntry && rows[i].Entry == EntryMe {
			break
		}
		i = NextSelectable(rows, i, +1)
	}
	want := []string{"C:bob", "C:carol", "N:chan", "C:design", "C:general", "C:random", "S:friends", "S:moments", "S:me"}
	if !eq(seen, want) {
		t.Fatalf("walking down visited %v\nwant %v", seen, want)
	}

	// Down at the last entry wraps to the first row, the new-DM button, and up
	// from there wraps back.
	top := NextSelectable(rows, i, +1)
	if rows[top].Kind != RowNew || rows[top].Section != SectionDM {
		t.Fatalf("down past the end went to %v, want the new-DM button", describe(rows[top:top+1]))
	}
	if n := NextSelectable(rows, top, -1); n != i {
		t.Fatalf("up from the top went to %v, want me", describe(rows[n:n+1]))
	}

	// Walking up visits everything in reverse, down to the top.
	seen = nil
	for {
		seen = append(seen, describe(rows[i : i+1])[0])
		if i == top {
			break
		}
		i = NextSelectable(rows, i, -1)
	}
	wantUp := []string{"S:me", "S:moments", "S:friends", "C:random", "C:general", "C:design", "N:chan", "C:carol", "C:bob", "N:dm"}
	if !eq(seen, wantUp) {
		t.Fatalf("walking up visited %v\nwant %v", seen, wantUp)
	}
}

// In an empty sidebar the "new" buttons are where the cursor goes; up from the
// first one wraps round to the foot.
func TestCursorStartsOnTheNewButtonsWhenEmpty(t *testing.T) {
	rows := NewAppState().SidebarRows() // H:dm N:dm H:chan N:chan S S S
	i := FirstSelectable(rows)
	if rows[i].Kind != RowNew || rows[i].Section != SectionDM {
		t.Fatalf("start = %v", describe(rows))
	}
	if n := NextSelectable(rows, i, +1); rows[n].Kind != RowNew || rows[n].Section != SectionChannel {
		t.Fatalf("down from the new-DM button went to %v", describe(rows[n:n+1]))
	}
	if n := NextSelectable(rows, i, -1); rows[n].Kind != RowEntry || rows[n].Entry != EntryMe {
		t.Fatalf("up from the first selectable row went to %v, want me", describe(rows[n:n+1]))
	}
	// A list with a single selectable row stays put either way.
	one := []Row{{Kind: RowHeader}, {Kind: RowEntry, Entry: EntryMe}}
	if NextSelectable(one, 1, +1) != 1 || NextSelectable(one, 1, -1) != 1 {
		t.Fatal("a lone selectable row moved")
	}
}

// An out-of-range cursor (a stale index after the list shrank) is pulled back to
// the first selectable row rather than panicking.
func TestNextSelectableToleratesAStaleIndex(t *testing.T) {
	rows := sidebarFixture().SidebarRows()
	for _, stale := range []int{-5, 999} {
		got := NextSelectable(rows, stale, +1)
		if got < 0 || got >= len(rows) || !rows[got].Selectable() {
			t.Errorf("stale %d -> %d (%v)", stale, got, describe(rows))
		}
	}
	if NextSelectable(nil, 0, 1) != -1 {
		t.Error("an empty row set should report no row")
	}
}

func TestIndexOfConversation(t *testing.T) {
	rows := sidebarFixture().SidebarRows()
	i := IndexOfConversation(rows, "c1")
	if i < 0 || rows[i].Conversation.Name != "general" {
		t.Fatalf("index of c1 = %d", i)
	}
	if IndexOfConversation(rows, "nope") != -1 {
		t.Fatal("an unknown id should not be found")
	}
}

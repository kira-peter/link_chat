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
		case RowEmpty:
			if r.Section == SectionDM {
				out = append(out, "E:dm")
			} else {
				out = append(out, "E:chan")
			}
		case RowConversation:
			out = append(out, "C:"+r.Conversation.Name)
		case RowEntry:
			out = append(out, map[EntryID]string{EntrySettings: "S:settings", EntryHelp: "S:help", EntryAbout: "S:about"}[r.Entry])
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
		"H:dm", "C:bob", "C:carol", // 300 then 100
		"H:chan", "C:design", "C:general", "C:random", // 900, 500, 200
		"S:settings", "S:help", "S:about",
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
	if got[1] != "C:carol" {
		t.Fatalf("carol did not reach the top of the DMs: %v", got)
	}
	// She did not jump above the channel header, and the channels are unchanged.
	if got[3] != "H:chan" || got[4] != "C:design" {
		t.Fatalf("a DM's activity disturbed the channels: %v", got)
	}
}

// Each section with nothing in it keeps its header and gets its own placeholder;
// the other section is untouched.
func TestEmptySectionsKeepTheirHeader(t *testing.T) {
	s := NewAppState()
	c := s.EnsureConversation("c1")
	c.Name, c.Kind = "general", protocol.KindChannel

	got := describe(s.SidebarRows())
	want := []string{"H:dm", "E:dm", "H:chan", "C:general", "S:settings", "S:help", "S:about"}
	if !eq(got, want) {
		t.Fatalf("no DMs: rows = %v\nwant %v", got, want)
	}

	s = NewAppState()
	d := s.EnsureConversation("d1")
	d.Name, d.Kind = "carol", protocol.KindDM
	got = describe(s.SidebarRows())
	want = []string{"H:dm", "C:carol", "H:chan", "E:chan", "S:settings", "S:help", "S:about"}
	if !eq(got, want) {
		t.Fatalf("no channels: rows = %v\nwant %v", got, want)
	}

	got = describe(NewAppState().SidebarRows())
	want = []string{"H:dm", "E:dm", "H:chan", "E:chan", "S:settings", "S:help", "S:about"}
	if !eq(got, want) {
		t.Fatalf("nothing at all: rows = %v\nwant %v", got, want)
	}
}

// The filter narrows both sections together and never removes a foot entry.
func TestFilterNarrowsBothSectionsAndSparesTheFoot(t *testing.T) {
	s := sidebarFixture()
	// "r" is in carol, general and random; bob and design do not have one.
	s.Filter = []rune("r")

	got := describe(s.SidebarRows())
	want := []string{
		"H:dm", "C:carol",
		"H:chan", "C:general", "C:random",
		"S:settings", "S:help", "S:about",
	}
	if !eq(got, want) {
		t.Fatalf("filter 'r': rows = %v\nwant %v", got, want)
	}

	s.Filter = []rune("zzz")
	got = describe(s.SidebarRows())
	want = []string{"H:dm", "E:dm", "H:chan", "E:chan", "S:settings", "S:help", "S:about"}
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
		{"empty placeholder", Row{Kind: RowEmpty}, false},
		{"conversation", Row{Kind: RowConversation}, true},
		{"entry", Row{Kind: RowEntry}, true},
	}
}

// The cursor starts on the first thing that can be selected, not on a header.
func TestFirstSelectableSkipsHeaders(t *testing.T) {
	rows := sidebarFixture().SidebarRows()
	i := FirstSelectable(rows)
	if i != 1 || rows[i].Conversation.Name != "bob" {
		t.Fatalf("first selectable = %d (%v)", i, describe(rows))
	}

	// With nothing but placeholders above them, the foot is where it lands.
	empty := NewAppState().SidebarRows()
	i = FirstSelectable(empty)
	if empty[i].Kind != RowEntry || empty[i].Entry != EntrySettings {
		t.Fatalf("with no conversations the cursor should start on Settings, got row %d %v", i, describe(empty))
	}
}

// Down goes DMs -> channels -> settings -> help -> about, jumping the header and
// stopping at the end; up is the mirror image.
func TestCursorWalksAcrossSectionsAndStopsAtTheEnds(t *testing.T) {
	rows := sidebarFixture().SidebarRows()

	i := FirstSelectable(rows)
	var seen []string
	for {
		seen = append(seen, describe(rows[i : i+1])[0])
		n := NextSelectable(rows, i, +1)
		if n == i {
			break
		}
		i = n
	}
	want := []string{"C:bob", "C:carol", "C:design", "C:general", "C:random", "S:settings", "S:help", "S:about"}
	if !eq(seen, want) {
		t.Fatalf("walking down visited %v\nwant %v", seen, want)
	}

	// Pressing down again at the last entry does nothing.
	if n := NextSelectable(rows, i, +1); n != i {
		t.Fatalf("down past the end moved the cursor %d -> %d", i, n)
	}

	// And back up to the first conversation, then no further.
	seen = nil
	for {
		seen = append(seen, describe(rows[i : i+1])[0])
		n := NextSelectable(rows, i, -1)
		if n == i {
			break
		}
		i = n
	}
	wantUp := []string{"S:about", "S:help", "S:settings", "C:random", "C:general", "C:design", "C:carol", "C:bob"}
	if !eq(seen, wantUp) {
		t.Fatalf("walking up visited %v\nwant %v", seen, wantUp)
	}
}

// Placeholders are jumped like headers, so the cursor never lands on a row that
// does nothing.
func TestCursorJumpsPlaceholders(t *testing.T) {
	rows := NewAppState().SidebarRows() // H:dm E:dm H:chan E:chan S S S
	i := FirstSelectable(rows)
	if rows[i].Entry != EntrySettings {
		t.Fatalf("start = %v", describe(rows))
	}
	if n := NextSelectable(rows, i, -1); n != i {
		t.Fatalf("up from the first selectable row moved to %d", n)
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

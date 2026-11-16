package client

import (
	"testing"

	"link-chat/pkg/protocol"
)

func TestPageForEveryKindOfRow(t *testing.T) {
	st := NewAppState()
	add := func(id, name, kind string, joined bool, at int64) {
		c := st.EnsureConversation(id)
		c.Name, c.Kind, c.Joined, c.LastMessageAt = name, kind, joined, at
	}
	add("d1", "carol", protocol.KindDM, true, 300)
	add("c1", "joined", protocol.KindChannel, true, 200)
	add("c2", "notyet", protocol.KindChannel, false, 100)
	rows := st.SidebarRows()

	for _, c := range []struct {
		name string
		idx  int
		want Page
	}{
		{"a direct message", IndexOfConversation(rows, "d1"), PageConversation},
		{"a joined channel", IndexOfConversation(rows, "c1"), PageConversation},
		{"a channel not joined", IndexOfConversation(rows, "c2"), PageChannelPreview},
		{"settings", IndexOfEntry(rows, EntrySettings), PageSettings},
		{"help", IndexOfEntry(rows, EntryHelp), PageHelp},
		{"about", IndexOfEntry(rows, EntryAbout), PageAbout},
		{"no row", -1, PageWelcome},
		{"past the end", len(rows), PageWelcome},
		{"a header", 0, PageWelcome},
	} {
		if got := PageFor(rows, c.idx); got != c.want {
			t.Errorf("%s (row %d): page %v, want %v", c.name, c.idx, got, c.want)
		}
	}

	// A filter that matches nothing leaves only the foot; nothing above it.
	st.Filter = []rune("zzz")
	rows = st.SidebarRows()
	if got := PageFor(rows, FirstSelectable(rows)); got != PageSettings {
		t.Errorf("with every conversation filtered out the first selectable row gives %v, want settings", got)
	}
}

func TestEntryPagesMapBack(t *testing.T) {
	for _, e := range Entries {
		rows := []Row{{Kind: RowEntry, Entry: e}}
		p := PageFor(rows, 0)
		if !p.IsEntryPage() {
			t.Errorf("entry %v gives %v, not an entry page", e, p)
		}
		if got, ok := p.EntryOf(); !ok || got != e {
			t.Errorf("page %v maps back to %v (%v), want %v", p, got, ok, e)
		}
	}
	if PageConversation.IsEntryPage() || PageWelcome.IsEntryPage() {
		t.Error("a non-entry page claims to be an entry page")
	}
}

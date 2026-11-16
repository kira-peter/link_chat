package client

import (
	"sort"
	"strings"

	"link-chat/pkg/protocol"
)

// RowKind says what a row of the sidebar is.
type RowKind int

const (
	// RowHeader is a section title ("Direct messages", "Channels"). It is drawn
	// but can never be selected: the cursor skips it.
	RowHeader RowKind = iota
	// RowConversation is one conversation.
	RowConversation
	// RowNew is the button at the top of each section that starts a new one of
	// its kind: a direct message, a channel. It is selectable, so ↑↓ reach it
	// like any row, and → or Enter opens its prompt — the same as Alt+O / Alt+N.
	RowNew
	// RowEntry is a fixed entry at the foot of the sidebar (settings, help,
	// about). It is selectable like a conversation but opens a dialog.
	RowEntry
)

// Section names which of the two conversation groups a row belongs to.
type Section int

const (
	SectionNone Section = iota
	SectionDM
	SectionChannel
)

// EntryID identifies a foot entry.
type EntryID int

const (
	EntrySettings EntryID = iota
	EntryHelp
	EntryAbout
	entryCount
)

// Row is one line of the sidebar, in display order.
//
// The sidebar used to be a flat list of conversations indexed by position. With
// two sections and a foot, "the third thing on screen" is no longer "the third
// conversation", so the cursor indexes this sequence instead and every consumer —
// drawing, arrow keys, Enter — reads the same rows
// (design.md decision 3).
type Row struct {
	Kind         RowKind
	Section      Section
	Conversation *Conversation // RowConversation only
	Entry        EntryID       // RowEntry only
}

// Selectable reports whether the cursor may rest on this row.
func (r Row) Selectable() bool {
	return r.Kind == RowConversation || r.Kind == RowEntry || r.Kind == RowNew
}

// Entries are the foot entries, top to bottom.
var Entries = []EntryID{EntrySettings, EntryHelp, EntryAbout}

// SidebarRows builds the sidebar: direct messages, then channels, then the foot.
//
// Each section is ordered by recent activity on its own, so a busy channel never
// pushes a quiet direct message out of the top block. The filter narrows both
// sections at once; the foot entries are not conversations and are never
// filtered away. Each section opens with its "new" button, which a filter hides:
// while typing a filter the rows are the matches.
func (s *AppState) SidebarRows() []Row {
	filter := strings.ToLower(string(s.Filter))

	var dms, chans []*Conversation
	for _, id := range s.Order {
		cv := s.Conversations[id]
		if cv == nil {
			continue
		}
		if filter != "" && !strings.Contains(strings.ToLower(cv.Name), filter) {
			continue
		}
		if cv.Kind == protocol.KindDM {
			dms = append(dms, cv)
		} else {
			chans = append(chans, cv)
		}
	}
	byActivity := func(g []*Conversation) {
		sort.SliceStable(g, func(i, j int) bool {
			if g[i].LastMessageAt != g[j].LastMessageAt {
				return g[i].LastMessageAt > g[j].LastMessageAt
			}
			return g[i].Name < g[j].Name
		})
	}
	byActivity(dms)
	byActivity(chans)

	rows := make([]Row, 0, len(dms)+len(chans)+2+int(entryCount)+2)
	rows = append(rows, Row{Kind: RowHeader, Section: SectionDM})
	if filter == "" {
		rows = append(rows, Row{Kind: RowNew, Section: SectionDM})
	}
	for _, cv := range dms {
		rows = append(rows, Row{Kind: RowConversation, Section: SectionDM, Conversation: cv})
	}
	rows = append(rows, Row{Kind: RowHeader, Section: SectionChannel})
	if filter == "" {
		rows = append(rows, Row{Kind: RowNew, Section: SectionChannel})
	}
	for _, cv := range chans {
		rows = append(rows, Row{Kind: RowConversation, Section: SectionChannel, Conversation: cv})
	}
	for _, e := range Entries {
		rows = append(rows, Row{Kind: RowEntry, Entry: e})
	}
	return rows
}

// FirstSelectable returns the index of the row the cursor starts on, or -1 if
// there is none: the first conversation, so that opening the app or clearing a
// filter lands on something to read rather than on a "new" button; with no
// conversation at all, the first row the cursor may rest on.
func FirstSelectable(rows []Row) int {
	for i, r := range rows {
		if r.Kind == RowConversation {
			return i
		}
	}
	for i, r := range rows {
		if r.Selectable() {
			return i
		}
	}
	return -1
}

// IndexOfNew is the row index of a section's "new" button, or -1.
func IndexOfNew(rows []Row, sec Section) int {
	for i, r := range rows {
		if r.Kind == RowNew && r.Section == sec {
			return i
		}
	}
	return -1
}

// NextSelectable moves the cursor from i by delta (+1 down, -1 up), skipping rows
// that cannot be selected. The list wraps: down from the last selectable row
// goes to the first, up from the first to the last. With only one selectable
// row it stays where it is.
func NextSelectable(rows []Row, i, delta int) int {
	if len(rows) == 0 {
		return -1
	}
	if i < 0 || i >= len(rows) {
		i = FirstSelectable(rows)
		if i < 0 {
			return -1
		}
	}
	step := 1
	if delta < 0 {
		step = -1
	}
	n := len(rows)
	for k := 1; k < n; k++ {
		j := ((i+step*k)%n + n) % n
		if rows[j].Selectable() {
			return j
		}
	}
	return i
}

// IndexOfConversation returns the row index of a conversation, or -1.
func IndexOfConversation(rows []Row, id string) int {
	for i, r := range rows {
		if r.Kind == RowConversation && r.Conversation != nil && r.Conversation.ID == id {
			return i
		}
	}
	return -1
}

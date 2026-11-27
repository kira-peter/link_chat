package client

import "link-chat/pkg/protocol"

// Page is what the right-hand side shows.
//
// The right side used to show exactly one kind of thing, the open conversation.
// Now the row under the sidebar cursor decides it: a conversation, a preview of a
// channel the user has not joined, or one of the foot entries' pages
// (spec tui-chat-client / 移动即打开会话、选中入口即显示页面).
type Page int

const (
	// PageWelcome is shown when nothing is selected: no conversations yet and the
	// cursor nowhere, or a filter that matches nothing.
	PageWelcome Page = iota
	// PageConversation is a direct message or a channel the user belongs to.
	PageConversation
	// PageChannelPreview is a channel the user has not joined. Looking at it does
	// not join it; only an explicit → or Enter does.
	PageChannelPreview
	PageSettings
	PageHelp
	PageAbout
	// PageNewDM and PageNewChannel belong to the sections' "new" buttons: a page
	// with one field, the peer's username or the channel's name. Entering it folds
	// the list like a conversation does, and the field takes the keyboard.
	PageNewDM
	PageNewChannel
	// PageMoments is the moments entry's page: timeline, notifications and
	// friends (spec tui-chat-client / 朋友圈页面).
	PageMoments
	// PageProfile is someone's profile. No row owns it: it is opened from the
	// moments page and goes back there.
	PageProfile
	// PageFriends is the friends entry's page.
	PageFriends
	// PageMe is the "me" entry's page: the user's card and a menu that leads
	// to editing the profile, their moments, settings, help and about, and the
	// account actions. Settings, help and about belong to it.
	PageMe
	// PageEditChannel is the creator's form for the active channel.
	PageEditChannel
)

// PageFor is the page a sidebar row puts on the right. Rows that cannot be
// selected, and no row at all, give the welcome page.
func PageFor(rows []Row, selected int) Page {
	if selected < 0 || selected >= len(rows) {
		return PageWelcome
	}
	r := rows[selected]
	switch r.Kind {
	case RowConversation:
		if r.Conversation == nil {
			return PageWelcome
		}
		if r.Conversation.Kind == protocol.KindChannel && !r.Conversation.Joined {
			return PageChannelPreview
		}
		return PageConversation
	case RowNew:
		if r.Section == SectionDM {
			return PageNewDM
		}
		return PageNewChannel
	case RowEntry:
		switch r.Entry {
		case EntryMe:
			return PageMe
		case EntryMoments:
			return PageMoments
		case EntryFriends:
			return PageFriends
		}
	}
	return PageWelcome
}

// IsEntryPage reports whether a page belongs to a foot entry.
func (p Page) IsEntryPage() bool {
	_, ok := p.EntryOf()
	return ok
}

// EntryOf is the foot entry a page belongs to. Settings, help and about are
// reached through "me", and so belong to it.
func (p Page) EntryOf() (EntryID, bool) {
	switch p {
	case PageMe, PageSettings, PageHelp, PageAbout:
		return EntryMe, true
	case PageMoments, PageProfile:
		return EntryMoments, true
	case PageFriends:
		return EntryFriends, true
	}
	return 0, false
}

// IsNewPage reports whether a page belongs to a section's "new" button.
func (p Page) IsNewPage() bool { return p == PageNewDM || p == PageNewChannel }

// SectionOf is the section a "new" page creates in.
func (p Page) SectionOf() (Section, bool) {
	switch p {
	case PageNewDM:
		return SectionDM, true
	case PageNewChannel:
		return SectionChannel, true
	}
	return SectionNone, false
}

// IndexOfEntry is the row index of a foot entry, or -1.
func IndexOfEntry(rows []Row, e EntryID) int {
	for i, r := range rows {
		if r.Kind == RowEntry && r.Entry == e {
			return i
		}
	}
	return -1
}

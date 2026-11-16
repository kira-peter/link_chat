package app

import (
	"time"

	"link-chat/pkg/client"
	"link-chat/pkg/i18n"
	"link-chat/pkg/tui/views"
	"link-chat/pkg/tui/widget"
)

// Pages.
//
// The right-hand side shows a page, and while the list has the keyboard the row
// under its cursor decides which: moving the cursor is choosing, no Enter needed
// (spec tui-chat-client / 移动即打开会话). → (or Enter) then hands the keyboard to
// that page, and the list folds away exactly as it does for a conversation.
//
// Moving the cursor only does cheap, local, undoable things: it changes what is
// drawn and, for a conversation, which one is open. Anything that reaches the
// server or another person — fetching history, joining, clearing the unread count
// — waits until the cursor has rested on a row (design.md decision 2).

// settleDelay is how long the cursor must rest on a conversation before its
// history is fetched and it counts as read. Long enough that holding ↓ skims past
// rows without touching them, short enough to feel immediate when you stop.
const settleDelay = 150 * time.Millisecond

// pendingSettle is a conversation waiting for the cursor to rest on it.
type pendingSettle struct {
	id string
	at time.Time
}

// page is the page currently on the right. A conversation that is open with no
// other page chosen is what the right side shows: everything that opens a
// conversation without going through the list — creating a channel, starting a
// direct message — gets its conversation on the right without having to know
// about pages.
func (a *App) page() client.Page {
	if a.ui.Page == client.PageWelcome && a.st.ActiveID != "" && a.st.Active() != nil {
		return client.PageConversation
	}
	return a.ui.Page
}

// syncPage makes the page follow the cursor while the list has the keyboard. With
// the keyboard on the right the page is whatever was handed over, and the cursor
// has no say.
//
// Moving onto a conversation opens it locally at once — draft swapped, the title
// changed — and schedules the network side for when the cursor rests.
func (a *App) syncPage() {
	if a.st.Focus != client.FocusList {
		return
	}
	rows := a.st.SidebarRows()
	a.clampSelection(rows)
	page := client.PageFor(rows, a.ui.Selected)
	// The conversation already open is never a preview: opening a channel joins
	// it, so if it is open the user is in it, whatever a stale flag says.
	if page == client.PageChannelPreview && rows[a.ui.Selected].Conversation.ID == a.st.ActiveID {
		page = client.PageConversation
	}
	prev := a.ui.Page
	a.ui.Page = page
	if page.IsNewPage() && page != prev {
		// Each "new" page starts with an empty field.
		a.ui.NewPage = views.NewPageModel{}
	}

	switch page {
	case client.PageConversation:
		id := rows[a.ui.Selected].Conversation.ID
		switch {
		case id != a.st.ActiveID:
			a.showConversation(id)
		case prev != client.PageConversation:
			// Back onto the conversation that stayed open behind a preview or an
			// entry page: it is shown again, so resting here settles it again.
			a.ui.pending = &pendingSettle{id: id, at: nowTime().Add(settleDelay)}
		}
	case client.PageChannelPreview:
		// A preview opens nothing: the conversation that was open stays open in the
		// background (its draft kept), but nothing is fetched or joined.
		a.ui.pending = nil
	default:
		a.ui.pending = nil
	}
	a.syncMarker()
}

// showConversation makes id the open conversation without any of the effects that
// need the cursor to rest: no history fetch, no join, no read marker.
func (a *App) showConversation(id string) {
	a.st.StashDraft()
	a.st.ActiveID = id
	a.st.LoadDraft(id)
	a.ui.Scroll = 0
	a.ui.pending = &pendingSettle{id: id, at: nowTime().Add(settleDelay)}
}

// settlePending carries out the deferred side of opening a conversation once the
// cursor has rested on it: it is marked read and its history fetched (joining
// first, which for a channel the user already belongs to is silent).
func (a *App) settlePending(now time.Time) {
	p := a.ui.pending
	if p == nil || now.Before(p.at) {
		return
	}
	a.ui.pending = nil
	if p.id != a.st.ActiveID {
		return
	}
	a.st.MarkRead(p.id)
	a.ui.NeedResume = true
	a.joinAndLoad(p.id)
}

// settleNow carries out a pending settle at once. Handing the keyboard to a
// conversation is a clear enough decision that there is no reason to wait.
func (a *App) settleNow() {
	if p := a.ui.pending; p != nil {
		p.at = time.Time{}
		a.settlePending(nowTime())
	}
}

// pendingIn is how long until a pending settle is due, so the main loop can wake
// for it.
func (a *App) pendingIn(now time.Time) (time.Duration, bool) {
	p := a.ui.pending
	if p == nil {
		return 0, false
	}
	if d := p.at.Sub(now); d > 0 {
		return d, true
	}
	return 0, true
}

// joinPreviewed joins the channel being previewed and opens it. It is the only way
// a preview turns into membership, and it happens because the user pressed → or
// Enter on it, never because the cursor passed over it.
func (a *App) joinPreviewed() {
	rows := a.st.SidebarRows()
	if a.ui.Selected < 0 || a.ui.Selected >= len(rows) || rows[a.ui.Selected].Conversation == nil {
		return
	}
	cv := rows[a.ui.Selected].Conversation
	// Joined locally at once; the server confirms with the join below, and a
	// refresh would say the same.
	cv.Joined = true
	a.ui.Page = client.PageConversation
	a.showConversation(cv.ID)
	a.settleNow()
}

// rightOpen reports whether there is a page the keyboard can be handed to.
func (a *App) rightOpen() bool {
	switch a.page() {
	case client.PageWelcome:
		return false
	case client.PageConversation:
		return a.st.ActiveID != ""
	}
	return true
}

// enterRight hands the keyboard to the page on the right: joining a previewed
// channel first, and putting the settings page on its first category.
func (a *App) enterRight() {
	// From the list the cursor decides what is entered, even if it was put there
	// without moving (a restored position, a test).
	if a.st.Focus == client.FocusList {
		a.syncPage()
	}
	switch a.page() {
	case client.PageChannelPreview:
		a.joinPreviewed()
	case client.PageConversation:
		a.settleNow()
	}
	a.moveFocus(client.FocusInput)
}

// showEntryPage puts an entry's page on the right with the cursor on its row and
// the keyboard on the page, which is what Alt+S and Alt+H do from anywhere.
func (a *App) showEntryPage(e client.EntryID) {
	rows := a.st.SidebarRows()
	if i := client.IndexOfEntry(rows, e); i >= 0 {
		a.ui.Selected = i
	}
	focus := a.st.Focus
	a.st.Focus = client.FocusList
	a.syncPage()
	a.st.Focus = focus
	a.moveFocus(client.FocusInput)
	a.syncFold()
	a.syncMarker()
}

// pageTitle is the title the page on the right shows centred on its first row,
// which is also what flies there when the list folds.
func (a *App) pageTitle() string {
	switch a.page() {
	case client.PageConversation:
		if cv := a.st.Active(); cv != nil {
			if cv.Name == "" {
				return i18n.OpenDM.Get(a.lang)
			}
			return cv.Name
		}
	case client.PageChannelPreview:
		if cv := a.previewed(); cv != nil {
			return cv.Name
		}
	case client.PageSettings:
		return i18n.SettingsTitle.Get(a.lang)
	case client.PageHelp:
		return i18n.HelpPageTitle.Get(a.lang)
	case client.PageAbout:
		return i18n.AboutTitle.Get(a.lang)
	case client.PageNewDM, client.PageNewChannel:
		return views.NewPageTitle(a.page() == client.PageNewChannel, a.lang)
	}
	return ""
}

// previewed is the channel under the cursor while it is being previewed.
func (a *App) previewed() *client.Conversation {
	rows := a.st.SidebarRows()
	if a.ui.Selected < 0 || a.ui.Selected >= len(rows) {
		return nil
	}
	return rows[a.ui.Selected].Conversation
}

// pageRowLine is the line within the sidebar of the row the page belongs to, for
// the flight's start.
func (a *App) pageRow() int {
	rows := a.st.SidebarRows()
	switch a.page() {
	case client.PageConversation:
		return client.IndexOfConversation(rows, a.st.ActiveID)
	case client.PageChannelPreview:
		return a.ui.Selected
	}
	if e, ok := a.page().EntryOf(); ok {
		return client.IndexOfEntry(rows, e)
	}
	if sec, ok := a.page().SectionOf(); ok {
		return client.IndexOfNew(rows, sec)
	}
	return -1
}

// pageTitleX is the column where the page's title starts at a fold. A
// conversation's title carries its member count beside the name and is centred
// as one line; the other pages centre their title alone.
func (a *App) pageTitleX(content widget.Rect, fold float64) (int, bool) {
	main := views.MainRectAt(content, fold)
	switch a.page() {
	case client.PageConversation:
		cv := a.st.Active()
		if cv == nil {
			return 0, false
		}
		return views.TitleX(main, cv, a.lang), true
	case client.PageWelcome:
		return 0, false
	}
	title := a.pageTitle()
	if title == "" {
		return 0, false
	}
	return views.PageTitleX(main, title), true
}

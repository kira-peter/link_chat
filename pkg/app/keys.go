package app

import (
	"context"
	"time"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/client"
	"link-chat/pkg/i18n"
	"link-chat/pkg/protocol"
	"link-chat/pkg/tui/views"
)

// handleEvent routes one terminal event.
func (a *App) handleEvent(ctx context.Context, ev tcell.Event) {
	switch e := ev.(type) {
	case *tcell.EventResize:
		// A resize can invalidate the colour profile assumption too, so re-detect.
		a.applyTheme()
		a.scr.Sync()
		return
	case *tcell.EventKey:
		a.handleKey(ctx, e)
	}
}

// handleKey owns the whole keyboard map.
//
// The rule that shapes it: printable characters belong to whatever text field
// has focus, and every command lives on Alt. A chat client exists to be typed
// into, so hijacking "s" or "q" would make the app unusable for the people who
// actually use it.
//
// Focus moves between regions with the arrow keys / Tab rather than with a
// command key, which is the arrangement a user already knows from every windowed
// application.
func (a *App) handleKey(ctx context.Context, ev *tcell.EventKey) {
	// A key during the sign-in entrance is meant for the main interface: the
	// entrance ends on the spot and the key is handled as usual
	// (spec tui-login-stage / 入场动画不阻塞输入).
	a.skipEntrance()

	if a.ui.Dialog != views.DialogNone {
		a.handleDialogKey(ctx, ev)
		return
	}

	alt := ev.Modifiers()&tcell.ModAlt != 0

	// Alt commands work from any region, so they need no focus juggling.
	if alt && a.handleAltCommand(ctx, ev) {
		return
	}

	// Ctrl+C always quits; it is the one convention nobody wants to relearn.
	if ev.Key() == tcell.KeyCtrlC {
		a.ui.Quitting = true
		return
	}

	// The list filter is a mode of its own: while active every printable rune
	// narrows the list rather than reaching anything else.
	if a.st.Filter != nil {
		a.handleFilterKey(ev)
		return
	}

	// Region switching.
	//
	// ← and → follow the screen: the list is on the left and the composer on the
	// right, so ← goes to the list and → goes to the composer. Pressing towards an
	// edge you are already at does nothing; it never wraps round to the far side.
	switch ev.Key() {
	case tcell.KeyTab:
		a.moveFocus(a.st.Focus.Cycle())
		return
	case tcell.KeyBacktab:
		a.moveFocus(a.st.Focus.CycleBack())
		return
	case tcell.KeyLeft:
		if a.shouldHandOffLeft() {
			a.moveFocus(a.st.Focus.Left())
			return
		}
	case tcell.KeyRight:
		if a.shouldHandOffRight() {
			a.moveFocus(a.st.Focus.Right())
			return
		}
	}

	if a.st.Focus == client.FocusInput {
		a.handleComposerKey(ctx, ev)
		return
	}
	a.handleListKey(ctx, ev)
}

// shouldHandOffLeft reports whether ← should move focus rather than do something
// local.
//
// In the composer, ← is first of all "move the caret": it only becomes "go to the
// list" once the caret has nowhere further left to go. That keeps the arrow keys
// from stealing editing, and it means a user need not clear their draft before
// stepping across. Nothing is lost on the way — the text and caret stay put, and
// → brings you back to exactly where you were (design.md decision 2).
func (a *App) shouldHandOffLeft() bool {
	switch a.st.Focus {
	case client.FocusInput:
		return a.st.InputCursor == 0
	default:
		// Already leftmost: the handoff is a no-op, and consuming the key here
		// stops it leaking into list navigation.
		return true
	}
}

// shouldHandOffRight is the mirror of shouldHandOffLeft.
func (a *App) shouldHandOffRight() bool {
	switch a.st.Focus {
	case client.FocusInput:
		// Rightmost region: only the caret is moved here, never the focus, so
		// → at the end of the text is a no-op rather than a jump to the far side.
		return false
	default:
		return true
	}
}

// moveFocus changes the focused region and starts the marker sliding to it.
//
// Every focus change goes through here so the transition is started in exactly
// one place, and so a move to the region that already has focus costs nothing.
//
// Two rules live here because every route to the composer — →, Tab, Esc, Enter —
// passes through:
//
//   - With no conversation open the composer has nothing to type into, so focus
//     cannot go there. Letting it would fold the list away and leave a blank
//     screen with nothing selectable (spec tui-chat-client / 没有打开的会话时向右).
//   - The fold follows focus, so it is re-aimed after every move.
func (a *App) moveFocus(to client.Focus) {
	if to == client.FocusInput && a.st.ActiveID == "" {
		return
	}
	if to == a.st.Focus {
		return
	}
	a.st.Focus = to
	if to == client.FocusList {
		a.selectOpenConversation()
	}
	a.startFocusTransition(to)
	a.syncFold()
	a.syncMarker()
}

// selectOpenConversation puts the sidebar cursor on the conversation that is open.
//
// The list comes back after the user has been in a conversation, and the row that
// belongs under the cursor is that conversation's — it is where the name flies
// back to, and it is the user's place in the list. Leaving the cursor wherever it
// last wandered would have the name land on one row while the highlight sits on
// another (spec tui-chat-client / 展开后选中项回到当前会话).
//
// With no open conversation, or one the current filter hides, the cursor is left
// alone rather than yanked to a row that is not there.
func (a *App) selectOpenConversation() {
	if a.st.ActiveID == "" {
		return
	}
	rows := a.st.SidebarRows()
	if i := client.IndexOfConversation(rows, a.st.ActiveID); i >= 0 && i != a.ui.Selected {
		a.ui.Selected = i
	}
}

// handleAltCommand runs the Alt-key commands. It reports whether the event was
// consumed, so the caller can fall through to normal handling.
//
// Alt is the deliberate choice: on a terminal it is one of the few modifiers
// that does not collide with text entry, and it is what the reference config
// screens use for their own commands.
func (a *App) handleAltCommand(ctx context.Context, ev *tcell.EventKey) bool {
	switch ev.Rune() {
	case 'n', 'N':
		a.openChannelDialog()
		return true
	case 'o', 'O':
		a.openDMDialog()
		return true
	case 's', 'S':
		a.openSettings()
		return true
	case 'h', 'H', '/':
		a.ui.Dialog = views.DialogHelp
		return true
	case 'f', 'F':
		a.st.Filter = []rune{}
		a.ui.Selected = 0
		return true
	case 't', 'T':
		a.toggleLanguage()
		return true
	case 'm', 'M':
		a.toggleMotion()
		return true
	case 'q', 'Q':
		a.ui.Quitting = true
		return true
	}

	switch ev.Key() {
	case tcell.KeyPgUp:
		a.scrollUp(20)
		return true
	case tcell.KeyPgDn:
		a.scrollDown(20)
		return true
	case tcell.KeyUp:
		a.scrollUp(1)
		return true
	case tcell.KeyDown:
		a.scrollDown(1)
		return true
	case tcell.KeyEnd:
		a.ui.Scroll = 0
		a.st.PendingNewMessage = false
		return true
	}
	return false
}

// openChannelDialog raises the create-channel prompt.
func (a *App) openChannelDialog() {
	if a.st.State != client.StateReady {
		return
	}
	a.ui.Dialog = views.DialogChannel
	a.ui.Prompt = views.PromptModel{
		Title: i18n.NewChannel.Get(a.lang),
		Label: i18n.ChannelName.Get(a.lang),
	}
}

// openDMDialog raises the direct-message prompt.
func (a *App) openDMDialog() {
	if a.st.State != client.StateReady {
		return
	}
	a.ui.Dialog = views.DialogDM
	a.ui.Prompt = views.PromptModel{
		Title: i18n.OpenDM.Get(a.lang),
		Label: i18n.PeerName.Get(a.lang),
	}
}

// scrollUp pans the transcript towards older messages.
func (a *App) scrollUp(n int) {
	a.ui.Scroll += n
	if a.ui.Scroll > maxScrollLimit {
		a.ui.Scroll = maxScrollLimit
	}
	// Reading history is an explicit act, so the stale jump hint is cleared.
	a.st.PendingNewMessage = false
}

// scrollDown pans back towards the newest message.
func (a *App) scrollDown(n int) {
	a.ui.Scroll -= n
	if a.ui.Scroll < 0 {
		a.ui.Scroll = 0
	}
	if a.ui.Scroll == 0 {
		a.st.PendingNewMessage = false
	}
}

// handleListKey handles navigation when the conversation list has focus.
//
// Only navigation keys live here. Everything actionable is an Alt command, so
// nothing in this function can be mistaken for typing.
func (a *App) handleListKey(ctx context.Context, ev *tcell.EventKey) {
	switch ev.Key() {
	case tcell.KeyUp:
		a.moveSelection(-1)
		return
	case tcell.KeyDown:
		a.moveSelection(1)
		return
	case tcell.KeyEnter:
		// Enter is the one exception to the Alt rule: "activate what is selected"
		// is unambiguous, and the list has nothing to type into.
		a.activateSelection()
		return
	case tcell.KeyEscape:
		a.moveFocus(client.FocusInput)
		return
	}

	switch ev.Rune() {
	case 'j':
		a.moveSelection(1)
	case 'k':
		a.moveSelection(-1)
	}
}

// activateSelection acts on the row under the cursor: a conversation is opened,
// a foot entry opens its dialog. Headers and placeholders are never selected, so
// there is nothing to do for them.
func (a *App) activateSelection() {
	rows := a.st.SidebarRows()
	a.clampSelection(rows)
	if a.ui.Selected < 0 || a.ui.Selected >= len(rows) {
		return
	}
	row := rows[a.ui.Selected]
	switch row.Kind {
	case client.RowConversation:
		a.openConversation(row.Conversation.ID)
		a.moveFocus(client.FocusInput)
	case client.RowEntry:
		a.openEntry(row.Entry)
	}
}

// openEntry opens the dialog behind a foot entry. These are the same dialogs the
// Alt shortcuts open, so the two routes cannot drift apart.
func (a *App) openEntry(e client.EntryID) {
	switch e {
	case client.EntrySettings:
		a.openSettings()
	case client.EntryHelp:
		a.ui.Dialog = views.DialogHelp
	case client.EntryAbout:
		a.ui.Dialog = views.DialogAbout
	}
}

// openSettings raises the settings dialog on its first value row.
func (a *App) openSettings() {
	a.ui.Dialog = views.DialogSettings
	a.ui.Settings.Selected = 1
}

// handleComposerKey handles typing and editing.
//
// This function must never interpret a printable rune as a command: the composer
// is the main surface of the application, and a letter that sometimes types and
// sometimes opens a dialog is a bug, not a shortcut.
func (a *App) handleComposerKey(ctx context.Context, ev *tcell.EventKey) {
	switch ev.Key() {
	case tcell.KeyEnter:
		if ev.Modifiers()&tcell.ModShift != 0 {
			a.st.InsertRunes([]rune{'\n'})
			return
		}
		a.sendCurrent(ctx)
		return
	case tcell.KeyEscape:
		a.moveFocus(client.FocusList)
		return
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		a.st.Backspace()
		return
	case tcell.KeyDelete:
		a.st.DeleteForward()
		return
	case tcell.KeyLeft:
		a.st.MoveCursor(-1)
		return
	case tcell.KeyRight:
		a.st.MoveCursor(1)
		return
	case tcell.KeyHome, tcell.KeyCtrlA:
		a.st.CursorHome()
		return
	case tcell.KeyEnd, tcell.KeyCtrlE:
		a.st.CursorEnd()
		return
	case tcell.KeyCtrlW:
		a.st.DeleteWordBefore()
		return
	case tcell.KeyCtrlU:
		a.st.RestoreInput("")
		return
	case tcell.KeyUp:
		// Up scrolls history only when the composer is empty; otherwise it moves
		// between the draft's lines, which is what a multi-line box should do.
		if len(a.st.Input) == 0 {
			a.scrollUp(1)
		}
		return
	case tcell.KeyDown:
		if len(a.st.Input) == 0 {
			a.scrollDown(1)
		}
		return
	case tcell.KeyPgUp:
		a.scrollUp(10)
		return
	case tcell.KeyPgDn:
		a.scrollDown(10)
		return
	case tcell.KeyRune:
		if r := ev.Rune(); r != 0 {
			a.st.InsertRunes([]rune{r})
		}
	}
}

// maxScrollLimit is a soft ceiling; render() clamps the real bound every frame
// from the rendered content height, so this only guards against runaway state.
const maxScrollLimit = 100000

// sendCurrent sends whatever is in the composer.
func (a *App) sendCurrent(ctx context.Context) {
	if a.st.State != client.StateReady || a.tr == nil {
		return
	}
	cv := a.st.Active()
	if cv == nil {
		return
	}
	text := a.st.InputText()
	if len(text) == 0 {
		return
	}
	// Keep the optimistic local echo cheap: clear the composer immediately, but
	// remember the text so a failure can put it back
	// (spec message-delivery / 发送失败保留草稿).
	sent := a.st.TakeInput()
	go func() {
		f, err := a.tr.Call(ctx, protocol.OpMessageSend, protocol.SendMessageReq{
			ConversationID: cv.ID,
			Body:           sent,
			ClientMsgID:    "",
		}, 20*time.Second)
		if err != nil {
			a.mu.Lock()
			a.st.RestoreInput(sent)
			a.st.InputErr = i18n.SendFailed.Get(a.lang) + " · " + i18n.RetryHint.Get(a.lang)
			a.mu.Unlock()
			a.markDirty()
			return
		}
		if f.Type == protocol.TypeError {
			code := errorCode(f)
			a.mu.Lock()
			a.st.RestoreInput(sent)
			a.st.InputErr = i18n.ErrText(code, "").Get(a.lang)
			a.mu.Unlock()
			a.markDirty()
			return
		}
		var me protocol.MessageEvent
		if err := decodeData(f, &me); err == nil && me.Message.ID != "" {
			a.mu.Lock()
			a.st.InsertMessage(me.Message, true)
			a.mu.Unlock()
			a.markDirty()
		}
	}()
}

// moveSelection moves the sidebar cursor one selectable row up or down.
//
// The cursor indexes the full row sequence, and headers and placeholders are
// skipped, so one press always lands on something that can be opened. At either
// end it stops rather than wrapping.
func (a *App) moveSelection(delta int) {
	rows := a.st.SidebarRows()
	a.clampSelection(rows)
	if next := client.NextSelectable(rows, a.ui.Selected, delta); next >= 0 && next != a.ui.Selected {
		a.ui.Selected = next
		// A new row starts its own sweep from the left.
		a.anim.listStart = nowTime()
	}
}

// clampSelection pulls the cursor back onto a selectable row. The row sequence
// changes under it — a filter narrows it, a conversation is deleted — so a stored
// index can point past the end or at a header.
func (a *App) clampSelection(rows []client.Row) {
	if a.ui.Selected >= 0 && a.ui.Selected < len(rows) && rows[a.ui.Selected].Selectable() {
		return
	}
	if i := client.FirstSelectable(rows); i >= 0 {
		a.ui.Selected = i
	}
}

// handleFilterKey edits the list filter.
func (a *App) handleFilterKey(ev *tcell.EventKey) {
	switch ev.Key() {
	case tcell.KeyEscape:
		a.st.Filter = nil
		a.ui.Selected = 0
		return
	case tcell.KeyEnter:
		// Enter accepts the filter: it is cleared and the highlighted row is acted
		// on, so the cursor must be resolved against the filtered rows first.
		rows := a.st.SidebarRows()
		a.clampSelection(rows)
		a.st.Filter = nil
		if a.ui.Selected >= 0 && a.ui.Selected < len(rows) {
			if row := rows[a.ui.Selected]; row.Kind == client.RowConversation {
				a.openConversation(row.Conversation.ID)
			}
		}
		return
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if len(a.st.Filter) == 0 {
			a.st.Filter = nil
			return
		}
		a.st.Filter = a.st.Filter[:len(a.st.Filter)-1]
		return
	case tcell.KeyUp:
		a.moveSelection(-1)
		return
	case tcell.KeyDown:
		a.moveSelection(1)
		return
	case tcell.KeyRune:
		if ev.Rune() != 0 {
			a.st.Filter = append(a.st.Filter, ev.Rune())
		}
	}
}

// toggleLanguage flips between Chinese and English, persists it, and repaints
// (spec tui-chat-client / 运行时切换语言).
func (a *App) toggleLanguage() {
	if a.lang == i18n.ZH {
		a.lang = i18n.EN
	} else {
		a.lang = i18n.ZH
	}
	a.st.Lang = string(a.lang)
	a.cfg.Language = string(a.lang)
	_ = SaveConfig(a.cfgPath, a.cfg)
}

// toggleRememberSession turns stored credentials on or off.
//
// Turning it off deletes the stored token immediately rather than waiting for the
// next logout: "don't remember me" that leaves a working credential on disk is
// not what the setting says.
func (a *App) toggleRememberSession() {
	a.mu.Lock()
	on := !a.cfg.Remember()
	a.cfg.SetRemember(on)
	remember := on
	current := a.st.Token
	a.mu.Unlock()

	if remember {
		if current != "" {
			a.persistToken(current)
		}
	} else {
		a.persistToken("")
	}
	_ = SaveConfig(a.cfgPath, a.cfg)
}

// toggleMotion flips the single animation switch and persists it.
//
// Turning animation off must not leave a marker stranded part-way through a
// slide: it is settled onto its target at once, which is the contract that one
// switch controls every animation (spec tui-design-system / 一个开关控制全部动画).
func (a *App) toggleMotion() {
	a.st.MotionOff = !a.st.MotionOff
	a.cfg.MotionOff = a.st.MotionOff
	_ = SaveConfig(a.cfgPath, a.cfg)

	if a.st.MotionOff {
		now := nowTime()
		a.anim.slide.MoveTo(a.regionX(a.st.Focus), now, 0, false)
		// A fold in flight lands on its target at once too: the one switch controls
		// every animation, and nothing may be left stranded part-way.
		a.settleFold()
	}
}

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
func (a *App) moveFocus(to client.Focus) {
	if to == a.st.Focus {
		return
	}
	a.st.Focus = to
	a.startFocusTransition(to)
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
		a.ui.Dialog = views.DialogSettings
		a.ui.Settings.Selected = 1
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
	visible := a.st.VisibleConversations()

	switch ev.Key() {
	case tcell.KeyUp:
		a.moveSelection(-1, len(visible))
		return
	case tcell.KeyDown:
		a.moveSelection(1, len(visible))
		return
	case tcell.KeyEnter:
		// Enter is the one exception to the Alt rule: "activate what is selected"
		// is unambiguous, and the list has nothing to type into.
		if a.ui.Selected >= 0 && a.ui.Selected < len(visible) {
			a.openConversation(visible[a.ui.Selected].ID)
			a.moveFocus(client.FocusInput)
		}
		return
	case tcell.KeyEscape:
		a.moveFocus(client.FocusInput)
		return
	}

	switch ev.Rune() {
	case 'j':
		a.moveSelection(1, len(visible))
	case 'k':
		a.moveSelection(-1, len(visible))
	}
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

// moveSelection clamps and wraps the list cursor.
func (a *App) moveSelection(delta, n int) {
	if n == 0 {
		a.ui.Selected = 0
		return
	}
	a.ui.Selected += delta
	if a.ui.Selected < 0 {
		a.ui.Selected = 0
	}
	if a.ui.Selected >= n {
		a.ui.Selected = n - 1
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
		a.st.Filter = nil
		visible := a.st.VisibleConversations()
		if a.ui.Selected < len(visible) {
			a.openConversation(visible[a.ui.Selected].ID)
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
		a.moveSelection(-1, len(a.st.VisibleConversations()))
		return
	case tcell.KeyDown:
		a.moveSelection(1, len(a.st.VisibleConversations()))
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
	}
}

package app

import (
	"link-chat/pkg/client"
	"link-chat/pkg/i18n"
	"link-chat/pkg/protocol"
	"link-chat/pkg/tui/layout"
	"link-chat/pkg/tui/views"
	"link-chat/pkg/tui/widget"
)

// render paints one frame and reports whether the app should exit.
func (a *App) render() bool {
	if a.ui.Quitting {
		return true
	}
	w, h := a.scr.Size()
	if w < 10 || h < 6 {
		// Below this there is nothing meaningful to draw; show the minimum the
		// layout needs rather than corrupting the screen
		// (spec tui-design-system / 极小窗口).
		a.scr.Clear()
		a.drawCentered(w, h, i18n.Unconfigured.Get(a.lang))
		a.scr.Show()
		return false
	}

	a.scr.Clear()
	chrome := views.Chrome{T: a.tokens}
	content := chrome.Frame(a.scr, a.st, a.lang, w, h)

	// Narrow windows collapse to a single pane instead of squeezing both into an
	// unreadable width (spec tui-chat-client / 窄屏折叠).
	compact := content.W < layout.MinViewWidth

	if compact {
		a.renderCompact(content)
	} else {
		a.renderWide(content)
	}

	chrome.StatusBar(a.scr, a.st, a.lang, w, h)

	// Modal layer last, so it is never overdrawn.
	a.renderDialog(w, h)

	a.scr.Show()
	return false
}

// renderWide draws the two-column layout, at whatever stage of folding it is in.
//
// Everything below is read from one number, fold: the sidebar's width, the
// conversation's extent, the divider's position and whether the sidebar's contents
// are still drawn. They cannot disagree because there is nothing else to disagree
// with (design.md decision 1).
func (a *App) renderWide(content widget.Rect) {
	now := nowTime()
	fold := a.foldNow(now)

	side := views.SidebarRectAt(content, fold)
	main := views.MainRectAt(content, fold)

	flight, flying := a.nameFlight(content, fold)

	// Past the threshold the sidebar is too narrow to show anything legibly; only
	// its blank space remains to shrink away.
	if views.SidebarContentVisible(fold) {
		list := views.List{T: a.tokens}
		lm := a.listModel(a.st.Focus)
		if flying {
			lm.FlyingID = a.st.ActiveID
		}
		list.Render(a.scr, side, lm, a.lang)
	}

	views.DrawDividerAt(a.scr, a.tokens, content, fold)

	a.renderTranscriptAndComposer(main, flying)

	// The flying name and the focus marker are drawn last, on top of everything:
	// both travel between regions, so neither belongs to any of them.
	if flying {
		views.FlightLayer{T: a.tokens}.Render(a.scr, content, flight, a.st.Focus, a.lang)
	}
	a.renderFocusOverlay(content)
}

// nameFlight works out where the open conversation's name is while the sidebar
// folds, and whether it is in the air at all.
//
// It leaves from the conversation's row in the fully open sidebar and lands on the
// title in the conversation, and between the two it follows the fold. The start is
// measured against the open layout, not the current one, so the departure point is
// the same on every frame however far the fold has got.
func (a *App) nameFlight(content widget.Rect, fold float64) (views.NameFlight, bool) {
	cv := a.st.Active()
	if cv == nil || !a.canFold() {
		return views.NameFlight{}, false
	}
	name := cv.Name
	if name == "" {
		name = i18n.OpenDM.Get(a.lang)
	}

	rows := a.st.SidebarRows()
	a.clampSelection(rows)
	line, onScreen := views.RowLine(rows, a.ui.Selected, cv.ID, content.H, a.st.Filter != nil, a.lang)
	if !onScreen {
		// Its row is scrolled out of the sidebar: there is nothing to fly from. The
		// name simply appears at the title when the fold ends.
		return views.NameFlight{}, false
	}

	fx, fy := views.SidebarRowAnchor(content, line)
	// The title sits at the conversation's left edge when fully folded.
	tx, ty := views.TitleAnchor(content, 1)
	f := views.NameFlight{Name: name, Fold: fold, FromX: fx, FromY: fy, ToX: tx, ToY: ty}
	return f, f.InFlight()
}

// listModel gathers what the sidebar draws. focus is passed in rather than read
// from state because the single-pane layout shows the list as focused whatever
// the underlying focus says.
func (a *App) listModel(focus client.Focus) views.ListModel {
	rows := a.st.SidebarRows()
	a.clampSelection(rows)
	return views.ListModel{
		Rows:     rows,
		Selected: a.ui.Selected,
		ActiveID: a.st.ActiveID,
		Filter:   a.st.Filter,
		Focus:    focus,
		Now:      nowMillis(),
		Username: a.st.Username,
		Shimmer:  a.animated() && a.listSweeping(),
		Elapsed:  nowTime().Sub(a.anim.listStart),
		Depth:    a.animDepth(),
	}
}

// renderFocusOverlay shows which region has the keyboard.
func (a *App) renderFocusOverlay(content widget.Rect) {
	now := nowTime()
	views.FocusOverlay{T: a.tokens}.Render(a.scr, content, views.FocusModel{
		Focus:   a.st.Focus,
		MarkerX: a.anim.slide.At(now),
		Active:  !a.anim.slide.Done(now),
	}, a.lang)
}

// renderCompact draws one pane at a time.
func (a *App) renderCompact(content widget.Rect) {
	if a.st.Focus == client.FocusList || a.st.ActiveID == "" {
		list := views.List{T: a.tokens}
		// Alone on screen, the list is by definition the focused region.
		list.Render(a.scr, content, a.listModel(client.FocusList), a.lang)
		a.drawStaticMarker(content, client.FocusList)
		return
	}
	a.renderTranscriptAndComposer(content, false)
	a.drawStaticMarker(content, client.FocusInput)
}

// drawStaticMarker puts the focus bar on the title row of a single-pane layout.
//
// With one pane on screen it is by definition the focused one, so there is
// nothing to slide between: the marker just sits at the pane's left edge.
func (a *App) drawStaticMarker(content widget.Rect, f client.Focus) {
	views.FocusOverlay{T: a.tokens}.RenderStatic(a.scr, content)
}

// renderTranscriptAndComposer draws the right-hand side. nameInFlight is true while
// the conversation's name is travelling to or from its title, so the title leaves
// that slot to the flight layer.
func (a *App) renderTranscriptAndComposer(main widget.Rect, nameInFlight bool) {
	inputLines := wrappedLineCount(a.st.InputText(), main.W-6)
	trRect, compRect := views.ComposerRect(main, inputLines)

	tr := views.Transcript{T: a.tokens}
	total := tr.Render(a.scr, trRect, views.TranscriptModel{
		Conversation:     a.st.Active(),
		UserID:           a.st.UserID,
		ScrollFromBottom: a.ui.Scroll,
		Now:              nowMillis(),
		HasNew:           a.st.PendingNewMessage,
		Focus:            a.st.Focus,
		NameInFlight:     nameInFlight,
	}, a.lang)

	// Clamp the scroll to what actually exists, so repeated PageUp at the top
	// does not accumulate an invisible offset.
	if maxScroll := total - trRect.H; a.ui.Scroll > maxScroll {
		a.ui.Scroll = maxInt(0, maxScroll)
	}
	if a.ui.Scroll < 0 {
		a.ui.Scroll = 0
	}

	comp := views.Composer{T: a.tokens}
	comp.Render(a.scr, compRect, views.ComposerModel{
		Input:       a.st.Input,
		Cursor:      a.st.InputCursor,
		Focus:       a.st.Focus,
		ReadOnly:    a.st.State != client.StateReady,
		Placeholder: i18n.ComposeHint.Get(a.lang),
		Err:         a.st.InputErr,
		MaxBytes:    4000,
		Shimmer:     a.animated() && a.shimmerActive(),
		Elapsed:     nowTime().Sub(a.anim.shimmerStart),
		Depth:       a.animDepth(),
	}, a.lang)
}

// renderDialog paints the modal layer when one is open.
func (a *App) renderDialog(w, h int) {
	d := views.Dialog{T: a.tokens}
	switch a.ui.Dialog {
	case views.DialogLogin:
		a.ui.Login.Shimmer = a.animated() && a.loginButtonSweeping()
		a.ui.Login.Elapsed = nowTime().Sub(a.anim.buttonStart)
		a.ui.Login.Depth = a.animDepth()
		d.RenderLogin(a.scr, w, h, a.ui.Login, a.lang)
	case views.DialogRegister:
		d.RenderRegister(a.scr, w, h, a.ui.Register, a.lang)
	case views.DialogRecover:
		d.RenderRecover(a.scr, w, h, a.ui.Recover, a.lang)
	case views.DialogChannel, views.DialogDM:
		d.RenderPrompt(a.scr, w, h, a.ui.Prompt, a.lang)
	case views.DialogHelp:
		d.RenderHelp(a.scr, w, h, a.lang)
	case views.DialogSettings:
		a.refreshSettingsRows()
		d.RenderSettings(a.scr, w, h, a.ui.Settings, a.st, a.lang)
	case views.DialogConfirm:
		d.RenderConfirm(a.scr, w, h, a.ui.Confirm, a.lang)
	case views.DialogSessionReplaced:
		d.RenderSessionReplaced(a.scr, w, h, a.ui.SessionReplaced, a.lang)
	case views.DialogAbout:
		d.RenderAbout(a.scr, w, h, Version, a.lang)
	}
}

// refreshSettingsRows rebuilds the settings list from current state.
func (a *App) refreshSettingsRows() {
	onOff := func(b bool) string {
		if b {
			return i18n.SetOn.Get(a.lang)
		}
		return i18n.SetOff.Get(a.lang)
	}
	a.ui.Settings.Rows = []views.SettingsRow{
		{Kind: 0, Label: i18n.SetGeneral.Get(a.lang)},
		{Kind: 1, Label: i18n.SetServer.Get(a.lang), Value: a.st.ServerURL},
		{Kind: 1, Label: i18n.SetUser.Get(a.lang), Value: a.st.Username},
		{Kind: 1, Label: i18n.SetCredStore.Get(a.lang), Value: credStoreName(a.creds, a.lang)},
		{Kind: 0, Label: i18n.SetAppearance.Get(a.lang)},
		{Kind: 1, Label: i18n.SetTheme.Get(a.lang), Value: a.st.ThemeName},
		{Kind: 1, Label: i18n.SetLanguage.Get(a.lang), Value: langName(a.lang)},
		{Kind: 1, Label: i18n.SetMotion.Get(a.lang), Value: onOff(!a.st.MotionOff)},
		{Kind: 1, Label: i18n.SetRemember.Get(a.lang), Value: rememberLabel(a.cfg.Remember(), a.lang)},
		{Kind: 0, Label: i18n.SetAccount.Get(a.lang)},
		{Kind: 2, Label: i18n.SetLogout.Get(a.lang)},
	}
}

// rememberLabel explains the consequence, not just the switch position: "off"
// alone does not tell anyone that it means typing a password every launch.
func rememberLabel(on bool, lang i18n.Lang) string {
	if on {
		return i18n.SetOn.Get(lang)
	}
	return i18n.SetOff.Get(lang) + " · " + i18n.RememberOffHint.Get(lang)
}

// credStoreName names the backend in use, so a fallback is never silent.
func credStoreName(s tokenStore, lang i18n.Lang) string {
	if s.backend != nil && s.backend.name() == "file" {
		return i18n.CredFile.Get(lang)
	}
	return i18n.CredKeyring.Get(lang)
}

func langName(l i18n.Lang) string {
	if l == i18n.EN {
		return "English"
	}
	return "中文"
}

// wrappedLineCount counts how many rows the composer needs for text.
func wrappedLineCount(text string, width int) int {
	if text == "" || width <= 0 {
		return 1
	}
	return len(layout.Wrap(text, width))
}

func (a *App) drawCentered(w, h int, s string) {
	c := widget.NewCanvas(a.scr, a.tokens, widget.Rect{X: 0, Y: 0, W: w, H: h})
	x := (w - layout.StringWidth(s)) / 2
	c.Text(maxInt(0, x), h/2, s, a.tokens.Faint())
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

var _ = protocol.Version

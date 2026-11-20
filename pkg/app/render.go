package app

import (
	"time"

	"link-chat/pkg/client"
	"link-chat/pkg/i18n"
	"link-chat/pkg/protocol"
	"link-chat/pkg/tui/layout"
	"link-chat/pkg/tui/views"
	"link-chat/pkg/tui/widget"
	"link-chat/pkg/version"
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

	// Before sign-in the panel stands alone on the stage: nothing of the main
	// interface is drawn behind it (spec tui-login-stage / 登录舞台只显示面板与背景).
	if a.onStage() {
		a.renderStage(w, h)
		a.scr.Show()
		return false
	}

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

	a.st.RightPage = a.page()
	chrome.StatusBar(a.scr, a.st, a.lang, w, h)

	// Modal layer last, so it is never overdrawn.
	a.renderDialog(w, h)

	// The sign-in entrance covers what it has not yet uncovered.
	a.renderEntrance(w, h)

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
			switch e, isEntry := a.page().EntryOf(); {
			case isEntry:
				lm.FlyingEntry, lm.FlyingIsEntry = e, true
			case a.page() == client.PageChannelPreview:
				if cv := a.previewed(); cv != nil {
					lm.FlyingID = cv.ID
				}
			case a.page().IsNewPage():
				sec, _ := a.page().SectionOf()
				lm.FlyingNew, lm.FlyingIsNew = sec, true
			default:
				lm.FlyingID = a.st.ActiveID
			}
		}
		list.Render(a.scr, side, lm, a.lang)
	}

	views.DrawDividerAt(a.scr, a.tokens, content, fold)

	a.renderPage(main, flying)
	a.paintPageSlide(main, now)

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
	if !a.canFold() || !a.rightOpen() {
		return views.NameFlight{}, false
	}
	name := a.pageTitle()
	if name == "" {
		return views.NameFlight{}, false
	}

	rows := a.st.SidebarRows()
	a.clampSelection(rows)
	line, onScreen := views.RowLineAt(rows, a.ui.Selected, a.pageRow(), content.H, a.st.Filter != nil)
	if !onScreen {
		// Its row is scrolled out of the sidebar: there is nothing to fly from. The
		// name simply appears at the title when the fold ends.
		return views.NameFlight{}, false
	}

	fx, fy := views.SidebarRowAnchor(content, line)
	// The title sits centred across the row when fully folded: the top centre.
	tx, ok := a.pageTitleX(content, 1)
	if !ok {
		return views.NameFlight{}, false
	}
	ty := views.MainRectAt(content, 1).Y
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
	// At rest the marker reads the title's position afresh. The title is centred,
	// so it moves when its text changes (a member count arriving after the
	// conversation opened), and a resting marker must not be left where the title
	// used to be. Only a marker in motion follows the slide.
	x := a.anim.slide.At(now)
	if a.anim.slide.Done(now) {
		x = a.regionX(a.st.Focus)
	}
	views.FocusOverlay{T: a.tokens}.Render(a.scr, content, views.FocusModel{
		Focus:   a.st.Focus,
		MarkerX: x,
		Active:  !a.anim.slide.Done(now),
	}, a.lang)
}

// renderCompact draws one pane at a time.
func (a *App) renderCompact(content widget.Rect) {
	if a.st.Focus == client.FocusList || !a.rightOpen() {
		list := views.List{T: a.tokens}
		// Alone on screen, the list is by definition the focused region.
		list.Render(a.scr, content, a.listModel(client.FocusList), a.lang)
		a.drawStaticMarker(content, client.FocusList)
		return
	}
	a.renderPage(content, false)
	a.drawStaticMarker(content, client.FocusInput)
}

// renderPage draws whichever page is on the right. nameInFlight is true while the
// page's title is travelling to or from its place, so the page leaves that slot to
// the flight layer.
func (a *App) renderPage(main widget.Rect, nameInFlight bool) {
	focused := a.st.Focus == client.FocusInput
	switch a.page() {
	case client.PageSettings:
		lay, tk, lang := a.previewLayout()
		views.SettingsPage{T: a.tokens}.Render(a.scr, main, views.SettingsPageModel{
			Nav: a.ui.SettingsNav, Focused: focused,
			Value: a.settingText, Choices: a.choiceLabels,
			Preview: lay, PreviewT: tk, PreviewLang: lang, NameInFlight: nameInFlight,
		}, a.lang)
	case client.PageHelp:
		views.HelpPage{T: a.tokens}.Render(a.scr, main, views.HelpPageModel{
			Focused: focused, NameInFlight: nameInFlight, Scroll: a.ui.HelpScroll,
		}, a.lang)
	case client.PageAbout:
		views.AboutPage{T: a.tokens}.Render(a.scr, main, version.String(), focused, nameInFlight, a.lang)
	case client.PageChannelPreview:
		views.ChannelPreviewPage{T: a.tokens}.Render(a.scr, main, a.previewed(), nowMillis(), focused, nameInFlight, a.lang)
	case client.PageNewDM, client.PageNewChannel:
		m := a.ui.NewPage
		m.Channel = a.page() == client.PageNewChannel
		m.Focused, m.NameInFlight = focused, nameInFlight
		views.NewPage{T: a.tokens}.Render(a.scr, main, m, a.lang)
	case client.PageWelcome:
		// Nothing chosen yet: the welcome, and no composer, since there is nothing
		// to type into (spec tui-chat-client / 登录后列表展开且不自动打开会话).
		views.Transcript{T: a.tokens}.Render(a.scr, main, views.TranscriptModel{Focus: a.st.Focus}, a.lang)
	default:
		a.renderTranscriptAndComposer(main, nameInFlight)
	}
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
		Layout:           a.cfg.Layout(),
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
		Input:        a.st.Input,
		Cursor:       a.st.InputCursor,
		Focus:        a.st.Focus,
		ReadOnly:     a.st.State != client.StateReady || a.st.Muted(nowTime().UnixMilli()),
		ReadOnlyText: a.mutedText(),
		Placeholder:  i18n.ComposeHint.Get(a.lang),
		Err:          a.st.InputErr,
		MaxBytes:     4000,
		Shimmer:      a.animated() && a.shimmerActive(),
		Elapsed:      nowTime().Sub(a.anim.shimmerStart),
		Depth:        a.animDepth(),
	}, a.lang)
}

// renderDialog paints the modal layer when one is open.
func (a *App) renderDialog(w, h int) {
	d := views.Dialog{T: a.tokens}
	switch a.ui.Dialog {
	case views.DialogLogin:
		a.ui.Login.Now = nowTime().UnixMilli()
		a.ui.Login.Shimmer = a.animated() && a.loginButtonSweeping()
		a.ui.Login.Elapsed = nowTime().Sub(a.anim.buttonStart)
		a.ui.Login.Depth = a.animDepth()
		d.RenderLogin(a.scr, w, h, a.ui.Login, a.lang)
	case views.DialogRegister:
		d.RenderRegister(a.scr, w, h, a.ui.Register, a.lang)
	case views.DialogRecover:
		d.RenderRecover(a.scr, w, h, a.ui.Recover, a.lang)
	case views.DialogLogout:
		a.ui.Logout.Shimmer = a.animated()
		a.ui.Logout.Elapsed = nowTime().Sub(a.anim.buttonStart)
		a.ui.Logout.Depth = a.animDepth()
		d.RenderLogout(a.scr, w, h, a.ui.Logout, a.lang)
	case views.DialogSessionReplaced:
		d.RenderSessionReplaced(a.scr, w, h, a.ui.SessionReplaced, a.lang)
	case views.DialogAccounts:
		m := a.ui.Accounts
		m.Accounts = a.accountEntries()
		m.Shimmer = a.animated()
		m.Elapsed = nowTime().Sub(a.anim.buttonStart)
		m.Depth = a.animDepth()
		d.RenderAccounts(a.scr, w, h, m, a.lang)
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

// onStage reports whether the sign-in stage is showing: one of the sign-in panels
// is up. Other dialogs — the displaced-session notice, confirmations — sit over
// the main interface instead, because what they are about is on it.
func (a *App) onStage() bool {
	switch a.ui.Dialog {
	case views.DialogLogin, views.DialogRegister, views.DialogRecover, views.DialogAccounts:
		return true
	}
	return false
}

// stagePanel is the rectangle of whichever sign-in panel is up.
func (a *App) stagePanel(w, h int) widget.Rect {
	switch a.ui.Dialog {
	case views.DialogRegister:
		return views.RegisterPanelRect(w, h)
	case views.DialogRecover:
		return views.RecoverPanelRect(w, h)
	case views.DialogAccounts:
		return views.AccountsPanelRect(w, h, len(a.cfg.Accounts))
	}
	return views.LoginPanelRect(w, h)
}

// stageElapsed is how far the dots have drifted. With animation off it is held
// at zero, so the field is the same still sky on every frame.
func (a *App) stageElapsed() time.Duration {
	if !a.animated() {
		return 0
	}
	return nowTime().Sub(a.anim.stageStart)
}

// renderStage draws the stage background and the panel on it.
func (a *App) renderStage(w, h int) {
	views.Stage{T: a.tokens}.Render(a.scr, w, h, views.StageModel{
		Panel:   a.stagePanel(w, h),
		Elapsed: a.stageElapsed(),
		Fade:    1,
	})
	a.renderDialog(w, h)
	// The sign-out exit, if one is playing, covers what it has not yet uncovered.
	a.renderExit(w, h)
}

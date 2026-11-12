package views

import (
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/client"
	"link-chat/pkg/i18n"
	"link-chat/pkg/tui/anim"
	"link-chat/pkg/tui/layout"
	"link-chat/pkg/tui/theme"
	"link-chat/pkg/tui/widget"
)

// DialogKind selects which dialog is on top.
type DialogKind int

const (
	DialogNone DialogKind = iota
	DialogLogin
	DialogRegister
	DialogRecover
	DialogChannel
	DialogDM
	DialogHelp
	DialogSettings
	DialogLogout
	DialogSessionReplaced
	DialogAbout
)

// Dialog renders the modal layer.
//
// Every dialog shares one visual form: a bordered surface centred over a
// dimmed backdrop, title in the top edge, fields aligned on a single value
// column, and one hint line at the bottom. Consistency here is what makes the
// modal layer feel like part of the product rather than a separate screen
// (spec tui-design-system / 对话框使用边框).
type Dialog struct {
	T theme.Tokens
}

// LoginFocus is which element of the login panel owns the keyboard.
//
// The panel is two fields, a button and two links, so a simple enumeration beats
// a generic focus system: Tab cycles it and every element has exactly one
// keyboard behaviour.
type LoginFocus int

const (
	LoginFocusUsername LoginFocus = iota
	LoginFocusPassword
	LoginFocusSubmit
	LoginFocusForgot
	LoginFocusRegister
	loginFocusCount // sentinel, not a focus target
)

// Next returns the focus that Tab moves to.
func (f LoginFocus) Next() LoginFocus {
	return (f + 1) % loginFocusCount
}

// Prev returns the focus that Shift+Tab moves to.
func (f LoginFocus) Prev() LoginFocus {
	return (f + loginFocusCount - 1) % loginFocusCount
}

// IsField reports whether the focus sits on a text input.
func (f LoginFocus) IsField() bool {
	return f == LoginFocusUsername || f == LoginFocusPassword
}

// IsLink reports whether the focus sits on one of the two links under the button.
func (f LoginFocus) IsLink() bool {
	return f == LoginFocusForgot || f == LoginFocusRegister
}

// Right is where → moves the focus. Only the two links sit side by side, so only
// they have a right neighbour: from the left link it is the right link, and from
// the right link (the edge) it stays put. Everything else is not on a row of
// peers, so → leaves the focus alone rather than guessing a direction for it.
func (f LoginFocus) Right() LoginFocus {
	if f == LoginFocusForgot {
		return LoginFocusRegister
	}
	return f
}

// Left is where ← moves the focus; the mirror image of Right.
func (f LoginFocus) Left() LoginFocus {
	if f == LoginFocusRegister {
		return LoginFocusForgot
	}
	return f
}

// LoginModel holds the sign-in panel state.
//
// There is deliberately no Server field: the address comes from configuration
// (design.md decision 6). It is still displayed on failure, so the user can tell
// a wrong address from a server that is down.
type LoginModel struct {
	Username string
	Password string
	Focus    LoginFocus
	Err      string
	Busy     bool
	// ServerShown is the address in use, rendered under the form when something
	// goes wrong.
	ServerShown string

	// Remembered means a saved session exists for Username, so the password row
	// is replaced by a note and signing in needs no typing at all. It is cleared
	// the moment the user signals they want something else: editing the name,
	// typing a password, or the saved session proving invalid
	// (design.md decision 1).
	Remembered bool

	// Shimmer sweeps a band of light across the focused button label. Elapsed is
	// how long the sweep has been running, passed in so a render stays a pure
	// function of its inputs.
	Shimmer bool
	Elapsed time.Duration
	Depth   anim.Depth
}

// RegisterModel holds the registration form.
type RegisterModel struct {
	Username string
	Password string
	Question string
	Answer   string
	// Field is 0=username, 1=password, 2=question, 3=answer.
	Field int
	// Picking is true while the preset question list is open.
	Picking bool
	PickIdx int
	Err     string
	Busy    bool
}

// RecoverStep is which step of the password-recovery wizard is showing.
type RecoverStep int

const (
	RecoverStepUsername RecoverStep = iota
	RecoverStepAnswer
	RecoverStepPassword
	RecoverStepDone
)

// RecoverModel holds the three-step recovery wizard.
type RecoverModel struct {
	Step     RecoverStep
	Username string
	Question string
	Answer   string
	NewPass  string
	Confirm  string
	// Field is the index of the input within the current step.
	Field int
	// ResetToken is held between step two and three.
	ResetToken string
	Err        string
	Busy       bool
}

// FieldCount is how many inputs the current step shows.
func (m RecoverModel) FieldCount() int {
	switch m.Step {
	case RecoverStepUsername:
		return 1
	case RecoverStepAnswer:
		return 1
	case RecoverStepPassword:
		return 2
	}
	return 0
}

// SessionReplacedModel describes why this client lost the account.
//
// It is shown as a blocking notice rather than a status-bar message because the
// user has just been logged out: they need to know it was deliberate, and where
// from, before they decide whether to sign back in.
type SessionReplacedModel struct {
	Username string
	Location string
	At       int64
	Device   string
}

// PromptModel holds a single-line text prompt (channel name, DM peer).
type PromptModel struct {
	Title string
	Label string
	Value string
	Err   string
	Busy  bool
}

// SettingsModel holds the settings screen state.
type SettingsModel struct {
	Selected int
	Rows     []SettingsRow
}

// SettingsRow is one line of the settings screen.
type SettingsRow struct {
	Kind  int // 0 header, 1 value, 2 action
	Label string
	Value string
}

// ConfirmModel holds a yes/no prompt.
// LogoutChoice is which button of the sign-out confirmation has the focus.
type LogoutChoice int

const (
	// LogoutCancel is the default: the safe choice is preselected.
	LogoutCancel LogoutChoice = iota
	LogoutConfirm
)

// LogoutModel is the sign-out confirmation.
type LogoutModel struct {
	Focus LogoutChoice
	// The focused button's light band: on, its clock, and the colour tier.
	Shimmer bool
	Elapsed time.Duration
	Depth   anim.Depth
}

// loginLabelCol returns the width of the label column: the widest label in the
// current language plus a gap. Chinese and English labels differ in width (the
// Chinese username label is 6 cells, the English one is 8), so the column is
// computed rather than fixed, and the two inputs always start at the same place.
func loginLabelCol(lang i18n.Lang) int {
	a := layout.StringWidth(i18n.LoginUsername.Get(lang))
	b := layout.StringWidth(i18n.LoginPassword.Get(lang))
	return maxInt(a, b) + 2
}

// loginPanelHeight is the height of the sign-in panel in rows: a blank row under
// the title, the two fields, the error line, the button, the links and a blank row
// above the bottom border. The address and key-hint rows that used to close the
// panel are gone.
const loginPanelHeight = 14

// rememberedDots is how many placeholder dots the password row shows when a saved
// session stands in for the password. It is a constant on purpose: it must not
// depend on the real password, which would leak its length, and the client does
// not know the password anyway (spec tui-chat-client / 已保存登录时的密码行).
const rememberedDots = 8

// RenderLogin draws the sign-in panel: two single-line fields, an error line, a
// bordered button, and two links.
//
// Each field puts its label and its input on one line. That is what lets the
// panel be shorter: a two-line field spends a whole row on a label that fits
// happily beside the input.
func (v Dialog) RenderLogin(s tcell.Screen, w, h int, m LoginModel, lang i18n.Lang) {
	box := LoginPanelRect(w, h)
	c := widget.NewCanvas(s, v.T, box)
	v.paintSurface(c)
	c.BoxTitle(i18n.LoginTitle.Get(lang), v.T.BorderStyle(), v.T.Heading())

	innerX := 4
	rowW := box.W - 2*innerX
	labelW := loginLabelCol(lang)
	inputX := innerX + labelW
	inputW := rowW - labelW

	// Username: label and input share a row.
	c.Text(innerX, 2, i18n.LoginUsername.Get(lang), v.T.Muted())
	v.fieldBox(c, inputX, 2, inputW, m.Username, m.Focus == LoginFocusUsername, false)

	// Password. With a saved session there is nothing to type, but the row keeps
	// the same box as an ordinary password so the panel does not change shape: it
	// holds a fixed run of dots, no caret, and no text. The dots say "something is
	// remembered" without saying how long it is.
	c.Text(innerX, 4, i18n.LoginPassword.Get(lang), v.T.Muted())
	if m.Remembered {
		v.fieldRow(c, inputX, 4, inputW, strings.Repeat("•", rememberedDots), false)
	} else {
		v.fieldBox(c, inputX, 4, inputW, m.Password, m.Focus == LoginFocusPassword, true)
	}

	// Error line: between the password and the button, centred, always reserved.
	// An over-long message is cut rather than wrapped, so it stays on this one row.
	// It also carries "signing in…" while a sign-in is under way: the panel has no
	// status row of its own any more, and the two never occur together, since a
	// new attempt clears the old error.
	switch {
	case m.Busy:
		msg := layout.Truncate(i18n.LoginSigning.Get(lang), rowW)
		c.Text(innerX+maxInt(0, (rowW-layout.StringWidth(msg))/2), 5, msg, v.T.WarningStyle().Background(v.T.Surface))
	case m.Err != "":
		msg := layout.Truncate(m.Err, rowW)
		c.Text(innerX+maxInt(0, (rowW-layout.StringWidth(msg))/2), 5, msg, v.T.DangerStyle().Background(v.T.Surface))
	}

	// Submit: a compact bordered button, three rows tall, centred.
	v.loginButton(c, innerX, 7, rowW, i18n.LoginSubmit.Get(lang), m)

	// Two underlined links, side by side.
	linkY := 11
	forgot := i18n.ForgotPassword.Get(lang)
	register := i18n.RegisterNow.Get(lang)
	gap := 4
	totalW := layout.StringWidth(forgot) + gap + layout.StringWidth(register)
	x := innerX + maxInt(0, (rowW-totalW)/2)
	x += v.link(c, x, linkY, forgot, m.Focus == LoginFocusForgot)
	x += gap
	v.link(c, x, linkY, register, m.Focus == LoginFocusRegister)

	// No status row and no key hint below the links: the address in use only
	// matters when it cannot be reached, and then the error line says it; the keys
	// are the ones every form uses (spec tui-login-stage /
	// 登录面板不常驻服务器地址与操作提示).
}

// fieldBox draws one text input across its whole width.
//
// The background is laid down first and the text written over it, so the field's
// extent never depends on what it holds: a two-letter username and an empty
// password box are the same width, and the rows line up. Drawing the fill only
// behind the characters is what made short names look like a stray dark patch
// (spec tui-design-system / 输入框外观令牌).
//
// Focus changes the background and shows a caret; it does not change the extent.
func (v Dialog) fieldBox(c *widget.Canvas, x, y, w int, value string, focused, isPassword bool) {
	shown := value
	if isPassword {
		shown = mask(value)
	}
	v.fieldRow(c, x, y, w, shown, focused)
}

// fieldRow is the shared painter behind every form field. shown is exactly what
// appears inside the box: callers decide whether that is the real text, a mask,
// or a fixed run of placeholder dots.
func (v Dialog) fieldRow(c *widget.Canvas, x, y, w int, shown string, focused bool) {
	if w <= 0 {
		return
	}
	bg := v.T.InputStyle()
	if focused {
		bg = v.T.InputFocusStyle()
	}

	// Without a background colour to show its extent, a field is marked by glyphs
	// at both ends instead.
	edges := v.T.FieldEdges()
	inner, innerX := w, x
	if edges && w > 2 {
		inner, innerX = w-2, x+1
	}

	body := " " + shown
	if focused {
		body += "█"
	}
	// When the content is wider than the box, keep its tail: the end is where the
	// caret is, and the part being typed must stay visible.
	if layout.StringWidth(body) > inner {
		body = tailFit(body, inner)
	}
	c.TextFill(innerX, y, inner, body, bg)

	if edges && w > 2 {
		open, close := "[", "]"
		c.Text(x, y, open, v.T.SurfaceStyle())
		c.Text(x+w-1, y, close, v.T.SurfaceStyle())
	}
}

// tailFit returns the longest suffix of s that fits in w cells.
func tailFit(s string, w int) string {
	r := []rune(s)
	total := 0
	i := len(r)
	for i > 0 {
		rw := layout.RuneWidth(r[i-1])
		if total+rw > w {
			break
		}
		total += rw
		i--
	}
	return string(r[i:])
}

// buttonPad is the empty space either side of a button label inside its frame.
const buttonPad = 3

// ButtonState is how a framed button is drawn: whether it has the focus, whether
// it is the dangerous choice, and the light band's clock.
type ButtonState struct {
	Focused bool
	// Danger draws the focused button in the danger colour instead of the accent:
	// the choice that cannot be undone should look like it.
	Danger  bool
	Shimmer bool
	Elapsed time.Duration
	Depth   anim.Depth
}

// FramedButtonWidth is the width of a framed button for a label.
func FramedButtonWidth(label string) int { return layout.StringWidth(label) + 2*buttonPad + 2 }

// loginButton draws the sign-in button as a small bordered box.
//
// Width comes from the label, not the row: a button that stretches across the
// whole panel reads as a banner. When focused, its frame and label take the accent
// colour and a band of light sweeps across the label, the same visual language as
// the composer hint (spec tui-chat-client / 登录按钮外观).
func (v Dialog) loginButton(c *widget.Canvas, x, y, rowW int, label string, m LoginModel) {
	boxW := minInt(FramedButtonWidth(label), rowW)
	v.frameButton(c, x+(rowW-boxW)/2, y, boxW, label, ButtonState{
		Focused: m.Focus == LoginFocusSubmit, Shimmer: m.Shimmer, Elapsed: m.Elapsed, Depth: m.Depth,
	})
}

// frameButton draws a three-row framed button of width boxW at (x, y) in c. The
// sign-in button and the sign-out confirmation's two buttons are all drawn here, so
// they cannot drift apart in shape or in how focus looks.
func (v Dialog) frameButton(c *widget.Canvas, x, y, boxW int, label string, b ButtonState) {
	labelW := layout.StringWidth(label)
	innerW := boxW - 2

	frame := v.T.Muted()
	rest := v.T.Accent
	if b.Focused {
		frame = v.T.Accented()
		if b.Danger {
			frame = v.T.DangerStyle()
			rest = v.T.Danger
		}
	}

	// Frame: three rows, border only. The panel surface shows through the middle,
	// so the button is an outline, not a filled block.
	boxRect := widget.Rect{X: c.Rect.X + x, Y: c.Rect.Y + y, W: boxW, H: 3}
	bc := widget.NewCanvas(c.S, v.T, boxRect)
	bc.Fill(v.T.SurfaceStyle())
	bc.Box(v.surfaceOn(frame))

	// Label, centred in the frame.
	labelX := 1 + (innerW-labelW)/2
	if b.Focused && b.Shimmer {
		v.shimmerLabel(bc, labelX, 1, label, innerW, rest, b)
		return
	}
	bc.Text(labelX, 1, label, v.surfaceOn(frame))
}

// surfaceOn re-bases a style onto the dialog surface background, so text drawn on
// the panel does not punch a page-coloured hole in it.
func (v Dialog) surfaceOn(st tcell.Style) tcell.Style {
	fg, _, attrs := st.Decompose()
	return tcell.StyleDefault.Foreground(fg).Background(v.T.Surface).Attributes(attrs)
}

// shimmerLabel paints a button label with a band of light sweeping through it.
//
// The band is positioned against the button whole inner width, padding included,
// so it enters from the left over empty space, crosses the label, and leaves on
// the right. Measured against the label alone it would be on top of it almost at
// once and look like a flash.
func (v Dialog) shimmerLabel(bc *widget.Canvas, labelX, y int, label string, innerW int, rest tcell.Color, b ButtonState) {
	band := anim.ButtonShimmer
	hi := v.T.Text

	marker := -1
	if b.Depth == anim.DepthMono {
		marker = band.ShimmerMarker(innerW, b.Elapsed)
	}

	cell := 0
	for _, r := range label {
		w := layout.RuneWidth(r)
		if w == 0 {
			continue
		}
		// This glyph's position within the inner width, where the band lives.
		pos := (labelX - 1) + cell

		style := v.T.SurfaceStyle().Foreground(rest)
		switch {
		case b.Depth == anim.DepthMono:
			if pos == marker || (w == 2 && pos+1 == marker) {
				style = style.Bold(true).Underline(true)
			}
		default:
			br := band.Brightness(pos, innerW, b.Elapsed)
			style = v.T.SurfaceStyle().Foreground(anim.ShimmerColor(rest, hi, br, b.Depth))
		}
		bc.Text(labelX+cell, y, string(r), style)
		cell += w
	}
}

// link draws an underlined text link and returns its width.
func (v Dialog) link(c *widget.Canvas, x, y int, label string, focused bool) int {
	style := v.T.Muted().Underline(true)
	if focused {
		style = v.T.Accented().Underline(true)
	}
	c.Text(x, y, label, style)
	return layout.StringWidth(label)
}

// RenderRegister draws the registration form.
//
// The question field is a text input that can also open a preset list, which is
// why it carries a hint: most people should pick one, but nobody should be
// forced to.
func (v Dialog) RenderRegister(s tcell.Screen, w, h int, m RegisterModel, lang i18n.Lang) {
	box := RegisterPanelRect(w, h)
	c := widget.NewCanvas(s, v.T, box)
	v.paintSurface(c)
	c.BoxTitle(i18n.RegisterTitle.Get(lang), v.T.BorderStyle(), v.T.Heading())

	innerX := 4
	fieldW := box.W - 2*innerX

	c.Text(innerX, 2, i18n.LoginUsername.Get(lang), v.T.Muted())
	v.fieldBox(c, innerX, 3, fieldW, m.Username, m.Field == 0, false)

	c.Text(innerX, 5, i18n.LoginPassword.Get(lang), v.T.Muted())
	v.fieldBox(c, innerX, 6, fieldW, m.Password, m.Field == 1, true)

	c.Text(innerX, 8, i18n.RegQuestion.Get(lang), v.T.Muted())
	v.fieldBox(c, innerX, 9, fieldW, m.Question, m.Field == 2, false)

	c.Text(innerX, 11, i18n.RegAnswer.Get(lang), v.T.Muted())
	v.fieldBox(c, innerX, 12, fieldW, m.Answer, m.Field == 3, false)

	// The answer is the only way back into this account, so say so plainly
	// rather than burying it in documentation.
	c.Text(innerX, 13, layout.Truncate(i18n.RegAnswerHelp.Get(lang), fieldW), v.T.Faint())

	switch {
	case m.Err != "":
		c.Text(innerX, 15, layout.Truncate(m.Err, fieldW), v.T.DangerStyle())
	case m.Busy:
		c.Text(innerX, 15, i18n.LoginConnect.Get(lang), v.T.WarningStyle())
	}

	c.Text(innerX, box.H-2, i18n.RegHint.Get(lang), v.T.Faint())

	if m.Picking {
		v.renderQuestionPicker(c, innerX, 9, fieldW, m, lang)
	}
}

// renderQuestionPicker overlays the preset question list under the field.
func (v Dialog) renderQuestionPicker(c *widget.Canvas, x, y, w int, m RegisterModel, lang i18n.Lang) {
	h := len(i18n.SecurityQuestions) + 2
	box := widget.Rect{X: c.Rect.X + x, Y: c.Rect.Y + y, W: w, H: h}
	pc := widget.NewCanvas(c.S, v.T, box)
	pc.Fill(v.T.SurfaceStyle())
	pc.Box(v.T.BorderStyle())

	for i, q := range i18n.SecurityQuestions {
		row := i + 1
		if row >= h-1 {
			break
		}
		style := v.T.SurfaceMuted()
		if i == m.PickIdx {
			pc.TextFill(1, row, w-2, " "+q.Get(lang), v.T.Selected())
			continue
		}
		pc.Text(2, row, layout.Truncate(q.Get(lang), w-3), style)
	}
}

// RenderRecover draws one step of the recovery wizard.
func (v Dialog) RenderRecover(s tcell.Screen, w, h int, m RecoverModel, lang i18n.Lang) {
	c := widget.NewCanvas(s, v.T, RecoverPanelRect(w, h))
	v.paintSurface(c)
	c.BoxTitle(i18n.RecoverTitle.Get(lang), v.T.BorderStyle(), v.T.Heading())

	innerX := 4
	fieldW := c.Rect.W - 2*innerX

	// Step indicator: which step, out of how many. Always visible so the user
	// knows how much is left (spec tui-chat-client / 找回密码流程).
	step := i18n.RecoverStep1
	switch m.Step {
	case viewsRecoverAnswer:
		step = i18n.RecoverStep2
	case viewsRecoverPassword:
		step = i18n.RecoverStep3
	}
	c.Text(innerX, 1, layout.Truncate(step.Get(lang), fieldW), v.T.Faint())

	switch m.Step {
	case viewsRecoverUsername:
		c.Text(innerX, 3, i18n.LoginUsername.Get(lang), v.T.Muted())
		v.fieldBox(c, innerX, 4, fieldW, m.Username, true, false)

	case viewsRecoverAnswer:
		// The question itself is the point of this step, so it gets the accent
		// colour rather than sitting in the muted label colour.
		c.Text(innerX, 3, layout.Truncate(m.Question, fieldW), v.T.Accented())
		c.Text(innerX, 5, i18n.RegAnswer.Get(lang), v.T.Muted())
		v.fieldBox(c, innerX, 6, fieldW, m.Answer, true, false)

	case viewsRecoverPassword:
		c.Text(innerX, 3, i18n.RecoverNew.Get(lang), v.T.Muted())
		v.fieldBox(c, innerX, 4, fieldW, m.NewPass, m.Field == 0, true)
		c.Text(innerX, 6, i18n.RecoverRepeat.Get(lang), v.T.Muted())
		v.fieldBox(c, innerX, 7, fieldW, m.Confirm, m.Field == 1, true)
	}

	switch {
	case m.Err != "":
		c.Text(innerX, 9, layout.Truncate(m.Err, fieldW), v.T.DangerStyle())
	case m.Busy:
		c.Text(innerX, 9, i18n.LoginConnect.Get(lang), v.T.WarningStyle())
	}

	c.Text(innerX, c.Rect.H-2, i18n.RecoverHint.Get(lang), v.T.Faint())
}

// Local aliases keep the wizard's step names short inside this file.
const (
	viewsRecoverUsername = RecoverStepUsername
	viewsRecoverAnswer   = RecoverStepAnswer
	viewsRecoverPassword = RecoverStepPassword
)

// RenderSessionReplaced draws the "signed in elsewhere" notice.
func (v Dialog) RenderSessionReplaced(s tcell.Screen, w, h int, m SessionReplacedModel, lang i18n.Lang) {
	box := centred(w, h, minInt(56, w-4), 11)
	c := widget.NewCanvas(s, v.T, box)
	v.paintSurface(c)
	c.BoxTitle(i18n.SessionReplacedTitle.Get(lang), v.T.BorderStyle(), v.T.DangerStyle())

	innerX := 4
	fieldW := box.W - 2*innerX

	c.Text(innerX, 2, layout.Truncate(i18n.SessionReplaced.Get(lang), fieldW), v.T.Body())
	if m.Username != "" {
		c.Text(innerX, 3, layout.Truncate(i18n.SessionReplacedWho.F(lang, m.Username), fieldW), v.T.Muted())
	}

	// Location and time are the actionable part: they answer "was that me?".
	c.Text(innerX, 5, i18n.RecoverLabelLocation.Get(lang), v.T.Faint())
	c.Text(innerX+12, 5, layout.Truncate(m.Location, fieldW-12), v.T.Accented())

	if m.At > 0 {
		c.Text(innerX, 6, i18n.RecoverLabelTime.Get(lang), v.T.Faint())
		c.Text(innerX+12, 6, i18n.FullTime(m.At), v.T.Muted())
	}
	if m.Device != "" {
		c.Text(innerX, 7, i18n.RecoverLabelDevice.Get(lang), v.T.Faint())
		c.Text(innerX+12, 7, layout.Truncate(m.Device, fieldW-12), v.T.Muted())
	}

	c.Text(innerX, box.H-2, i18n.SessionReplacedAction.Get(lang), v.T.Faint())
}

// RenderPrompt draws a one-field prompt (new channel or DM).
func (v Dialog) RenderPrompt(s tcell.Screen, w, h int, m PromptModel, lang i18n.Lang) {
	box := centred(w, h, minInt(50, w-4), 8)
	c := widget.NewCanvas(s, v.T, box)
	v.paintSurface(c)
	c.BoxTitle(m.Title, v.T.BorderStyle(), v.T.Heading())

	c.Text(3, 2, m.Label, v.T.Muted())
	// The prompt has one field and it always owns the keyboard, so it is drawn
	// focused. Same painter as the forms: full width, same look.
	fieldX := 14
	v.fieldBox(c, fieldX, 2, box.W-fieldX-3, m.Value, true, false)

	if m.Err != "" {
		c.Text(3, 4, layout.Truncate(m.Err, box.W-6), v.T.DangerStyle())
	}
	c.Text(3, box.H-2, i18n.Cancel.Get(lang)+"  Esc · Enter", v.T.Faint())
}

// RenderAbout draws the About dialog: the app's name, its version and a line on
// what it is. The version is passed in because only the binary knows it.
func (v Dialog) RenderAbout(s tcell.Screen, w, h int, version string, lang i18n.Lang) {
	box := centred(w, h, minInt(46, w-4), 9)
	c := widget.NewCanvas(s, v.T, box)
	v.paintSurface(c)
	c.BoxTitle(i18n.AboutTitle.Get(lang), v.T.BorderStyle(), v.T.Heading())

	innerX := 4
	avail := box.W - 2*innerX

	c.Text(innerX, 2, layout.Truncate(i18n.AppName.Get(lang), avail), v.T.Accented())
	c.Text(innerX, 4, layout.Truncate(i18n.AboutBlurb.Get(lang), avail), v.T.SurfaceMuted())

	label := i18n.AboutVer.Get(lang)
	c.Text(innerX, 6, label, v.T.Faint())
	c.Text(innerX+layout.StringWidth(label)+2, 6, layout.Truncate(version, avail-layout.StringWidth(label)-2), v.T.SurfaceStyle())

	c.Text(innerX, box.H-2, i18n.KeyBack.Get(lang), v.T.Faint())
}

// RenderConfirm draws a yes/no confirmation.
// LogoutPanelRect is where the sign-out confirmation sits.
func LogoutPanelRect(w, h int) widget.Rect { return centred(w, h, minInt(44, w-4), 10) }

// RenderLogout draws the sign-out confirmation: a line saying what signing out
// means, and two framed buttons in the sign-in button's style, "cancel" and "sign
// out", side by side (spec tui-login-stage / 退出登录的确认).
func (v Dialog) RenderLogout(s tcell.Screen, w, h int, m LogoutModel, lang i18n.Lang) {
	box := LogoutPanelRect(w, h)
	// A one-cell moat of plain background around the panel: floating over the
	// main interface, its border would otherwise run straight into the list's
	// divider and the message bubbles and read as one tangle of lines.
	moat := widget.Rect{X: box.X - 1, Y: box.Y - 1, W: box.W + 2, H: box.H + 2}
	widget.NewCanvas(s, v.T, moat).Fill(v.T.Base())
	c := widget.NewCanvas(s, v.T, box)
	v.paintSurface(c)
	c.BoxTitle(i18n.LogoutTitle.Get(lang), v.T.BorderStyle(), v.T.Heading())

	msg := layout.Truncate(i18n.LogoutMessage.Get(lang), box.W-6)
	c.Text((box.W-layout.StringWidth(msg))/2, 2, msg, v.T.Body())

	cancel, confirm := i18n.Cancel.Get(lang), i18n.LogoutConfirm.Get(lang)
	cw, kw := FramedButtonWidth(cancel), FramedButtonWidth(confirm)
	const gap = 3
	x := (box.W - (cw + gap + kw)) / 2
	if x < 1 {
		x = 1
	}
	state := func(focused, danger bool) ButtonState {
		return ButtonState{Focused: focused, Danger: danger, Shimmer: m.Shimmer, Elapsed: m.Elapsed, Depth: m.Depth}
	}
	v.frameButton(c, x, 5, cw, cancel, state(m.Focus == LogoutCancel, false))
	v.frameButton(c, x+cw+gap, 5, kw, confirm, state(m.Focus == LogoutConfirm, true))
}

// RenderHelp draws the keyboard reference.
func (v Dialog) RenderHelp(s tcell.Screen, w, h int, lang i18n.Lang) {
	box := centred(w, h, minInt(64, w-4), minInt(22, h-2))
	c := widget.NewCanvas(s, v.T, box)
	v.paintSurface(c)
	c.BoxTitle(i18n.HelpTitle.Get(lang), v.T.BorderStyle(), v.T.Heading())

	y := 2
	section := func(title string, rows []i18n.HelpRow) {
		if y >= box.H-2 {
			return
		}
		c.Text(3, y, title, v.T.Heading())
		y++
		for _, r := range rows {
			if y >= box.H-2 {
				return
			}
			c.Text(4, y, layout.PadRight(r.Key, 14), v.T.Accented())
			c.Text(20, y, layout.Truncate(r.Desc.Get(lang), box.W-24), v.T.Muted())
			y++
		}
		y++
	}
	section(i18n.HelpGlobal.Get(lang), i18n.HelpGlobalRows)
	section(i18n.HelpList.Get(lang), i18n.HelpListRows)
	section(i18n.HelpCompose.Get(lang), i18n.HelpComposeRows)

	c.Text(3, box.H-2, i18n.HelpClose.Get(lang), v.T.Faint())
}

// RenderSettings draws the settings screen as a full-height panel rather than a
// small dialog: settings benefit from room, and the reference config screen is
// itself a full screen.
func (v Dialog) RenderSettings(s tcell.Screen, w, h int, m SettingsModel, st *client.AppState, lang i18n.Lang) {
	box := centred(w, h, minInt(64, w-4), minInt(20, h-2))
	c := widget.NewCanvas(s, v.T, box)
	v.paintSurface(c)
	c.BoxTitle(i18n.SettingsTitle.Get(lang), v.T.BorderStyle(), v.T.Heading())

	const labelX, valueX = 4, 34
	valueW := box.W - valueX - 4
	_ = valueW

	for i, row := range m.Rows {
		y := 2 + i
		if y >= box.H-2 {
			break
		}
		switch row.Kind {
		case 0: // section header
			c.Text(labelX-1, y, row.Label, v.T.Heading())
		case 2: // action
			style := v.T.Muted()
			if i == m.Selected {
				c.TextPadded(labelX-1, y, "", v.T.Selected())
				style = v.T.Selected()
			}
			c.Text(labelX, y, row.Label, style)
		default:
			style := v.T.Body()
			valStyle := v.T.Muted()
			if i == m.Selected {
				c.TextPadded(labelX-1, y, "", v.T.Selected())
				style = v.T.Selected()
				valStyle = v.T.SelectedMuted()
			}
			c.Text(labelX, y, row.Label, style)
			c.Text(valueX, y, layout.Truncate(row.Value, box.W-valueX-3), valStyle)
		}
	}

	c.Text(labelX-1, box.H-2, i18n.KeyBack.Get(lang)+" · ↑↓ · Enter", v.T.Faint())
}

// paintSurface fills a dialog's interior and dims nothing else: tcell has no
// cheap backdrop blend, and a bright box on a clean background already reads as
// "on top".
func (v Dialog) paintSurface(c *widget.Canvas) {
	c.Fill(v.T.SurfaceStyle())
}

// LoginPanelRect, RegisterPanelRect and RecoverPanelRect are where the three
// sign-in panels sit on a w×h screen. The panels draw themselves here and the
// stage keeps its background clear of the same rectangles, so the two cannot
// disagree about where a panel is.
func LoginPanelRect(w, h int) widget.Rect {
	return centred(w, h, minInt(50, w-4), loginPanelHeight)
}

func RegisterPanelRect(w, h int) widget.Rect { return centred(w, h, minInt(54, w-4), 18) }

func RecoverPanelRect(w, h int) widget.Rect { return centred(w, h, minInt(48, w-4), 12) }

// centred returns a rect of the given size centred in the screen.
func centred(w, h, bw, bh int) widget.Rect {
	if bw > w {
		bw = w
	}
	if bh > h {
		bh = h
	}
	return widget.Rect{X: (w - bw) / 2, Y: (h - bh) / 2, W: bw, H: bh}
}

func mask(s string) string {
	n := len([]rune(s))
	if n == 0 {
		return ""
	}
	out := make([]rune, n)
	for i := range out {
		out[i] = '•'
	}
	return string(out)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

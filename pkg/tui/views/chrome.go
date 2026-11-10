// Package views renders the client's screens.
//
// Each view is a function of AppState onto a rect of cells. There is no widget
// tree and no retained state: a view receives the state, paints, and returns.
// That makes the whole interface a pure function that can be snapshotted in tests
// (design.md decision 7).
package views

import (
	"fmt"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/client"
	"link-chat/pkg/i18n"
	"link-chat/pkg/protocol"
	"link-chat/pkg/tui/layout"
	"link-chat/pkg/tui/theme"
	"link-chat/pkg/tui/widget"
)

// Chrome is the persistent scaffolding: the app frame, the status bar and the
// keyboard legend. It is drawn around whatever view is active so the interface
// never jumps between screens.
type Chrome struct {
	T theme.Tokens
}

// Frame draws the outer structure: a title row, a rule, the content region, and
// the status bar.
//
// Note what is absent: no border around the window. Region boundaries come from
// rules and whitespace instead, which is what the reference config screen does
// and what keeps a dense terminal screen from looking like a form
// (spec tui-design-system / 主界面不使用整块边框).
func (c Chrome) Frame(s tcell.Screen, st *client.AppState, lang i18n.Lang, w, h int) (content widget.Rect) {
	// Background must be painted explicitly; tcell does not clear to our theme.
	bg := widget.NewCanvas(s, c.T, widget.Rect{X: 0, Y: 0, W: w, H: h})
	bg.Fill(c.T.Base())

	// Title row.
	title := widget.NewCanvas(s, c.T, widget.Rect{X: 0, Y: 0, W: w, H: 1})
	title.Fill(c.T.Base())
	title.Text(layout.GutterX, 0, i18n.AppName.Get(lang), c.T.Heading())

	// Connection chip on the right, so state is always visible without hunting.
	chip, chipStyle := connectionChip(st, lang, c.T)
	title.TextRight(0, chip, chipStyle)

	// Rule under the title.
	rule := widget.NewCanvas(s, c.T, widget.Rect{X: layout.GutterX, Y: 1, W: w - 2*layout.GutterX, H: 1})
	rule.Rule(0, c.T.BorderSoftStyle())

	// Bottom block: a rule, then the status line. The content region stops above
	// the rule so nothing ever shares a row with it.
	if ruleY := h - statusRows; ruleY > 2 {
		bar := widget.NewCanvas(s, c.T, widget.Rect{X: layout.GutterX, Y: ruleY, W: w - 2*layout.GutterX, H: 1})
		bar.Rule(0, c.T.BorderSoftStyle())
	}

	return ContentRect(w, h)
}

// statusRows is the height of the bottom block: a rule plus the status line.
const statusRows = 2

// ContentRect is the region between the title rule and the status block.
//
// It depends only on the window size, which is what lets code outside rendering —
// the focus animation needs to know where each pane's title sits — ask for the
// layout without drawing anything.
func ContentRect(w, h int) widget.Rect {
	ruleY := h - statusRows
	return widget.Rect{
		X: 0,
		Y: 2,
		W: w,
		H: maxInt(0, ruleY-2),
	}
}

// connectionChip renders the state as "● label", using a dot whose colour
// carries most of the meaning so the label can stay short.
func connectionChip(st *client.AppState, lang i18n.Lang, t theme.Tokens) (string, tcell.Style) {
	dot := "●"
	if !t.UnicodeBox {
		dot = "*"
	}
	switch st.State {
	case client.StateReady:
		return dot + " " + i18n.Connected.Get(lang), t.SuccessStyle()
	case client.StateConnecting:
		return dot + " " + i18n.Connecting.Get(lang), t.WarningStyle()
	case client.StateAuthenticating:
		return dot + " " + i18n.Authenticating.Get(lang), t.WarningStyle()
	case client.StateDisconnected:
		return dot + " " + i18n.Disconnected.Get(lang), t.DangerStyle()
	case client.StateAuthFailed:
		return dot + " " + i18n.NeedLogin.Get(lang), t.DangerStyle()
	case client.StateUnconfigured:
		return dot + " " + i18n.Unconfigured.Get(lang), t.Faint()
	}
	return dot, t.Faint()
}

// StatusBar draws the bottom line: transient messages on the left, the keyboard
// legend on the right.
//
// Transient status replaces itself rather than stacking, so the bar never grows
// a backlog of stale notices (spec tui-design-system / 状态提示条).
func (c Chrome) StatusBar(s tcell.Screen, st *client.AppState, lang i18n.Lang, w, h int) {
	if h < 2 {
		return
	}
	y := h - 1
	bar := widget.NewCanvas(s, c.T, widget.Rect{X: layout.GutterX, Y: y, W: w - 2*layout.GutterX, H: 1})
	bar.Fill(c.T.Base())

	left, leftStyle := statusContent(st, lang, c.T)
	if left != "" {
		bar.Text(0, 0, left, leftStyle)
	}

	// The legend is contextual: showing the keys that work right now is what
	// makes shortcuts discoverable without a manual
	// (spec tui-chat-client / 快捷键提示常驻).
	legend := keyLegend(st, lang)
	legendW := layout.StringWidth(legend)
	avail := bar.Rect.W - layout.StringWidth(left) - 2
	if avail > 0 {
		if legendW > avail {
			legend = layout.Truncate(legend, avail)
		}
		bar.TextRight(0, legend, c.T.Faint())
	}
}

// statusContent picks the message for the left side of the bar, most urgent
// first.
func statusContent(st *client.AppState, lang i18n.Lang, t theme.Tokens) (string, tcell.Style) {
	switch {
	case st.StatusErr && st.StatusMsg != "":
		return st.StatusMsg, t.DangerStyle()
	case st.State == client.StateDisconnected:
		if st.RetryIn > 0 {
			return fmt.Sprintf("%s · %s %s",
				i18n.Disconnected.Get(lang),
				i18n.Reconnecting.Get(lang),
				formatCountdown(st.RetryIn)), t.WarningStyle()
		}
		return i18n.Disconnected.Get(lang), t.WarningStyle()
	case st.State == client.StateAuthFailed:
		return i18n.NeedLogin.Get(lang), t.DangerStyle()
	case st.StatusMsg != "":
		return st.StatusMsg, t.Muted()
	}
	return "", t.Faint()
}

func formatCountdown(d time.Duration) string {
	secs := int(d.Seconds())
	if secs < 1 {
		secs = 1
	}
	return fmt.Sprintf("%ds", secs)
}

// keyLegend builds the shortcut hint line for the current context.
//
// Commands are shown with their Alt prefix, because that is the actual keystroke:
// a legend that said "s settings" would teach a user to type "s" into their
// message.
func keyLegend(st *client.AppState, lang i18n.Lang) string {
	parts := []string{}
	if st.ShowHelp {
		return i18n.HelpClose.Get(lang)
	}
	if st.ShowSettings || st.ShowLogin {
		return i18n.KeyBack.Get(lang)
	}
	if st.Focus == client.FocusInput {
		// SendHint already names the send key, so it stands alone here.
		parts = append(parts, i18n.SendHint.Get(lang))
	} else {
		parts = append(parts, i18n.KeySwitch.Get(lang), i18n.KeyNew.Get(lang), i18n.KeyDM.Get(lang))
	}
	parts = append(parts, i18n.KeySettings.Get(lang), i18n.KeyHelp.Get(lang))
	return strings.Join(parts, "  ")
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// ensure imports used by sibling files stay accounted for.
var _ = protocol.Version

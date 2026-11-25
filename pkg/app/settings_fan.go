package app

import (
	"time"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/client"
	"link-chat/pkg/tui/views"
	"link-chat/pkg/tui/widget"
)

// The settings page's choice mode opens its candidates as a fan from the item
// being chosen, with the quick wheel's look and animation: the page stays where
// it was, a line grows out of the item's value and the candidates light up from
// the middle; closing runs it backwards (spec tui-settings / 选择模式).

type choiceFan struct {
	opened time.Time
	// closing is the fan as it was when the choice mode ended, drawn while its
	// closing animation runs.
	closing *choiceFanShot
	// dir is the way the fan last opened, for its keys.
	dir views.FanDir
	// roll is the fan rolling from one end of the candidates to the other.
	roll *wheelRoll
}

// noteChoiceMove starts the roll when the choice jumped from one end of the
// candidates to the other, as the quick wheel does.
func (a *App) noteChoiceMove(from, to int) {
	if absDiff(from, to) > 1 {
		a.ui.ChoiceFan.roll = startRoll(from, to, a.animated())
	} else {
		a.ui.ChoiceFan.roll = nil
	}
}

type choiceFanShot struct {
	labels []string
	sel    int
	anchor widget.Rect
	keep   []widget.Rect
	at     time.Time
	colors []tcell.Color
}

func (a *App) openChoiceFan() { a.ui.ChoiceFan = choiceFan{opened: nowTime()} }

// closeChoiceFan is called as the choice mode ends; the candidates as they were
// play the closing over the page.
func (a *App) closeChoiceFan() {
	d := a.ui.SettingsDrawn
	item, ok := a.currentItem()
	if !ok || !a.animated() || d.Value.W == 0 {
		a.ui.ChoiceFan = choiceFan{}
		return
	}
	a.ui.ChoiceFan = choiceFan{closing: &choiceFanShot{
		labels: a.choiceLabels(item.ID), sel: a.ui.SettingsNav.Choice,
		anchor: d.Value, keep: a.choiceKeep(), at: nowTime(),
		colors: a.choiceColors(item.ID),
	}}
}

// choiceColors are a colour setting's candidates in their own colours, or nil
// for any other setting.
func (a *App) choiceColors(id views.SettingID) []tcell.Color {
	if !isColorSetting(id) {
		return nil
	}
	vals := a.choiceValues(id)
	out := make([]tcell.Color, len(vals))
	for i, v := range vals {
		out[i] = views.MessageColorValue(a.tokens, v)
	}
	return out
}

// choiceKeep is what stays bright besides the value the fan grows from: the
// item's whole row, and the preview, which shows the candidate.
func (a *App) choiceKeep() []widget.Rect {
	d := a.ui.SettingsDrawn
	keep := []widget.Rect{d.Item}
	if d.Preview.W > 0 {
		keep = append(keep, d.Preview)
	}
	return keep
}

func (a *App) choosing() bool {
	return a.page() == client.PageSettings && a.st.Focus == client.FocusInput &&
		a.ui.SettingsNav.Level == views.LevelChoose
}

func (a *App) choiceFanMoving(now time.Time) bool {
	if a.choosing() {
		return true
	}
	cl := a.ui.ChoiceFan.closing
	if cl != nil && now.Sub(cl.at) < views.FanCloseDuration() {
		return true
	}
	a.ui.ChoiceFan.closing = nil
	return false
}

// renderChoiceFan draws the candidate fan over the settings page.
func (a *App) renderChoiceFan(content widget.Rect) {
	now := nowTime()
	m := views.FanModel{Motion: a.animated(), Shimmer: a.animated(), Depth: a.animDepth()}
	d := a.ui.SettingsDrawn
	switch {
	case a.choosing():
		item, ok := a.currentItem()
		if !ok || d.Value.W == 0 {
			return
		}
		sel := a.ui.SettingsNav.Choice
		if s, ok := a.ui.ChoiceFan.roll.shown(now); ok {
			sel = s
		} else {
			a.ui.ChoiceFan.roll = nil
		}
		m.Labels, m.Sel, m.Since = a.choiceLabels(item.ID), sel, now.Sub(a.ui.ChoiceFan.opened)
		m.Anchor, m.Keep = d.Value, a.choiceKeep()
		m.Colors = a.choiceColors(item.ID)
	case a.choiceFanMoving(now):
		cl := a.ui.ChoiceFan.closing
		m.Labels, m.Sel, m.Anchor, m.Keep, m.Since, m.Closing = cl.labels, cl.sel, cl.anchor, cl.keep, now.Sub(cl.at), true
		m.Colors = cl.colors
	default:
		return
	}
	dir := views.FanWheel{T: a.tokens}.Render(a.scr, content, m, a.lang)
	if a.choosing() {
		a.ui.ChoiceFan.dir = dir
	}
}

package views

import (
	"time"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/i18n"
	"link-chat/pkg/tui/anim"
	"link-chat/pkg/tui/layout"
	"link-chat/pkg/tui/widget"
)

// The account list: the panel on the sign-in stage that "switch account" opens.
// Each account used on this device is a button in the sign-in button's style;
// the one under the cursor is lit and has the band sweeping across it, the same
// as the sign-in button with focus (spec tui-account-switch / 账号列表面板).

// AccountEntry is one row of the list.
type AccountEntry struct {
	Name string
	// Remembered says a sign-in is saved for this account, so choosing it signs
	// straight in.
	Remembered bool
}

// AccountsModel is what the panel needs.
type AccountsModel struct {
	Accounts []AccountEntry
	// Cursor indexes Accounts; len(Accounts) is "use another account".
	Cursor  int
	Shimmer bool
	Elapsed time.Duration
	Depth   anim.Depth
}

// accountRowH is how tall one account button is.
const accountRowH = 3

// accountsVisible is how many buttons fit in the panel on a screen h rows tall.
func accountsVisible(h, n int) int {
	room := (h - 6) / accountRowH
	if room < 1 {
		room = 1
	}
	return minInt(n, room)
}

// AccountsPanelRect is where the account list sits: centred like the sign-in
// panel, as tall as its buttons need up to what the screen allows.
func AccountsPanelRect(w, h, n int) widget.Rect {
	rows := accountsVisible(h, n+1)
	return centred(w, h, minInt(50, w-4), 4+rows*accountRowH)
}

// RenderAccounts draws the list. When there are more buttons than fit, the
// window follows the cursor.
func (v Dialog) RenderAccounts(s tcell.Screen, w, h int, m AccountsModel, lang i18n.Lang) {
	n := len(m.Accounts) + 1
	box := AccountsPanelRect(w, h, len(m.Accounts))
	c := widget.NewCanvas(s, v.T, box)
	v.paintSurface(c)
	c.BoxTitle(i18n.AccountsTitle.Get(lang), v.T.BorderStyle(), v.T.Heading())

	visible := accountsVisible(h, n)
	start := 0
	if m.Cursor >= visible {
		start = m.Cursor - visible + 1
	}
	bw := box.W - 8
	for i := 0; i < visible && start+i < n; i++ {
		idx := start + i
		label := i18n.AccountsOther.Get(lang)
		note := ""
		if idx < len(m.Accounts) {
			a := m.Accounts[idx]
			label = a.Name
			note = i18n.AccountsNone.Get(lang)
			if a.Remembered {
				note = i18n.AccountsSaved.Get(lang)
			}
		}
		y := 2 + i*accountRowH
		focused := idx == m.Cursor
		v.frameButton(c, 4, y, bw, label, ButtonState{
			Focused: focused, Shimmer: m.Shimmer, Elapsed: m.Elapsed, Depth: m.Depth,
		})
		if note != "" {
			// The note sits inside the button's right end, dimmed, so the name
			// stays centred and the button keeps the sign-in button's look.
			nx := 4 + bw - 2 - layout.StringWidth(note)
			c.Text(nx, y+1, note, v.surfaceOn(v.T.Faint()))
		}
	}
}

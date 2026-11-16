package views

import (
	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/i18n"
)

// The settings page.
//
// Settings are a declarative catalog: blocks of categories, each category a list
// of items, each item read-only, an action, or a choice among values. The page's
// layout, its keys and its preview all read this one table, so a setting is added
// in one place and nothing dispatches on a label's text any more
// (design.md decision 3; spec tui-settings).

// SettingKind is what an item does when the user tries to change it.
type SettingKind int

const (
	// SettingReadOnly shows a value that cannot be changed here.
	SettingReadOnly SettingKind = iota
	// SettingChoice is picked from a list of values in the choice mode.
	SettingChoice
)

// SettingID names a setting, for the app to read and write it.
type SettingID int

const (
	SetUsername SettingID = iota
	SetServerAddr
	SetCredStore
	SetRemember
	SetTheme
	SetMotion
	SetBubbleWidth
	SetDensity
	SetTimeFormat
	SetLanguage
	SetMsgBorder
	SetMsgTime
	SetSelfText
	SetOtherText
	SetSelfBorder
	SetOtherBorder
)

// SettingsItem is one line of a category.
type SettingsItem struct {
	ID    SettingID
	Kind  SettingKind
	Label i18n.T
}

// SettingsCategory is one entry of the left column.
type SettingsCategory struct {
	Title i18n.T
	Items []SettingsItem
	// Preview says the category changes how chat looks, so a small chat preview
	// is shown under its items.
	Preview bool
}

// SettingsBlock is a titled group of categories in the left column.
type SettingsBlock struct {
	Title      i18n.T
	Categories []SettingsCategory
}

// SettingsCatalog is the whole settings tree, in display order.
var SettingsCatalog = []SettingsBlock{
	{Title: i18n.SetBlockPersonal, Categories: []SettingsCategory{
		{Title: i18n.SetAccount, Items: []SettingsItem{
			{SetUsername, SettingReadOnly, i18n.SetUser},
			{SetServerAddr, SettingReadOnly, i18n.SetServer},
			{SetCredStore, SettingReadOnly, i18n.SetCredStore},
			{SetRemember, SettingChoice, i18n.SetRemember},
		}},
	}},
	{Title: i18n.SetBlockInterface, Categories: []SettingsCategory{
		{Title: i18n.SetAppearance, Preview: true, Items: []SettingsItem{
			{SetTheme, SettingChoice, i18n.SetTheme},
			{SetMotion, SettingChoice, i18n.SetMotion},
		}},
		{Title: i18n.SetChat, Preview: true, Items: []SettingsItem{
			{SetBubbleWidth, SettingChoice, i18n.SetBubbleWidth},
			{SetDensity, SettingChoice, i18n.SetDensity},
			{SetTimeFormat, SettingChoice, i18n.SetTimeFormat},
		}},
		{Title: i18n.SetMsgStyle, Preview: true, Items: []SettingsItem{
			{SetMsgBorder, SettingChoice, i18n.SetMsgBorder},
			{SetMsgTime, SettingChoice, i18n.SetMsgTime},
			{SetSelfText, SettingChoice, i18n.SetSelfText},
			{SetOtherText, SettingChoice, i18n.SetOtherText},
			{SetSelfBorder, SettingChoice, i18n.SetSelfBorder},
			{SetOtherBorder, SettingChoice, i18n.SetOtherBorder},
		}},
	}},
	{Title: i18n.SetBlockSystem, Categories: []SettingsCategory{
		{Title: i18n.SetGeneral, Items: []SettingsItem{
			{SetLanguage, SettingChoice, i18n.SetLanguage},
		}},
	}},
}

// AccountAction is one of the operations at the foot of the category column.
// They are not categories — they have no items — and each asks for confirmation
// before doing anything (spec tui-settings / 分类栏底部的账号操作).
type AccountAction int

const (
	ActionSwitchAccount AccountAction = iota
	ActionSignOut
)

// AccountActions are the foot operations, top to bottom.
var AccountActions = []struct {
	Action AccountAction
	Title  i18n.T
	Note   i18n.T
}{
	{ActionSwitchAccount, i18n.SetSwitchAccount, i18n.SwitchAccountNote},
	{ActionSignOut, i18n.SetLogout, i18n.SignOutNote},
}

// ColumnLen is how many rows the category column's cursor can rest on: every
// category, then every account action.
func ColumnLen() int { return len(Categories()) + len(AccountActions) }

// ActionAt reports whether column index i is an account action, and which.
func ActionAt(i int) (AccountAction, bool) {
	j := i - len(Categories())
	if j < 0 || j >= len(AccountActions) {
		return 0, false
	}
	return AccountActions[j].Action, true
}

// Categories flattens the catalog's categories in order; a category's index here
// is how the page refers to it.
func Categories() []SettingsCategory {
	var out []SettingsCategory
	for _, b := range SettingsCatalog {
		out = append(out, b.Categories...)
	}
	return out
}

// SettingsLevel is which column has the keyboard.
type SettingsLevel int

const (
	LevelCategory SettingsLevel = iota
	LevelItem
	LevelChoose
)

// SettingsNav is where the user is in the settings page.
type SettingsNav struct {
	Level SettingsLevel
	// Cat indexes Categories(); Item indexes that category's Items.
	Cat, Item int
	// Choice is the candidate under the wheel's centre in the choice mode, an
	// index into the item's choices. Original is the index the setting had when
	// the mode was entered, which "cancel" goes back to.
	Choice, Original int
}

// SettingsAction is what a key asks the app to do beyond moving around.
type SettingsAction int

const (
	ActNone SettingsAction = iota
	// ActLeave: leave the settings page for the conversation list.
	ActLeave
	// ActEnterChoose: the choice mode was entered; the app fills in Choice and
	// Original from the setting's current value.
	ActEnterChoose
	// ActCommit: save Choice as the item's new value.
	ActCommit
	// ActCancel: the choice mode was left without saving.
	ActCancel
	// ActInvoke: run the account action under the cursor (it asks for
	// confirmation first).
	ActInvoke
)

// Key moves the settings page for one key. choices is how many values the item
// under the cursor has, which only the choice mode needs. It returns the new
// position and what, if anything, the app must do.
//
// ← and → move between the levels and Esc goes back one level; ↑ and ↓ move
// within a level and wrap round at its ends (spec tui-settings / 层级之间的左右切换、
// 选择模式).
func (n SettingsNav) Key(k tcell.Key, r rune, choices int) (SettingsNav, SettingsAction) {
	cats := Categories()
	if n.Cat < 0 || n.Cat >= ColumnLen() {
		n.Cat = 0
	}
	var items []SettingsItem
	if n.Cat < len(cats) {
		items = cats[n.Cat].Items
	}
	up := k == tcell.KeyUp || (k == tcell.KeyRune && r == 'k')
	down := k == tcell.KeyDown || (k == tcell.KeyRune && r == 'j')

	switch n.Level {
	case LevelCategory:
		switch {
		case up:
			n.Cat = wrapStep(n.Cat, -1, ColumnLen())
		case down:
			n.Cat = wrapStep(n.Cat, 1, ColumnLen())
		case k == tcell.KeyRight || k == tcell.KeyEnter:
			if _, ok := ActionAt(n.Cat); ok {
				return n, ActInvoke
			}
			n.Level, n.Item = LevelItem, firstActionable(items)
		case k == tcell.KeyLeft || k == tcell.KeyEscape:
			return n, ActLeave
		}
	case LevelItem:
		switch {
		case up && len(items) > 0:
			n.Item = wrapStep(n.Item, -1, len(items))
		case down && len(items) > 0:
			n.Item = wrapStep(n.Item, 1, len(items))
		case k == tcell.KeyLeft || k == tcell.KeyEscape:
			n.Level = LevelCategory
		case k == tcell.KeyRight || k == tcell.KeyEnter:
			if n.Item < 0 || n.Item >= len(items) {
				break
			}
			if items[n.Item].Kind == SettingChoice {
				n.Level = LevelChoose
				return n, ActEnterChoose
			}
		}
	case LevelChoose:
		switch {
		case up && choices > 0:
			n.Choice = wrapStep(n.Choice, -1, choices)
		case down && choices > 0:
			n.Choice = wrapStep(n.Choice, 1, choices)
		case k == tcell.KeyRight || k == tcell.KeyEnter:
			n.Level = LevelItem
			return n, ActCommit
		case k == tcell.KeyLeft || k == tcell.KeyEscape:
			n.Level, n.Choice = LevelItem, n.Original
			return n, ActCancel
		}
	}
	return n, ActNone
}

// wrapStep moves i by d within 0..n-1, wrapping round at either end.
func wrapStep(i, d, n int) int {
	if n <= 0 {
		return 0
	}
	return ((i+d)%n + n) % n
}

// firstActionable is the first item that does something, which is where → into a
// category lands. Read-only items can still be reached with ↑ ↓ — they are part
// of the page — they are just not where the cursor starts. A category with only
// read-only items puts the cursor on its first item.
func firstActionable(items []SettingsItem) int {
	for i, it := range items {
		if it.Kind != SettingReadOnly {
			return i
		}
	}
	return 0
}

package app

import (
	"fmt"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/i18n"
	"link-chat/pkg/protocol"
	"link-chat/pkg/tui/theme"
	"link-chat/pkg/tui/views"
)

// The settings page's values: what each setting is now, what it can be, and how a
// chosen value is applied. The page and its keys are in views; this is the part
// that knows the app (spec tui-settings / 设置项及其取值).

// themeNames are the built-in themes, in the order they are offered.
var themeNames = []string{"ink", "paper"}

// choiceValues are the stored values of a choice setting, in the order they are
// offered. The page shows choiceLabels; a commit stores choiceValues[i].
func (a *App) choiceValues(id views.SettingID) []string {
	switch id {
	case views.SetRemember, views.SetMotion:
		return []string{"on", "off"}
	case views.SetTheme:
		return themeNames
	case views.SetBubbleWidth:
		out := make([]string, len(BubbleWidths))
		for i, w := range BubbleWidths {
			out[i] = fmt.Sprint(w)
		}
		return out
	case views.SetDensity:
		return Densities
	case views.SetTimeFormat:
		return TimeFormats
	case views.SetLanguage:
		return []string{string(i18n.ZH), string(i18n.EN)}
	case views.SetMsgBorder, views.SetMsgTime:
		return []string{"show", "hide"}
	case views.SetPlaza:
		return []string{"on", "off"}
	case views.SetWheelKey:
		return protocol.WheelKeys
	case views.SetSelfText, views.SetOtherText, views.SetSelfBorder, views.SetOtherBorder:
		return views.MessageColors
	}
	return nil
}

// choiceLabels are how a choice setting's values read on the page.
func (a *App) choiceLabels(id views.SettingID) []string {
	vals := a.choiceValues(id)
	out := make([]string, len(vals))
	for i, v := range vals {
		out[i] = a.valueLabel(id, v)
	}
	return out
}

func (a *App) valueLabel(id views.SettingID, v string) string {
	switch id {
	case views.SetRemember, views.SetMotion, views.SetPlaza:
		if v == "on" {
			return i18n.SetOn.Get(a.lang)
		}
		if id == views.SetRemember {
			return rememberLabel(false, a.lang)
		}
		return i18n.SetOff.Get(a.lang)
	case views.SetTheme:
		if v == "paper" {
			return i18n.ThemePaper.Get(a.lang)
		}
		return i18n.ThemeInk.Get(a.lang)
	case views.SetBubbleWidth:
		return v + "%"
	case views.SetDensity:
		if v == "compact" {
			return i18n.DensityCompact.Get(a.lang)
		}
		return i18n.DensityComfort.Get(a.lang)
	case views.SetTimeFormat:
		if v == "12h" {
			return i18n.Time12h.Get(a.lang)
		}
		return i18n.Time24h.Get(a.lang)
	case views.SetLanguage:
		return langName(i18n.Lang(v))
	case views.SetMsgBorder, views.SetMsgTime:
		if v == "hide" {
			return i18n.SetHide.Get(a.lang)
		}
		return i18n.SetShow.Get(a.lang)
	case views.SetSelfText, views.SetOtherText, views.SetSelfBorder, views.SetOtherBorder:
		return views.MessageColorName(v, a.lang)
	case views.SetWheelKey:
		return views.WheelKeyName(v)
	}
	return v
}

// currentValue is a choice setting's stored value now.
func (a *App) currentValue(id views.SettingID) string {
	onOff := func(b bool) string {
		if b {
			return "on"
		}
		return "off"
	}
	lay := a.cfg.Layout()
	switch id {
	case views.SetRemember:
		return onOff(a.cfg.Remember())
	case views.SetMotion:
		return onOff(!a.st.MotionOff)
	case views.SetPlaza:
		return onOff(a.cfg.Plaza == "on")
	case views.SetWheelKey:
		return a.wheelKey()
	case views.SetTheme:
		return a.st.ThemeName
	case views.SetBubbleWidth:
		return fmt.Sprint(lay.BubblePercent)
	case views.SetDensity:
		if lay.Compact {
			return "compact"
		}
		return "comfortable"
	case views.SetTimeFormat:
		if lay.Clock24 {
			return "24h"
		}
		return "12h"
	case views.SetLanguage:
		return string(a.lang)
	case views.SetMsgBorder:
		return showHide(!lay.NoBorder)
	case views.SetMsgTime:
		return showHide(!lay.NoTime)
	case views.SetSelfText:
		return colorOr(lay.SelfText)
	case views.SetOtherText:
		return colorOr(lay.OtherText)
	case views.SetSelfBorder:
		return colorOr(lay.SelfBorder)
	case views.SetOtherBorder:
		return colorOr(lay.OtherBorder)
	}
	return ""
}

func showHide(on bool) string {
	if on {
		return "show"
	}
	return "hide"
}

// colorOr is a layout's colour choice as stored: "" is the default.
func colorOr(v string) string {
	if v == "" {
		return "default"
	}
	return v
}

// settingText is the text a setting shows beside its label.
func (a *App) settingText(id views.SettingID) string {
	switch id {
	case views.SetUsername:
		return a.st.Username
	case views.SetServerAddr:
		return a.st.ServerURL
	case views.SetCredStore:
		return credStoreName(a.creds, a.lang)
	}
	return a.valueLabel(id, a.currentValue(id))
}

// indexOf is the position of the current value among the choices, 0 if absent.
func (a *App) currentIndex(id views.SettingID) int {
	cur := a.currentValue(id)
	for i, v := range a.choiceValues(id) {
		if v == cur {
			return i
		}
	}
	return 0
}

// currentItem is the settings item under the cursor.
func (a *App) currentItem() (views.SettingsItem, bool) {
	cats := views.Categories()
	n := a.ui.SettingsNav
	if n.Cat < 0 || n.Cat >= len(cats) {
		return views.SettingsItem{}, false
	}
	items := cats[n.Cat].Items
	if n.Item < 0 || n.Item >= len(items) {
		return views.SettingsItem{}, false
	}
	return items[n.Item], true
}

// handleSettingsPageKey drives the settings page while it has the keyboard.
func (a *App) handleSettingsPageKey(ev *tcell.EventKey) {
	item, _ := a.currentItem()
	choices := len(a.choiceValues(item.ID))
	k := ev.Key()
	if a.ui.SettingsNav.Level == views.LevelChoose {
		// The candidates follow their fan: turn along it, go on along the line
		// to keep one, back against it to give up.
		next, prev, out, back := views.FanKeys(a.ui.ChoiceFan.dir)
		switch k {
		case next:
			k = tcell.KeyDown
		case prev:
			k = tcell.KeyUp
		case out:
			k = tcell.KeyEnter
		case back:
			k = tcell.KeyEscape
		}
	}
	before := a.ui.SettingsNav
	nav, act := a.ui.SettingsNav.Key(k, ev.Rune(), choices)
	a.ui.SettingsNav = nav
	if before.Level == views.LevelChoose && nav.Level == views.LevelChoose && nav.Choice != before.Choice {
		a.noteChoiceMove(before.Choice, nav.Choice)
	}
	switch act {
	case views.ActLeave:
		// Settings opens from "me", so leaving it goes back there.
		a.backToMe()
	case views.ActEnterChoose:
		i := a.currentIndex(item.ID)
		a.ui.SettingsNav.Choice, a.ui.SettingsNav.Original = i, i
		a.openChoiceFan()
	case views.ActCommit:
		a.closeChoiceFan()
		vals := a.choiceValues(item.ID)
		if nav.Choice >= 0 && nav.Choice < len(vals) && vals[nav.Choice] != a.currentValue(item.ID) {
			a.uploadSettings(a.applySetting(item.ID, vals[nav.Choice]))
			a.setStatus(i18n.SetSaved.Get(a.lang), false)
		}
		a.applyPendingSettings(settingKey(item.ID))
	case views.ActCancel:
		a.closeChoiceFan()
		a.applyPendingSettings("")
	case views.ActInvoke:
		switch act, _ := views.ActionAt(nav.Cat); act {
		case views.ActionSwitchAccount:
			// Nothing is lost by switching — this account stays remembered — so it
			// goes straight to the list.
			a.switchAccount()
		case views.ActionSignOut:
			a.openLogoutConfirm()
		}
	}
}

// applySetting stores and applies a chosen value. Nothing here runs while the
// user is still choosing: candidates only reach the preview, so moving through
// the wheel never repaints the whole interface in another language or theme.
//
// It returns the account settings to upload, or nil for a setting that stays on
// this machine (spec user-settings-sync / 账号设置的范围).
func (a *App) applySetting(id views.SettingID, v string) protocol.Settings {
	if id == views.SetRemember {
		if (v == "on") != a.cfg.Remember() {
			a.toggleRememberSession()
		}
		return nil
	}
	key := settingKey(id)
	if key == "" {
		return nil
	}
	return a.setAccountSetting(key, v)
}

// previewLayout is the chat layout the settings page's preview draws: the saved
// one, with the candidate under the wheel substituted while choosing.
func (a *App) previewLayout() (views.Layout, theme.Tokens, i18n.Lang) {
	lay, tk, lang := a.cfg.Layout(), a.tokens, a.lang
	n := a.ui.SettingsNav
	item, ok := a.currentItem()
	if !ok || n.Level != views.LevelChoose {
		return lay, tk, lang
	}
	vals := a.choiceValues(item.ID)
	if n.Choice < 0 || n.Choice >= len(vals) {
		return lay, tk, lang
	}
	v := vals[n.Choice]
	switch item.ID {
	case views.SetBubbleWidth:
		fmt.Sscan(v, &lay.BubblePercent)
	case views.SetDensity:
		lay.Compact = v == "compact"
	case views.SetTimeFormat:
		lay.Clock24 = v != "12h"
	case views.SetMsgBorder:
		lay.NoBorder = v == "hide"
	case views.SetMsgTime:
		lay.NoTime = v == "hide"
	case views.SetSelfText:
		lay.SelfText = v
	case views.SetOtherText:
		lay.OtherText = v
	case views.SetSelfBorder:
		lay.SelfBorder = v
	case views.SetOtherBorder:
		lay.OtherBorder = v
	case views.SetTheme:
		if p, ok := theme.Resolve(v); ok {
			tk = theme.NewTokens(p, a.tokens.ColorCap, a.tokens.UnicodeBox)
		}
	case views.SetLanguage:
		lang = i18n.Lang(v)
	}
	return lay, tk, lang
}

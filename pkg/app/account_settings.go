package app

import (
	"strconv"

	"link-chat/pkg/i18n"
	"link-chat/pkg/protocol"
	"link-chat/pkg/tui/views"
)

// Account settings (spec user-settings-sync).
//
// The top-level fields of Config (Theme, Language, MotionOff, the chat and
// message style fields) remain the settings in effect: every reader keeps working
// and an older client reading this file still finds the last values (design.md
// decision 8). AccountSettings caches each account's settings by username;
// switching to an account copies its cache onto the top-level fields, and every
// change copies them back.

// accountSettings is the settings in effect, as a complete protocol set.
func (c Config) accountSettings() protocol.Settings {
	motion := "on"
	if c.MotionOff {
		motion = "off"
	}
	bw := ""
	if c.BubbleWidth != 0 {
		bw = strconv.Itoa(c.BubbleWidth)
	}
	return protocol.Settings{
		"theme":        c.Theme,
		"motion":       motion,
		"language":     c.Language,
		"bubble_width": bw,
		"density":      c.Density,
		"time_format":  c.TimeFormat,
		"msg_border":   c.MsgBorder,
		"msg_time":     c.MsgTime,
		"self_text":    c.SelfText,
		"other_text":   c.OtherText,
		"self_border":  c.SelfBorder,
		"other_border": c.OtherBorder,
		"plaza":        c.Plaza,
		"wheel_key":    c.WheelKey,
	}.Normalize()
}

// setAccountSettings puts s in effect. Invalid values fall back to defaults.
func (c *Config) setAccountSettings(s protocol.Settings) {
	n := s.Normalize()
	c.Theme = n["theme"]
	c.MotionOff = n["motion"] == "off"
	c.Language = n["language"]
	c.BubbleWidth, _ = strconv.Atoi(n["bubble_width"])
	c.Density = n["density"]
	c.TimeFormat = n["time_format"]
	c.MsgBorder = n["msg_border"]
	c.MsgTime = n["msg_time"]
	c.SelfText = n["self_text"]
	c.OtherText = n["other_text"]
	c.SelfBorder = n["self_border"]
	c.OtherBorder = n["other_border"]
	c.Plaza = n["plaza"]
	c.WheelKey = n["wheel_key"]
}

// cacheAccountSettings stores the settings in effect as name's cache.
func (c *Config) cacheAccountSettings(name string) {
	if name == "" {
		return
	}
	if c.AccountSettings == nil {
		c.AccountSettings = map[string]protocol.Settings{}
	}
	c.AccountSettings[name] = c.accountSettings()
}

// useAccountSettings puts name's cache in effect, or the defaults when name has
// none. It reports whether a cache was found.
func (c *Config) useAccountSettings(name string) bool {
	if s, ok := c.AccountSettings[name]; ok && name != "" {
		c.setAccountSettings(s)
		return true
	}
	c.setAccountSettings(protocol.DefaultSettings())
	return false
}

// migrateAccountSettings moves the settings of a config written before they
// travelled with the account into the cache of the account they belonged to:
// the most recent one, or the configured username. With no account at all they
// stay in effect, and the first sign-in caches them under that account.
func (c *Config) migrateAccountSettings() {
	if len(c.AccountSettings) > 0 {
		return
	}
	name := c.Username
	if len(c.Accounts) > 0 {
		name = c.Accounts[0]
	}
	c.cacheAccountSettings(name)
}

// settingKey is the protocol key of a settings page item, or "" for a setting
// that stays on this machine.
func settingKey(id views.SettingID) string {
	switch id {
	case views.SetTheme:
		return "theme"
	case views.SetMotion:
		return "motion"
	case views.SetLanguage:
		return "language"
	case views.SetBubbleWidth:
		return "bubble_width"
	case views.SetDensity:
		return "density"
	case views.SetTimeFormat:
		return "time_format"
	case views.SetMsgBorder:
		return "msg_border"
	case views.SetMsgTime:
		return "msg_time"
	case views.SetSelfText:
		return "self_text"
	case views.SetOtherText:
		return "other_text"
	case views.SetSelfBorder:
		return "self_border"
	case views.SetOtherBorder:
		return "other_border"
	case views.SetPlaza:
		return "plaza"
	case views.SetWheelKey:
		return "wheel_key"
	}
	return ""
}

// applyAccountSettings puts s in effect everywhere — the config and this
// account's cache, the theme's tokens, the language, the animation switch, the
// message layout — and saves the config. Everything that changes account
// settings comes through here: the settings page, a download after sign-in, an
// update pushed from another connection.
func (a *App) applyAccountSettings(s protocol.Settings) {
	a.cfg.setAccountSettings(s)
	a.cfg.cacheAccountSettings(a.st.Username)

	if a.st.ThemeName != a.cfg.Theme {
		a.st.ThemeName = a.cfg.Theme
		a.applyTheme()
	}
	if lang := i18n.Parse(a.cfg.Language); lang != a.lang {
		a.lang = lang
		a.st.Lang = string(lang)
	}
	if a.cfg.MotionOff != a.st.MotionOff {
		a.st.MotionOff = a.cfg.MotionOff
		if a.st.MotionOff {
			// One switch controls every animation: nothing is left part-way.
			a.anim.slide.MoveTo(a.regionX(a.st.Focus), nowTime(), 0, false)
			a.settleFold()
		}
	}
	if !a.previewing {
		_ = SaveConfig(a.cfgPath, a.cfg)
	}
	a.markDirty()
}

// previewSetting puts a candidate in effect everywhere without saving or
// uploading it, so the page shows what it would look like.
func (a *App) previewSetting(key, v string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.previewing = true
	defer func() { a.previewing = false }()
	s := a.cfg.accountSettings()
	s[key] = v
	a.applyAccountSettings(s)
}

// setAccountSetting changes one account setting and returns the patch to upload.
// It is the entry for changes made on this machine, which arrive on the main
// loop without a.mu; it takes the lock, because downloads and pushed updates
// change the same settings from the network goroutine.
func (a *App) setAccountSetting(key, v string) protocol.Settings {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.cfg.accountSettings()
	s[key] = v
	a.applyAccountSettings(s)
	return protocol.Settings{key: v}
}

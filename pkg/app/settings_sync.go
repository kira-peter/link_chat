package app

import (
	"context"
	"encoding/json"
	"time"

	"link-chat/pkg/i18n"
	"link-chat/pkg/protocol"
	"link-chat/pkg/tui/views"
)

// Settings sync (spec user-settings-sync). Network calls run off the main loop;
// the results are applied under a.mu like any other server reply.

const settingsCallTimeout = 10 * time.Second

// settingsCall is the network side of the settings requests. Tests replace it.
func (a *App) settingsCall(op string, data any) (protocol.Frame, error) {
	if a.settingsCallFn != nil {
		return a.settingsCallFn(op, data)
	}
	if a.tr == nil {
		return protocol.Frame{}, errNoTransport
	}
	ctx, cancel := context.WithTimeout(context.Background(), settingsCallTimeout)
	defer cancel()
	return a.tr.Call(ctx, op, data, settingsCallTimeout)
}

// syncSettingsAfterSignIn downloads the account's settings and puts them in
// effect; an account the server has none for gets this machine's settings as
// its first. A failure keeps the cache and says nothing: the user is signed in
// either way (spec user-settings-sync / 登录后下载并应用、首次上传本机设置).
func (a *App) syncSettingsAfterSignIn() {
	a.mu.Lock()
	a.settingsUnsupported = false
	a.mu.Unlock()

	f, err := a.settingsCall(protocol.OpSettingsGet, nil)
	if err != nil {
		return
	}
	if f.Type == protocol.TypeError {
		if errorCode(f) == protocol.CodeInvalidArgument {
			// settings.get has no arguments: an older server that does not know it.
			a.mu.Lock()
			a.settingsUnsupported = true
			a.mu.Unlock()
		}
		return
	}
	var res protocol.SettingsGetRes
	if decodeData(f, &res) != nil {
		return
	}

	a.mu.Lock()
	if !res.Exists {
		local := a.cfg.accountSettings()
		a.cfg.cacheAccountSettings(a.st.Username)
		_ = SaveConfig(a.cfgPath, a.cfg)
		a.mu.Unlock()
		a.putSettings(local, false)
		return
	}
	a.applyOrDefer(res.Settings)
	a.mu.Unlock()
}

// uploadSettings sends a change made on this machine, in the background.
func (a *App) uploadSettings(patch protocol.Settings) {
	a.mu.Lock()
	skip := a.settingsUnsupported || a.tr == nil && a.settingsCallFn == nil
	a.mu.Unlock()
	if len(patch) == 0 || skip {
		return
	}
	go a.putSettings(patch, true)
}

// putSettings saves patch on the server. A failure keeps the local change and,
// when the user made it, says it did not reach the server
// (spec user-settings-sync / 修改后上传).
func (a *App) putSettings(patch protocol.Settings, tell bool) {
	f, err := a.settingsCall(protocol.OpSettingsPut, protocol.SettingsPutReq{Settings: patch})
	if err == nil && f.Type == protocol.TypeRes {
		return
	}
	if !tell {
		return
	}
	a.mu.Lock()
	a.setStatus(i18n.SettingsNotSynced.Get(a.lang), true)
	a.mu.Unlock()
	a.markDirty()
}

// onSettingsUpdated applies settings saved by another connection of the account.
func (a *App) onSettingsUpdated(data json.RawMessage) {
	var res protocol.SettingsRes
	if json.Unmarshal(data, &res) != nil {
		return
	}
	a.mu.Lock()
	a.applyOrDefer(res.Settings)
	a.mu.Unlock()
}

// applyOrDefer puts settings from the server in effect, or keeps them for when
// the choice mode ends: changing what is being browsed under the user would make
// the wheel jump. Caller holds a.mu.
func (a *App) applyOrDefer(s protocol.Settings) {
	if a.ui.SettingsNav.Level == views.LevelChoose {
		a.pendingSettings = s
		return
	}
	a.applyAccountSettings(s)
}

// applyPendingSettings is called when the choice mode ends, on the main loop. A
// setting the user just committed keeps the committed value: that is the newer
// one, and its upload is already on its way.
func (a *App) applyPendingSettings(committed string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.pendingSettings
	if s == nil {
		return
	}
	a.pendingSettings = nil
	if committed != "" {
		s[committed] = a.cfg.accountSettings()[committed]
	}
	a.applyAccountSettings(s)
}

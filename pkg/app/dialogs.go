package app

import (
	"context"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/client"
	"link-chat/pkg/i18n"
	"link-chat/pkg/protocol"
	"link-chat/pkg/tui/views"
)

// handleDialogKey routes keys while a modal is open.
//
// Nothing reaches the main interface from here: a dialog owns the keyboard until
// it closes, which is what makes Esc-to-back predictable.
func (a *App) handleDialogKey(ctx context.Context, ev *tcell.EventKey) {
	switch a.ui.Dialog {
	case views.DialogLogin:
		a.handleLoginKey(ctx, ev)
	case views.DialogRegister:
		a.handleRegisterKey(ctx, ev)
	case views.DialogRecover:
		a.handleRecoverKey(ctx, ev)
	case views.DialogChannel:
		a.handlePromptKey(ctx, ev, true)
	case views.DialogDM:
		a.handlePromptKey(ctx, ev, false)
	case views.DialogHelp:
		a.handleHelpKey(ev)
	case views.DialogSettings:
		a.handleSettingsKey(ev)
	case views.DialogConfirm:
		a.handleConfirmKey(ev)
	case views.DialogSessionReplaced:
		a.handleSessionReplacedKey(ctx, ev)
	}
}

// handleLoginKey drives the sign-in panel.
//
// The panel is a small form: two fields, a button and two links. Tab walks all
// five, Enter activates whatever has focus, and a link that has focus can also
// be opened with Space — the same contract a browser form has, which is what
// people already expect from a login box
// (spec tui-chat-client / 登录面板结构).
func (a *App) handleLoginKey(ctx context.Context, ev *tcell.EventKey) {
	m := &a.ui.Login

	switch ev.Key() {
	case tcell.KeyEscape:
		// Esc on the login screen quits only when a session exists; otherwise
		// there is nowhere to go back to.
		if a.st.State == client.StateReady {
			a.ui.Dialog = views.DialogNone
		} else {
			a.ui.Quitting = true
		}
		return
	case tcell.KeyTab, tcell.KeyDown:
		a.setLoginFocus(m.Focus.Next())
		return
	case tcell.KeyBacktab, tcell.KeyUp:
		a.setLoginFocus(m.Focus.Prev())
		return
	case tcell.KeyEnter:
		switch m.Focus {
		case views.LoginFocusForgot:
			a.openRecover()
		case views.LoginFocusRegister:
			a.openRegister()
		default:
			a.submitLogin(ctx)
		}
		return
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		a.loginBackspace()
		return
	case tcell.KeyRune:
		r := ev.Rune()
		if r == 0 {
			return
		}
		// Space activates a focused link, matching browser behaviour.
		if r == ' ' && !m.Focus.IsField() {
			switch m.Focus {
			case views.LoginFocusForgot:
				a.openRecover()
			case views.LoginFocusRegister:
				a.openRegister()
			}
			return
		}
		switch m.Focus {
		case views.LoginFocusUsername:
			m.Username += string(r)
			// A saved session belongs to one account: a different name is a
			// different login.
			a.forgetSavedSession()
		case views.LoginFocusPassword:
			m.Password += string(r)
			// Typing a password means the user wants a password login.
			a.forgetSavedSession()
		default:
			return
		}
		m.Err = ""
	}
}

func (a *App) loginBackspace() {
	m := &a.ui.Login
	trim := func(s string) string {
		if s == "" {
			return s
		}
		r := []rune(s)
		return string(r[:len(r)-1])
	}
	switch m.Focus {
	case views.LoginFocusUsername:
		m.Username = trim(m.Username)
		a.forgetSavedSession()
	case views.LoginFocusPassword:
		m.Password = trim(m.Password)
		a.forgetSavedSession()
	}
}

// submitLogin dials (if needed) and authenticates.
//
// The server address is not part of the form; it comes from configuration and is
// only echoed back on failure so the user can tell a bad address from a dead
// server (design.md decision 6).
func (a *App) submitLogin(ctx context.Context) {
	m := &a.ui.Login
	user := strings.TrimSpace(m.Username)
	pass := m.Password

	if user == "" {
		m.Err = i18n.LoginUsername.Get(a.lang)
		m.Focus = views.LoginFocusUsername
		a.markDirty()
		return
	}
	// A remembered login needs no password: the saved session stands in for it.
	// Only an ordinary login insists on one.
	if !m.Remembered && pass == "" {
		m.Err = i18n.LoginPassword.Get(a.lang)
		m.Focus = views.LoginFocusPassword
		a.markDirty()
		return
	}
	useSaved := m.Remembered && a.st.Token != ""

	// The address is configuration, not form input. It is normalised once here so
	// a bare host in the config file still connects.
	server := normaliseServerURL(a.cfg.Server)
	if server == "" {
		m.Err = i18n.ServerDown.Get(a.lang)
		a.markDirty()
		return
	}

	m.Busy = true
	m.Err = ""
	a.st.ServerURL = server
	a.markDirty()

	go func() {
		// Tear down any previous transport before dialing again.
		if a.tr != nil {
			a.tr.Close()
			a.tr = nil
		}
		a.connectForLogin(ctx)
		if a.tr == nil {
			a.failLogin(i18n.ServerDown.Get(a.lang))
			return
		}

		// Wait for the transport to become usable before issuing a request.
		if !a.waitConnected(10 * time.Second) {
			a.failLogin(i18n.ServerDown.Get(a.lang))
			return
		}

		if useSaved {
			a.callResume(ctx, user)
			return
		}
		a.callLogin(ctx, user, pass)
	}()
}

// callResume signs in with the saved session instead of a password.
//
// This is where a restart actually logs in. It used to happen automatically at
// startup, which meant merely opening the client displaced the account's session
// on another device without the user ever deciding to; now it happens when the
// user presses the button (design.md decision 2).
func (a *App) callResume(ctx context.Context, user string) {
	f, err := a.tr.Call(ctx, protocol.OpAuthResume, protocol.ResumeReq{
		Token: a.st.Token, Device: a.device,
	}, 20*time.Second)
	if err != nil {
		// A network failure is not evidence the session is bad. The saved login is
		// kept so the user can simply try again; throwing it away because the
		// server was briefly unreachable would cost them their no-password login.
		a.failLoginKeepRemembered(i18n.ServerDown.Get(a.lang))
		return
	}
	if f.Type == protocol.TypeError {
		code := errorCode(f)
		if code == protocol.CodeTokenExpired || code == protocol.CodeUnauthorized {
			a.fallBackToPassword(code)
			return
		}
		a.failLoginKeepRemembered(i18n.ErrText(code, "").Get(a.lang))
		return
	}
	a.finishAuth(f, user)
}

// fallBackToPassword turns a failed resume into an ordinary password sign-in.
//
// The saved token is dead for one of three reasons — expired, revoked, or taken
// over by a login elsewhere — and none of them is recoverable by retrying. So the
// remembered state is dropped, the reason is shown, and the caret moves to the
// password so the user can carry straight on.
func (a *App) fallBackToPassword(code string) {
	a.mu.Lock()
	a.st.Token = ""
	a.persistTokenLocked("")
	a.ui.Login.Remembered = false
	a.ui.Login.Busy = false
	a.ui.Login.Password = ""
	a.ui.Login.Focus = views.LoginFocusPassword
	a.ui.Login.Err = i18n.ErrText(code, "").Get(a.lang)
	a.mu.Unlock()
	a.markDirty()
}

// failLoginKeepRemembered reports a failure without discarding the saved session.
func (a *App) failLoginKeepRemembered(msg string) {
	a.mu.Lock()
	a.ui.Login.Busy = false
	a.ui.Login.Err = msg
	a.mu.Unlock()
	a.markDirty()
}

// forgetSavedSession abandons the remembered login in favour of a password one.
//
// It is called whenever the user signals they want something else: editing the
// username (the token belongs to a specific account), or typing into the password
// (design.md decision 1). The token itself is left in the store; it is only the
// panel that stops offering it.
func (a *App) forgetSavedSession() {
	m := &a.ui.Login
	if !m.Remembered {
		return
	}
	m.Remembered = false
	m.Err = ""
}

// normaliseServerURL accepts a bare host, a host:port, or a full ws:// URL and
// returns a WebSocket endpoint. Accepting all three is worth the few lines: it is
// the difference between "paste the address you were given" working or not.
func normaliseServerURL(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if !strings.Contains(s, "://") {
		s = "ws://" + s
	}
	if !strings.HasSuffix(s, "/ws") {
		s = strings.TrimSuffix(s, "/") + "/ws"
	}
	return s
}

// waitConnected blocks until the socket is up or the deadline passes.
func (a *App) waitConnected(d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		s := a.st.State
		if s == client.StateAuthenticating || s == client.StateReady {
			return true
		}
		if s == client.StateDisconnected && a.st.RetryIn > 0 {
			// The dial already failed once; do not spin.
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

func (a *App) callLogin(ctx context.Context, user, pass string) {
	// The device name is what the displaced client is shown, so it has to travel
	// with the login rather than being inferred by the server.
	f, err := a.tr.Call(ctx, protocol.OpAuthLogin, protocol.LoginReq{
		Username: user, Password: pass, Device: a.device,
	}, 20*time.Second)
	if err != nil {
		a.failLogin(i18n.ServerDown.Get(a.lang))
		return
	}
	a.finishAuth(f, user)
}

func (a *App) callRegister(ctx context.Context, user, pass string) {
	f, err := a.tr.Call(ctx, protocol.OpAuthRegister, protocol.RegisterReq{
		Username: user, Password: pass,
	}, 20*time.Second)
	if err != nil {
		a.failLogin(i18n.ServerDown.Get(a.lang))
		return
	}
	a.finishAuth(f, user)
}

// finishAuth applies the result of login or register.
func (a *App) finishAuth(f protocol.Frame, username string) {
	if f.Type == protocol.TypeError {
		code := errorCode(f)
		msg := i18n.ErrText(code, "").Get(a.lang)
		a.mu.Lock()
		a.ui.Login.Busy = false
		a.ui.Login.Err = msg
		// Put the caret where the fix belongs, so the user does not have to hunt
		// for the field the server complained about.
		if code == protocol.CodeUsernameTaken || code == protocol.CodeInvalidUsername {
			a.ui.Login.Focus = views.LoginFocusUsername
		}
		if code == protocol.CodeWeakPassword {
			a.ui.Login.Focus = views.LoginFocusPassword
		}
		a.mu.Unlock()
		a.markDirty()
		return
	}

	var res protocol.AuthRes
	if err := decodeData(f, &res); err != nil {
		a.failLogin(i18n.LoginFailed.Get(a.lang))
		return
	}

	a.mu.Lock()
	a.st.Token = res.Token
	a.st.Username = res.User.Username
	if a.st.Username == "" {
		a.st.Username = username
	}
	a.st.UserID = res.User.ID
	a.persistTokenLocked(res.Token)
	a.cfg.Username = a.st.Username
	a.cfg.UserID = res.User.ID
	a.ui.Login.Busy = false
	a.ui.Login.Err = ""
	a.mu.Unlock()

	_ = SaveConfig(a.cfgPath, a.cfg)
	a.onAuthenticated(res)
}

func (a *App) failLogin(msg string) {
	a.mu.Lock()
	a.ui.Login.Busy = false
	a.ui.Login.Err = msg
	a.mu.Unlock()
	a.markDirty()
}

// handlePromptKey edits the one-field create dialogs.
func (a *App) handlePromptKey(ctx context.Context, ev *tcell.EventKey, isChannel bool) {
	m := &a.ui.Prompt
	switch ev.Key() {
	case tcell.KeyEscape:
		a.ui.Dialog = views.DialogNone
		return
	case tcell.KeyEnter:
		a.submitPrompt(ctx, isChannel)
		return
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if m.Value != "" {
			r := []rune(m.Value)
			m.Value = string(r[:len(r)-1])
		}
		return
	case tcell.KeyRune:
		if ev.Rune() != 0 {
			m.Value += string(ev.Rune())
			m.Err = ""
		}
	}
}

func (a *App) submitPrompt(ctx context.Context, isChannel bool) {
	m := &a.ui.Prompt
	v := strings.TrimSpace(m.Value)
	if v == "" || a.tr == nil {
		return
	}
	m.Busy = true
	op := protocol.OpChannelCreate
	body := any(protocol.ChannelCreateReq{Name: v})
	if !isChannel {
		op = protocol.OpDMOpen
		body = protocol.DMOpenReq{Username: v}
	}

	go func() {
		f, err := a.tr.Call(ctx, op, body, 20*time.Second)
		if err != nil {
			a.failPrompt(i18n.ServerDown.Get(a.lang))
			return
		}
		if f.Type == protocol.TypeError {
			code := errorCode(f)
			a.failPrompt(i18n.ErrText(code, "").Get(a.lang))
			return
		}
		var cv protocol.Conversation
		if err := decodeData(f, &cv); err != nil {
			a.failPrompt(i18n.LoginFailed.Get(a.lang))
			return
		}
		a.mu.Lock()
		a.st.SetConversations(append(a.conversationsAsList(), cv))
		// Opening the DM we just started is the only useful next step.
		a.openConversationForced(cv.ID)
		a.ui.Dialog = views.DialogNone
		a.ui.Prompt = views.PromptModel{}
		a.mu.Unlock()
		a.markDirty()
	}()
}

// conversationsAsList snapshots the current conversations for a merge.
func (a *App) conversationsAsList() []protocol.Conversation {
	out := make([]protocol.Conversation, 0, len(a.st.Order))
	for _, id := range a.st.Order {
		if cv := a.st.Conversations[id]; cv != nil {
			out = append(out, cv.Conversation)
		}
	}
	return out
}

// openConversationForced switches conversations even when the ID is already
// active, which is what a freshly created conversation needs.
func (a *App) openConversationForced(id string) {
	a.st.StashDraft()
	a.st.ActiveID = id
	a.st.LoadDraft(id)
	a.ui.Scroll = 0
	a.st.MarkRead(id)
	if a.tr != nil {
		a.joinAndLoad(id)
	}
}

func (a *App) failPrompt(msg string) {
	a.mu.Lock()
	a.ui.Prompt.Busy = false
	a.ui.Prompt.Err = msg
	a.mu.Unlock()
	a.markDirty()
}

// handleHelpKey closes the help panel on almost anything.
func (a *App) handleHelpKey(ev *tcell.EventKey) {
	switch ev.Key() {
	case tcell.KeyEscape:
		a.ui.Dialog = views.DialogNone
		return
	}
	switch ev.Rune() {
	case '?', 'q':
		a.ui.Dialog = views.DialogNone
	}
}

// handleSettingsKey drives the settings list.
func (a *App) handleSettingsKey(ev *tcell.EventKey) {
	a.refreshSettingsRows()
	rows := a.ui.Settings.Rows

	switch ev.Key() {
	case tcell.KeyEscape:
		a.ui.Dialog = views.DialogNone
		return
	case tcell.KeyUp:
		a.settingsMove(-1, rows)
		return
	case tcell.KeyDown:
		a.settingsMove(1, rows)
		return
	case tcell.KeyEnter:
		a.activateSetting()
		return
	}

	switch ev.Rune() {
	case 'k':
		a.settingsMove(-1, rows)
	case 'j':
		a.settingsMove(1, rows)
	case 'q':
		a.ui.Dialog = views.DialogNone
	}
}

func (a *App) settingsMove(delta int, rows []views.SettingsRow) {
	i := a.ui.Settings.Selected + delta
	for i > 0 && i < len(rows) && rows[i].Kind == 0 {
		i += delta
	}
	if i >= 0 && i < len(rows) && rows[i].Kind != 0 {
		a.ui.Settings.Selected = i
	}
}

// activateSetting toggles or acts on the selected row.
func (a *App) activateSetting() {
	rows := a.ui.Settings.Rows
	i := a.ui.Settings.Selected
	if i < 0 || i >= len(rows) {
		return
	}
	row := rows[i]

	switch row.Label {
	case i18n.SetTheme.Get(a.lang):
		// Cycle the two built-in themes.
		if a.st.ThemeName == "ink" {
			a.st.ThemeName = "paper"
		} else {
			a.st.ThemeName = "ink"
		}
		a.cfg.Theme = a.st.ThemeName
		a.applyTheme()
		_ = SaveConfig(a.cfgPath, a.cfg)
		a.setStatus(i18n.SetSaved.Get(a.lang), false)
	case i18n.SetLanguage.Get(a.lang):
		a.toggleLanguage()
		a.setStatus(i18n.SetSaved.Get(a.lang), false)
	case i18n.SetMotion.Get(a.lang):
		a.toggleMotion()
		a.setStatus(i18n.SetSaved.Get(a.lang), false)
	case i18n.SetRemember.Get(a.lang):
		a.toggleRememberSession()
		a.setStatus(i18n.SetSaved.Get(a.lang), false)
	case i18n.SetLogout.Get(a.lang):
		a.ui.Confirm = views.ConfirmModel{
			Title:   i18n.SetLogout.Get(a.lang),
			Message: i18n.DeleteConfirm.Get(a.lang),
			Yes:     false,
		}
		a.ui.Dialog = views.DialogConfirm
	}
}

// handleConfirmKey answers the confirmation.
func (a *App) handleConfirmKey(ev *tcell.EventKey) {
	switch ev.Key() {
	case tcell.KeyEscape:
		a.ui.Dialog = views.DialogSettings
		return
	case tcell.KeyTab, tcell.KeyLeft, tcell.KeyRight:
		a.ui.Confirm.Yes = !a.ui.Confirm.Yes
		return
	case tcell.KeyEnter:
		if a.ui.Confirm.Yes {
			a.logout()
			return
		}
		a.ui.Dialog = views.DialogSettings
		return
	}
	switch ev.Rune() {
	case 'y', 'Y':
		a.logout()
	case 'n', 'N':
		a.ui.Dialog = views.DialogSettings
	}
}

// logout revokes the session locally and returns to the login screen.
func (a *App) logout() {
	if a.tr != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = a.tr.Call(ctx, protocol.OpAuthLogout, protocol.ResumeReq{Token: a.st.Token}, 5*time.Second)
	}
	a.mu.Lock()
	a.st.Token = ""
	a.persistTokenLocked("")
	a.st.State = client.StateAuthFailed
	a.st.Conversations = map[string]*client.Conversation{}
	a.st.Order = nil
	a.st.ActiveID = ""
	a.mu.Unlock()
	_ = SaveConfig(a.cfgPath, a.cfg)
	a.openLogin()
	a.markDirty()
}

// handleSessionReplacedKey handles the "signed in elsewhere" notice.
//
// There is no dismiss-in-place: this window has no session any more, so the only
// sensible choices are to sign in again or to leave (spec tui-chat-client /
// 会话被顶掉时的界面).
func (a *App) handleSessionReplacedKey(ctx context.Context, ev *tcell.EventKey) {
	switch ev.Key() {
	case tcell.KeyEscape:
		a.ui.Quitting = true
	case tcell.KeyEnter:
		a.mu.Lock()
		username := a.ui.SessionReplaced.Username
		a.mu.Unlock()
		a.openLoginWithUsername(username)
	}
}

// openLoginWithUsername raises the sign-in panel with a username already filled
// in, which is what the recovery and displacement flows both want.
func (a *App) openLoginWithUsername(username string) {
	a.mu.Lock()
	a.ui.Login = views.LoginModel{
		Username:    username,
		Focus:       views.LoginFocusPassword,
		ServerShown: a.cfg.Server,
	}
	a.ui.Dialog = views.DialogLogin
	a.mu.Unlock()
	a.markDirty()
}

// setLoginFocus moves the sign-in panel focus and restarts the button sweep when
// the button is the new target, so the band always enters from the left.
func (a *App) setLoginFocus(f views.LoginFocus) {
	m := &a.ui.Login
	if f == m.Focus {
		return
	}
	m.Focus = f
	if f == views.LoginFocusSubmit {
		a.anim.buttonStart = nowTime()
	}
}

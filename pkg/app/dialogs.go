package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

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
	case views.DialogLogout:
		a.handleLogoutKey(ev)
	case views.DialogAccounts:
		a.handleAccountsKey(ctx, ev)
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
	case tcell.KeyRight:
		// The two links are side by side, so → and ← move between them, the way
		// the picture reads. Anywhere else they do nothing: a field has no caret
		// to move, and the rest of the panel is a single column.
		a.setLoginFocus(m.Focus.Right())
		return
	case tcell.KeyLeft:
		a.setLoginFocus(m.Focus.Left())
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
	// A climbing wait after repeated wrong passwords: the panel shows the
	// countdown, and Enter does nothing until it ends (spec tui-chat-client /
	// 倒计时期间不能提交).
	if m.RetryAt > nowTime().UnixMilli() {
		return
	}
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

	token, attempt := a.st.Token, a.authAttempt.Add(1)
	go func() {
		tr := a.authTransport(ctx, server, attempt)
		if tr == nil {
			return
		}
		if !a.waitConnected(ctx, tr, 10*time.Second) {
			if !a.lockTransport(tr) {
				return
			}
			msg := a.unreachable(server)
			if a.st.Outdated != nil {
				msg = a.outdatedMessage()
			}
			a.ui.Login.Busy = false
			a.ui.Login.Err = msg
			a.mu.Unlock()
			a.markDirty()
			return
		}
		if useSaved {
			a.callResume(ctx, tr, user, token)
			return
		}
		a.callLogin(ctx, tr, user, pass)
	}()
}

// callResume signs in with the saved session instead of a password.
//
// This is where a restart actually logs in. It used to happen automatically at
// startup, which meant merely opening the client displaced the account's session
// on another device without the user ever deciding to; now it happens when the
// user presses the button (design.md decision 2).
func (a *App) callResume(ctx context.Context, tr *client.Transport, user, token string) {
	f, err := tr.Call(ctx, protocol.OpAuthResume, protocol.ResumeReq{
		Token: token, Device: a.device,
	}, 20*time.Second)
	if err != nil {
		// A network failure is not evidence the session is bad. The saved login is
		// kept so the user can simply try again; throwing it away because the
		// server was briefly unreachable would cost them their no-password login.
		a.failLogin(tr, a.callErrText(err))
		return
	}
	if f.Type == protocol.TypeError {
		code := errorCode(f)
		if code == protocol.CodeTokenExpired || code == protocol.CodeUnauthorized {
			a.fallBackToPassword(tr, code)
			return
		}
		if code == protocol.CodeAccountBanned {
			var info protocol.SanctionInfo
			_ = decodeData(f, &info)
			a.fallBackToPassword(tr, code)
			a.failLogin(tr, a.bannedMessage(info))
			return
		}
		a.failLogin(tr, i18n.ErrText(code, "").Get(a.lang))
		return
	}
	a.finishAuth(tr, f, user)
}

// fallBackToPassword turns a failed resume into an ordinary password sign-in.
//
// The saved token is dead for one of three reasons — expired, revoked, or taken
// over by a login elsewhere — and none of them is recoverable by retrying. So the
// remembered state is dropped, the reason is shown, and the caret moves to the
// password so the user can carry straight on.
func (a *App) fallBackToPassword(tr *client.Transport, code string) {
	if !a.lockTransport(tr) {
		return
	}
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
func (a *App) waitConnected(ctx context.Context, tr *client.Transport, d time.Duration) bool {
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		if !a.lockTransport(tr) {
			return false
		}
		state, outdated := a.st.State, a.st.Outdated != nil
		a.mu.Unlock()
		if state == client.StateAuthenticating || state == client.StateReady {
			return true
		}
		if state == client.StateDisconnected || outdated {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-tick.C:
		}
	}
}

func (a *App) callLogin(ctx context.Context, tr *client.Transport, user, pass string) {
	// The device name is what the displaced client is shown, so it has to travel
	// with the login rather than being inferred by the server.
	f, err := tr.Call(ctx, protocol.OpAuthLogin, protocol.LoginReq{
		Username: user, Password: pass, Device: a.device,
	}, 20*time.Second)
	if err != nil {
		a.failLogin(tr, a.callErrText(err))
		return
	}
	a.finishAuth(tr, f, user)
}

// errorRetryAfter reads the server's suggested wait from an error frame.
func errorRetryAfter(f protocol.Frame) int64 {
	var p protocol.ResPayload
	if err := json.Unmarshal(f.Payload, &p); err != nil {
		return 0
	}
	return int64(p.RetryAfterMS)
}

// callErrText names why a request got no answer: the server stayed silent, or
// the connection went away. Both used to read "server unreachable", which sent
// people looking for a server that was in fact running.
func (a *App) callErrText(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return i18n.ServerNoReply.Get(a.lang)
	case errors.Is(err, client.ErrClosed):
		return i18n.ConnLost.Get(a.lang)
	}
	return i18n.ServerDown.Get(a.lang)
}

// finishAuth applies the result of login or register.
func (a *App) finishAuth(tr *client.Transport, f protocol.Frame, username string) {
	if f.Type == protocol.TypeError {
		code := errorCode(f)
		msg := i18n.ErrText(code, "").Get(a.lang)
		if code == protocol.CodeAccountBanned {
			var info protocol.SanctionInfo
			_ = decodeData(f, &info)
			msg = a.bannedMessage(info)
		}
		if !a.lockTransport(tr) {
			return
		}
		a.ui.Login.Busy = false
		if code == protocol.CodeRateLimited {
			// The server says exactly how long; the panel counts it down.
			if ms := errorRetryAfter(f); ms > 0 {
				a.ui.Login.RetryAt = nowTime().UnixMilli() + ms
				msg = ""
			}
		}
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
		a.failLogin(tr, i18n.LoginFailed.Get(a.lang))
		return
	}

	if !a.lockTransport(tr) {
		return
	}
	a.ui.Login.Busy = false
	a.ui.Login.Err = ""
	a.completeSignInLocked(res, username)
	a.mu.Unlock()
}

func (a *App) failLogin(tr *client.Transport, msg string) {
	if !a.lockTransport(tr) {
		return
	}
	a.ui.Login.Busy = false
	a.ui.Login.Err = msg
	a.mu.Unlock()
	a.markDirty()
}

func (a *App) canEditChannel() bool {
	cv := a.st.Active()
	return a.page() == client.PageConversation && a.st.State == client.StateReady &&
		cv != nil && cv.Kind == protocol.KindChannel && cv.Joined && cv.CreatedBy != "" && cv.CreatedBy == a.st.UserID
}

func (a *App) openChannelEditor() {
	if !a.canEditChannel() {
		return
	}
	cv := a.st.Active()
	a.ui.NewPage = views.NewPageModel{
		EditingID: cv.ID, Value: cv.Name, Topic: cv.Topic, Private: cv.Visibility == protocol.ChannelPrivate,
	}
	a.ui.Page = client.PageEditChannel
	a.moveFocus(client.FocusInput)
	a.syncMarker()
}

func (a *App) cancelChannelForm() {
	editing := a.ui.NewPage.EditingID != ""
	a.ui.NewPage = views.NewPageModel{}
	if editing {
		a.ui.Page = client.PageConversation
		a.syncMarker()
		return
	}
	a.ui.Page = client.PageWelcome
	if a.st.Active() != nil {
		a.ui.Page = client.PageConversation
	}
	a.moveFocus(client.FocusList)
}

// handleNewPageKey edits the channel form. Only its confirm button submits it.
func (a *App) handleNewPageKey(ctx context.Context, ev *tcell.EventKey) {
	m := &a.ui.NewPage
	if m.Busy {
		return
	}
	var value *string
	switch m.Field {
	case views.ChannelNameField:
		value = &m.Value
	case views.ChannelTopicField:
		value = &m.Topic
	}
	switch ev.Key() {
	case tcell.KeyEscape:
		if m.EditingID != "" {
			a.cancelChannelForm()
		} else {
			a.moveFocus(client.FocusList)
		}
		return
	case tcell.KeyEnter:
		switch m.Field {
		case views.ChannelConfirmField:
			a.submitPrompt(ctx, true)
		case views.ChannelCancelField:
			a.cancelChannelForm()
		default:
			m.Field++
		}
		return
	case tcell.KeyLeft, tcell.KeyRight:
		switch m.Field {
		case views.ChannelVisibilityField:
			m.Private = ev.Key() == tcell.KeyLeft
			m.Err = ""
		case views.ChannelConfirmField, views.ChannelCancelField:
			m.Field = views.ChannelConfirmField
			if ev.Key() == tcell.KeyRight {
				m.Field = views.ChannelCancelField
			}
		}
		return
	case tcell.KeyTab, tcell.KeyDown:
		m.Field = (m.Field + 1) % views.ChannelFieldCount
		return
	case tcell.KeyBacktab, tcell.KeyUp:
		m.Field = (m.Field + views.ChannelFieldCount - 1) % views.ChannelFieldCount
		return
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if value != nil && *value != "" {
			_, size := utf8.DecodeLastRuneInString(*value)
			*value = (*value)[:len(*value)-size]
		}
		m.Err = ""
		return
	case tcell.KeyRune:
		if m.Field == views.ChannelVisibilityField {
			if ev.Rune() == ' ' {
				m.Private = !m.Private
				m.Err = ""
			}
		} else if value != nil && ev.Rune() != 0 {
			*value += string(ev.Rune())
			m.Err = ""
		}
	}
}

func (a *App) submitPrompt(ctx context.Context, isChannel bool) {
	m := &a.ui.NewPage
	v := strings.TrimSpace(m.Value)
	tr := a.tr.Load()
	if v == "" || tr == nil {
		if isChannel && v == "" {
			m.Err = i18n.ChannelNameRequired.Get(a.lang)
			m.Field = views.ChannelNameField
		}
		return
	}
	op := protocol.OpDMOpen
	body := any(protocol.DMOpenReq{Username: v})
	editingID := m.EditingID
	if isChannel {
		topic := strings.TrimSpace(m.Topic)
		if utf8.RuneCountInString(topic) > protocol.ChannelTopicMaxRunes {
			m.Err = fmt.Sprintf(i18n.ChannelTopicLength.Get(a.lang), protocol.ChannelTopicMaxRunes)
			m.Field = views.ChannelTopicField
			return
		}
		if strings.ContainsFunc(topic, unicode.IsControl) {
			m.Err = i18n.ChannelTopicInvalid.Get(a.lang)
			m.Field = views.ChannelTopicField
			return
		}
		op = protocol.OpChannelCreate
		req := protocol.ChannelCreateReq{Name: v, Topic: topic, Visibility: protocol.ChannelPublic}
		if m.Private {
			// Older servers reject the operation instead of creating a public channel.
			op = protocol.OpChannelCreatePrivate
			req.Visibility = protocol.ChannelPrivate
		}
		body = req
		if editingID != "" {
			op = protocol.OpChannelUpdate
			body = protocol.ChannelUpdateReq{ConversationID: editingID, ChannelCreateReq: req}
		}
	}
	m.Busy = true

	go func() {
		a.conversationSync.Lock()
		defer a.conversationSync.Unlock()
		f, err := tr.Call(ctx, op, body, 20*time.Second)
		if err != nil {
			a.failPrompt(i18n.ServerDown.Get(a.lang))
			return
		}
		if f.Type == protocol.TypeError {
			var res protocol.ResPayload
			_ = json.Unmarshal(f.Payload, &res)
			msg := i18n.ErrText(res.Code, res.Message)
			if op == protocol.OpChannelUpdate && res.Code == protocol.CodeInvalidArgument && !tr.SupportsChannelUpdate() {
				msg = i18n.ChannelEditUpgrade
			}
			a.failPrompt(msg.Get(a.lang))
			return
		}
		var cv protocol.Conversation
		if err := decodeData(f, &cv); err != nil {
			a.failPrompt(i18n.LoginFailed.Get(a.lang))
			return
		}
		a.mu.Lock()
		if a.tr.Load() != tr {
			a.mu.Unlock()
			return
		}
		if editingID != "" {
			if current := a.st.Conversations[cv.ID]; current != nil {
				selected := a.selectedSidebarRow()
				current.Name, current.Topic, current.Visibility = cv.Name, cv.Topic, cv.Visibility
				a.restoreSidebarRow(selected)
			}
			if a.ui.Page == client.PageEditChannel && a.ui.NewPage.EditingID == editingID {
				a.cancelChannelForm()
			}
			a.mu.Unlock()
			a.markDirty()
			return
		}
		a.st.SetConversations(append(a.conversationsAsList(), cv))
		// Opening what we just created is the only useful next step. The keyboard
		// stays on the right, now in its composer, and the list stays folded.
		a.ui.NewPage = views.NewPageModel{}
		a.openConversationForced(cv.ID)
		a.syncFold()
		a.syncMarker()
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
	a.ui.Page = client.PageConversation
	a.ui.pending = nil
	if i := client.IndexOfConversation(a.st.SidebarRows(), id); i >= 0 {
		a.ui.Selected = i
	}
	a.st.StashDraft()
	a.st.ActiveID = id
	a.st.LoadDraft(id)
	a.ui.Scroll = 0
	a.st.MarkRead(id)
	a.syncFold()
	a.syncMarker()
	if a.tr.Load() != nil {
		a.joinAndLoad(id)
	}
}

func (a *App) failPrompt(msg string) {
	a.mu.Lock()
	a.ui.NewPage.Busy = false
	a.ui.NewPage.Err = msg
	a.mu.Unlock()
	a.markDirty()
}

// openLogoutConfirm raises the sign-out confirmation with "cancel" focused: the
// safe choice is the default, so a stray Enter keeps the user signed in.
func (a *App) openLogoutConfirm() {
	a.ui.Logout = views.LogoutModel{Focus: views.LogoutCancel}
	a.anim.buttonStart = nowTime()
	a.ui.Dialog = views.DialogLogout
}

// handleLogoutKey drives the sign-out confirmation. ← → and Tab move between the
// two buttons — they sit side by side, so the arrows follow the screen and stop at
// the edges; Tab cycles. Enter does what the focused button says and Esc is cancel
// (spec tui-login-stage / 退出登录的确认).
func (a *App) handleLogoutKey(ev *tcell.EventKey) {
	m := &a.ui.Logout
	focus := func(c views.LogoutChoice) {
		if m.Focus != c {
			m.Focus = c
			// The band enters the newly focused button from its left edge.
			a.anim.buttonStart = nowTime()
		}
	}
	switch ev.Key() {
	case tcell.KeyEscape:
		a.ui.Dialog = views.DialogNone
	case tcell.KeyLeft:
		focus(views.LogoutCancel)
	case tcell.KeyRight:
		focus(views.LogoutConfirm)
	case tcell.KeyTab, tcell.KeyBacktab:
		focus(1 - m.Focus)
	case tcell.KeyEnter:
		if m.Focus == views.LogoutConfirm {
			a.logout()
			return
		}
		// Cancel: back to the settings page, on the sign-out item.
		a.ui.Dialog = views.DialogNone
	}
}

// logout signs out: back to the sign-in panel, with this account's saved sign-in
// kept, so one Enter brings the user back (spec tui-account-switch /
// 退出登录保留登录记录).
func (a *App) logout() { a.leaveSession(false) }

// switchAccount ends this session and opens the account list.
func (a *App) switchAccount() { a.leaveSession(true) }

// leaveSession ends the session on this device and returns to the stage.
//
// It does not ask the server to revoke the token: the token is what "signed in on
// this device" means, and a revoked one could not bring the user back. Only the
// connection is closed — off the UI loop, so a slow network never freezes the
// window (spec tui-login-stage / 注销不阻塞界面).
func (a *App) leaveSession(toAccounts bool) {
	a.authAttempt.Add(1)
	// The exit animation starts from the main interface as it is now, so take its
	// picture before anything is cleared.
	a.beginExit()

	a.mu.Lock()
	if tr := a.tr.Swap(nil); tr != nil {
		closer := a.closeTransport
		if closer == nil {
			closer = func() { tr.Close() }
		}
		go closer()
	}
	user, token := a.st.Username, a.st.Token
	a.st.State = client.StateAuthFailed
	a.retryDelay = 0
	a.st.RetryIn = 0
	a.clearAccountDataLocked()
	a.mu.Unlock()
	_ = SaveConfig(a.cfgPath, a.cfg)

	if toAccounts {
		a.openAccounts(user)
	} else {
		a.ui.Login = views.LoginModel{Username: user, ServerShown: a.cfg.Server}
		a.openLogin()
		if token != "" && a.cfg.Remember() {
			// The saved sign-in is still good: the panel offers it, one Enter away.
			a.ui.Login.Remembered = true
			a.ui.Login.Focus = views.LoginFocusSubmit
		}
	}
	a.anim.stageStart = nowTime()
	a.markDirty()
}

// clearAccountDataLocked discards cached state owned by the previous account.
func (a *App) clearAccountDataLocked() {
	a.st.Conversations = map[string]*client.Conversation{}
	a.st.Order = nil
	a.st.ActiveID = ""
	a.st.Users = make(map[string]protocol.User)
	a.st.MutedUntil, a.st.MutedForever = 0, false
	a.pendingSettings = nil
	a.ui.Moments = momentsState{}
	a.st.Input, a.st.InputCursor = nil, 0
	a.st.InputErr = ""
	a.st.PendingNewMessage = false
	a.st.Filter = nil
	a.st.Focus = client.FocusList
	a.ui.Page = client.PageWelcome
	a.ui.pending = nil
	a.ui.Selected = 0
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

// unreachable is the error for a server that could not be connected to. It names
// the address, which the panel no longer shows anywhere else, so a typo in the
// configuration is distinguishable from a server that is not running
// (spec tui-login-stage / 连不上服务器时显示地址).
func (a *App) unreachable(server string) string {
	return fmt.Sprintf(i18n.ServerDownAt.Get(a.lang), server)
}

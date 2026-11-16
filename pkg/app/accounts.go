package app

import (
	"context"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/client"
	"link-chat/pkg/tui/views"
)

// The account list (spec tui-account-switch / 账号列表面板、从账号列表登录).
//
// It lists the accounts signed in on this instance, most recent first, and "use
// another account". Choosing an account with a saved sign-in signs straight in —
// the stage's entrance plays as for any sign-in — and choosing one without goes
// to the sign-in panel with its name filled in.

// accountEntries is the list as the panel shows it.
func (a *App) accountEntries() []views.AccountEntry {
	out := make([]views.AccountEntry, 0, len(a.cfg.Accounts))
	for _, name := range a.cfg.Accounts {
		out = append(out, views.AccountEntry{
			Name:       name,
			Remembered: a.cfg.Remember() && a.creds.forUser(name).has(),
		})
	}
	return out
}

// openAccounts puts the account list on the stage with the cursor on current,
// the account just left, when it is listed.
func (a *App) openAccounts(current string) {
	a.ui.Accounts = views.AccountsModel{}
	for i, name := range a.cfg.Accounts {
		if name == current {
			a.ui.Accounts.Cursor = i
		}
	}
	a.anim.buttonStart = nowTime()
	a.ui.Dialog = views.DialogAccounts
}

// accountsSweeping reports whether the focused account button's band is running.
func (a *App) accountsSweeping() bool { return a.ui.Dialog == views.DialogAccounts }

// handleAccountsKey drives the account list.
func (a *App) handleAccountsKey(ctx context.Context, ev *tcell.EventKey) {
	n := len(a.cfg.Accounts) + 1
	m := &a.ui.Accounts
	move := func(d int) {
		// The list wraps: past the last item is the first, and back.
		m.Cursor = ((m.Cursor+d)%n + n) % n
		// The band enters the newly focused button from its left edge.
		a.anim.buttonStart = nowTime()
	}
	switch ev.Key() {
	case tcell.KeyUp, tcell.KeyBacktab:
		move(-1)
	case tcell.KeyDown, tcell.KeyTab:
		move(1)
	case tcell.KeyEnter:
		a.chooseAccount(ctx)
	case tcell.KeyEscape:
		// Back to the sign-in panel for the account the cursor started on, as
		// after signing out.
		name := ""
		if m.Cursor < len(a.cfg.Accounts) {
			name = a.cfg.Accounts[m.Cursor]
		}
		a.openLoginFor(name)
	}
	switch ev.Rune() {
	case 'k':
		move(-1)
	case 'j':
		move(1)
	}
}

// chooseAccount acts on the account under the cursor.
func (a *App) chooseAccount(ctx context.Context) {
	m := a.ui.Accounts
	if m.Cursor >= len(a.cfg.Accounts) {
		// "Use another account": a blank sign-in panel.
		a.ui.Login = views.LoginModel{ServerShown: a.cfg.Server, Focus: views.LoginFocusUsername}
		a.ui.Dialog = views.DialogLogin
		return
	}
	name := a.cfg.Accounts[m.Cursor]
	a.openLoginFor(name)
	if a.ui.Login.Remembered {
		// A saved sign-in: sign in with it now, exactly as pressing Enter on the
		// remembered panel would. The panel stays on the stage while it runs, so
		// a failure lands there with its reason, and success plays the entrance.
		a.submitLogin(ctx)
	}
}

// openLoginFor opens the sign-in panel for an account: remembered with focus on
// the button when a sign-in is saved for it, otherwise with its name filled in
// and the caret in the password row. The app's notion of the current account
// moves to it, so the saved token used is that account's.
func (a *App) openLoginFor(name string) {
	a.mu.Lock()
	a.st.Username = name
	a.st.UserID = ""
	a.st.Token = ""
	if name != "" && a.cfg.Remember() {
		if tok, err := a.creds.forUser(name).load(); err == nil {
			a.st.Token = tok
		}
	}
	a.mu.Unlock()
	a.ui.Login = views.LoginModel{Username: name, ServerShown: a.cfg.Server}
	a.openLogin()
	if a.st.Token != "" {
		a.ui.Login.Remembered = true
		a.ui.Login.Focus = views.LoginFocusSubmit
	}
	a.anim.buttonStart = nowTime()
}

var _ = client.FocusList

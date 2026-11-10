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

// openRegister switches from the sign-in panel to the registration form,
// carrying the username over so it does not have to be retyped.
func (a *App) openRegister() {
	a.ui.Register = views.RegisterModel{
		Username: strings.TrimSpace(a.ui.Login.Username),
		Field:    1,
	}
	if a.ui.Register.Username != "" {
		a.ui.Register.Field = 1
	} else {
		a.ui.Register.Field = 0
	}
	a.ui.Dialog = views.DialogRegister
	a.markDirty()
}

// openRecover starts the recovery wizard at step one.
func (a *App) openRecover() {
	a.ui.Recover = views.RecoverModel{
		Step:     views.RecoverStepUsername,
		Username: strings.TrimSpace(a.ui.Login.Username),
	}
	a.ui.Dialog = views.DialogRecover
	a.markDirty()
}

// handleRegisterKey edits the registration form.
func (a *App) handleRegisterKey(ctx context.Context, ev *tcell.EventKey) {
	m := &a.ui.Register

	// The preset question list captures the keyboard while it is open, which is
	// what makes ↑↓/Enter feel like a real picker rather than a text field.
	if m.Picking {
		switch ev.Key() {
		case tcell.KeyEscape:
			m.Picking = false
		case tcell.KeyUp:
			if m.PickIdx > 0 {
				m.PickIdx--
			}
		case tcell.KeyDown:
			if m.PickIdx < len(i18n.SecurityQuestions)-1 {
				m.PickIdx++
			}
		case tcell.KeyEnter:
			m.Question = i18n.SecurityQuestions[m.PickIdx].Get(a.lang)
			m.Picking = false
			m.Field = 3
		}
		return
	}

	switch ev.Key() {
	case tcell.KeyEscape:
		a.ui.Dialog = views.DialogLogin
		a.ui.Login.Username = m.Username
		a.ui.Login.Focus = views.LoginFocusPassword
		return
	case tcell.KeyTab, tcell.KeyDown:
		m.Field = (m.Field + 1) % 4
		return
	case tcell.KeyBacktab, tcell.KeyUp:
		m.Field = (m.Field + 3) % 4
		return
	case tcell.KeyEnter:
		// Enter on the question field opens the preset list; on any other field
		// it submits.
		if m.Field == 2 {
			m.Picking = true
			m.PickIdx = 0
			return
		}
		a.submitRegister(ctx)
		return
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		a.registerBackspace()
		return
	case tcell.KeyRune:
		r := ev.Rune()
		if r == 0 {
			return
		}
		switch m.Field {
		case 0:
			m.Username += string(r)
		case 1:
			m.Password += string(r)
		case 2:
			m.Question += string(r)
		case 3:
			m.Answer += string(r)
		}
		m.Err = ""
	}
}

func (a *App) registerBackspace() {
	m := &a.ui.Register
	trim := func(s string) string {
		if s == "" {
			return s
		}
		r := []rune(s)
		return string(r[:len(r)-1])
	}
	switch m.Field {
	case 0:
		m.Username = trim(m.Username)
	case 1:
		m.Password = trim(m.Password)
	case 2:
		m.Question = trim(m.Question)
	case 3:
		m.Answer = trim(m.Answer)
	}
}

// submitRegister validates locally, then registers.
func (a *App) submitRegister(ctx context.Context) {
	m := &a.ui.Register
	user := strings.TrimSpace(m.Username)
	question := strings.TrimSpace(m.Question)
	answer := strings.TrimSpace(m.Answer)

	// Local validation first: a round trip just to be told a field is empty is
	// wasted latency, and it must not spend a rate-limit token.
	switch {
	case user == "":
		m.Err = i18n.RegRequired.Get(a.lang)
		m.Field = 0
		a.markDirty()
		return
	case m.Password == "":
		m.Err = i18n.RegRequired.Get(a.lang)
		m.Field = 1
		a.markDirty()
		return
	case question == "":
		m.Err = i18n.RegRequired.Get(a.lang)
		m.Field = 2
		a.markDirty()
		return
	case answer == "":
		m.Err = i18n.RegRequired.Get(a.lang)
		m.Field = 3
		a.markDirty()
		return
	}

	m.Busy = true
	m.Err = ""
	a.markDirty()

	pass := m.Password
	go func() {
		server := normaliseServerURL(a.cfg.Server)
		if err := a.ensureConnected(ctx, server); err != nil {
			a.failRegister(i18n.ServerDown.Get(a.lang))
			return
		}
		f, err := a.tr.Call(ctx, protocol.OpAuthRegister, protocol.RegisterReq{
			Username:         user,
			Password:         pass,
			SecurityQuestion: question,
			SecurityAnswer:   answer,
		}, 20*time.Second)
		if err != nil {
			a.failRegister(i18n.ServerDown.Get(a.lang))
			return
		}
		if f.Type == protocol.TypeError {
			code := errorCode(f)
			a.mu.Lock()
			a.ui.Register.Busy = false
			a.ui.Register.Err = i18n.ErrText(code, "").Get(a.lang)
			switch code {
			case protocol.CodeUsernameTaken, protocol.CodeInvalidUsername:
				a.ui.Register.Field = 0
			case protocol.CodeWeakPassword:
				a.ui.Register.Field = 1
			case protocol.CodeSecurityQuestionReq:
				a.ui.Register.Field = 2
			}
			a.mu.Unlock()
			a.markDirty()
			return
		}

		var res protocol.AuthRes
		if err := decodeData(f, &res); err != nil {
			a.failRegister(i18n.LoginFailed.Get(a.lang))
			return
		}
		a.mu.Lock()
		a.st.Token = res.Token
		a.st.Username = res.User.Username
		if a.st.Username == "" {
			a.st.Username = user
		}
		a.st.UserID = res.User.ID
		a.persistTokenLocked(res.Token)
		a.cfg.Username = a.st.Username
		a.cfg.UserID = res.User.ID
		a.ui.Register.Busy = false
		a.ui.Login.Username = a.st.Username
		a.mu.Unlock()
		_ = SaveConfig(a.cfgPath, a.cfg)
		a.onAuthenticated(res)
	}()
}

func (a *App) failRegister(msg string) {
	a.mu.Lock()
	a.ui.Register.Busy = false
	a.ui.Register.Err = msg
	a.mu.Unlock()
	a.markDirty()
}

// ensureConnected makes sure a live connection exists, dialing if needed.
//
// The state check uses the named constant, not a number: reading StateReady as
// "2" silently treats an authenticating connection as ready and skips the dial.
func (a *App) ensureConnected(ctx context.Context, server string) error {
	if a.tr != nil && (a.st.State == client.StateReady || a.st.State == client.StateAuthenticating) {
		return nil
	}
	if a.tr != nil {
		a.tr.Close()
		a.tr = nil
	}
	a.cfg.Server = server
	a.st.ServerURL = server
	a.connectForLogin(ctx)
	if a.tr == nil {
		return errNoTransport
	}
	if !a.waitConnected(10 * time.Second) {
		return errNoTransport
	}
	return nil
}

var errNoTransport = errorString("server unreachable")

type errorString string

func (e errorString) Error() string { return string(e) }

// handleRecoverKey drives the three-step wizard.
func (a *App) handleRecoverKey(ctx context.Context, ev *tcell.EventKey) {
	m := &a.ui.Recover

	switch ev.Key() {
	case tcell.KeyEscape:
		// Back one step; from the first step it returns to sign-in
		// (spec tui-chat-client / 任一步可返回).
		switch m.Step {
		case views.RecoverStepUsername:
			a.ui.Dialog = views.DialogLogin
		case views.RecoverStepAnswer:
			m.Step = views.RecoverStepUsername
			m.Answer = ""
			m.Err = ""
		case views.RecoverStepPassword:
			m.Step = views.RecoverStepAnswer
			m.NewPass = ""
			m.Confirm = ""
			m.Err = ""
		}
		a.markDirty()
		return
	case tcell.KeyTab, tcell.KeyDown:
		if n := m.FieldCount(); n > 0 {
			m.Field = (m.Field + 1) % n
		}
		return
	case tcell.KeyBacktab, tcell.KeyUp:
		if n := m.FieldCount(); n > 0 {
			m.Field = (m.Field + n - 1) % n
		}
		return
	case tcell.KeyEnter:
		a.submitRecoverStep(ctx)
		return
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		a.recoverBackspace()
		return
	case tcell.KeyRune:
		r := ev.Rune()
		if r == 0 {
			return
		}
		switch m.Step {
		case views.RecoverStepUsername:
			m.Username += string(r)
		case views.RecoverStepAnswer:
			m.Answer += string(r)
		case views.RecoverStepPassword:
			if m.Field == 0 {
				m.NewPass += string(r)
			} else {
				m.Confirm += string(r)
			}
		}
		m.Err = ""
	}
}

func (a *App) recoverBackspace() {
	m := &a.ui.Recover
	trim := func(s string) string {
		if s == "" {
			return s
		}
		r := []rune(s)
		return string(r[:len(r)-1])
	}
	switch m.Step {
	case views.RecoverStepUsername:
		m.Username = trim(m.Username)
	case views.RecoverStepAnswer:
		m.Answer = trim(m.Answer)
	case views.RecoverStepPassword:
		if m.Field == 0 {
			m.NewPass = trim(m.NewPass)
		} else {
			m.Confirm = trim(m.Confirm)
		}
	}
}

// submitRecoverStep advances the wizard from whichever step it is on.
func (a *App) submitRecoverStep(ctx context.Context) {
	m := &a.ui.Recover
	switch m.Step {
	case views.RecoverStepUsername:
		a.recoverAskQuestion(ctx)
	case views.RecoverStepAnswer:
		a.recoverVerify(ctx)
	case views.RecoverStepPassword:
		a.recoverReset(ctx)
	}
}

func (a *App) recoverAskQuestion(ctx context.Context) {
	m := &a.ui.Recover
	user := strings.TrimSpace(m.Username)
	if user == "" {
		m.Err = i18n.LoginUsername.Get(a.lang)
		a.markDirty()
		return
	}
	m.Busy = true
	m.Err = ""
	a.markDirty()

	go func() {
		if err := a.ensureConnected(ctx, normaliseServerURL(a.cfg.Server)); err != nil {
			a.failRecover(i18n.ServerDown.Get(a.lang))
			return
		}
		f, err := a.tr.Call(ctx, protocol.OpRecoverQuestion, protocol.RecoverQuestionReq{Username: user}, 20*time.Second)
		if err != nil {
			a.failRecover(i18n.ServerDown.Get(a.lang))
			return
		}
		if f.Type == protocol.TypeError {
			code := errorCode(f)
			a.mu.Lock()
			a.ui.Recover.Busy = false
			// "Cannot recover" keeps the user on step one with an explanation,
			// because retyping the username is the only fix available
			// (spec tui-chat-client / 用户名不可找回).
			a.ui.Recover.Err = i18n.RecoverCannot.Get(a.lang)
			if code == protocol.CodeRateLimited {
				a.ui.Recover.Err = i18n.ErrText(code, "").Get(a.lang)
			}
			a.mu.Unlock()
			a.markDirty()
			return
		}
		var res protocol.RecoverQuestionRes
		if err := decodeData(f, &res); err != nil {
			a.failRecover(i18n.LoginFailed.Get(a.lang))
			return
		}
		a.mu.Lock()
		a.ui.Recover.Busy = false
		a.ui.Recover.Question = res.Question
		a.ui.Recover.Step = views.RecoverStepAnswer
		a.ui.Recover.Field = 0
		a.ui.Recover.Answer = ""
		a.mu.Unlock()
		a.markDirty()
	}()
}

func (a *App) recoverVerify(ctx context.Context) {
	m := &a.ui.Recover
	if strings.TrimSpace(m.Answer) == "" {
		m.Err = i18n.RegRequired.Get(a.lang)
		a.markDirty()
		return
	}
	user, answer := strings.TrimSpace(m.Username), m.Answer
	m.Busy = true
	m.Err = ""
	a.markDirty()

	go func() {
		f, err := a.tr.Call(ctx, protocol.OpRecoverVerify, protocol.RecoverVerifyReq{
			Username: user, Answer: answer,
		}, 20*time.Second)
		if err != nil {
			a.failRecover(i18n.ServerDown.Get(a.lang))
			return
		}
		if f.Type == protocol.TypeError {
			code := errorCode(f)
			a.mu.Lock()
			a.ui.Recover.Busy = false
			switch code {
			case protocol.CodeSecurityAnswerWrong:
				a.ui.Recover.Err = i18n.ErrText(code, "").Get(a.lang)
				a.ui.Recover.Answer = ""
			case protocol.CodeCannotRecover:
				a.ui.Recover.Err = i18n.RecoverCannot.Get(a.lang)
				a.ui.Recover.Step = views.RecoverStepUsername
			default:
				a.ui.Recover.Err = i18n.ErrText(code, "").Get(a.lang)
			}
			a.mu.Unlock()
			a.markDirty()
			return
		}
		var res protocol.RecoverVerifyRes
		if err := decodeData(f, &res); err != nil {
			a.failRecover(i18n.LoginFailed.Get(a.lang))
			return
		}
		a.mu.Lock()
		a.ui.Recover.Busy = false
		a.ui.Recover.ResetToken = res.ResetToken
		a.ui.Recover.Step = views.RecoverStepPassword
		a.ui.Recover.Field = 0
		a.mu.Unlock()
		a.markDirty()
	}()
}

func (a *App) recoverReset(ctx context.Context) {
	m := &a.ui.Recover
	// Mismatched confirmation is caught locally; sending it would only waste a
	// rate-limit token and a round trip
	// (spec tui-chat-client / 两次输入不一致).
	if m.NewPass != m.Confirm {
		m.Err = i18n.RecoverMismatch.Get(a.lang)
		m.Field = 1
		a.markDirty()
		return
	}
	if msg := m.NewPass; msg == "" {
		m.Err = i18n.RegRequired.Get(a.lang)
		m.Field = 0
		a.markDirty()
		return
	}
	token := m.ResetToken
	newPass := m.NewPass
	m.Busy = true
	m.Err = ""
	a.markDirty()

	go func() {
		f, err := a.tr.Call(ctx, protocol.OpRecoverReset, protocol.RecoverResetReq{
			ResetToken: token, NewPassword: newPass,
		}, 20*time.Second)
		if err != nil {
			a.failRecover(i18n.ServerDown.Get(a.lang))
			return
		}
		if f.Type == protocol.TypeError {
			code := errorCode(f)
			a.mu.Lock()
			a.ui.Recover.Busy = false
			switch code {
			case protocol.CodeResetTokenExpired, protocol.CodeResetTokenInvalid:
				// Start over: the token cannot be revived
				// (spec tui-chat-client / 重置令牌过期).
				a.ui.Recover = views.RecoverModel{
					Step:     views.RecoverStepUsername,
					Username: a.ui.Recover.Username,
					Err:      i18n.RecoverExpired.Get(a.lang),
				}
			case protocol.CodeWeakPassword:
				a.ui.Recover.Err = i18n.ErrText(code, "").Get(a.lang)
				a.ui.Recover.Field = 0
				a.ui.Recover.NewPass = ""
				a.ui.Recover.Confirm = ""
			default:
				a.ui.Recover.Err = i18n.ErrText(code, "").Get(a.lang)
			}
			a.mu.Unlock()
			a.markDirty()
			return
		}

		a.mu.Lock()
		user := a.ui.Recover.Username
		a.ui.Recover = views.RecoverModel{Step: views.RecoverStepDone}
		a.ui.Login = views.LoginModel{
			Username:    user,
			Focus:       views.LoginFocusPassword,
			ServerShown: a.cfg.Server,
			Err:         i18n.RecoverDone.Get(a.lang),
		}
		a.ui.Dialog = views.DialogLogin
		a.mu.Unlock()
		a.markDirty()
	}()
}

func (a *App) failRecover(msg string) {
	a.mu.Lock()
	a.ui.Recover.Busy = false
	a.ui.Recover.Err = msg
	a.mu.Unlock()
	a.markDirty()
}

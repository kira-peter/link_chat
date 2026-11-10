package app

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/client"
	"link-chat/pkg/tui/views"
)

// newLoginApp builds an app showing the login panel, with a fake credential store
// holding token (empty for none). The server address is unreachable on purpose:
// these tests are about what the panel does with keys and state, and any attempt
// to actually dial would show up as Busy or an error.
func newLoginApp(t *testing.T, username, token string, remember bool) (*App, *inMemoryBackend) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "login.json")
	// TestMain gives every config path its own in-memory store. Fetching it here,
	// before New(), lets the token be in place when the constructor reads it.
	b := credentials(path).(*inMemoryBackend)
	if token != "" {
		_ = b.set("default", token)
	}
	cfg := Config{
		Server: "ws://127.0.0.1:1/ws", Theme: "ink", Language: "zh",
		Instance: "default", Username: username,
	}
	cfg.SetRemember(remember)
	return New(newTestScreen(t, 100, 30), cfg, path), b
}

// ---------- 4.1 startup ----------

// With a saved session the panel opens remembered, on the button, name filled in,
// and nothing has been sent to the server.
func TestStartupOffersSavedSession(t *testing.T) {
	a, _ := newLoginApp(t, "frank", "saved-token", true)

	m := a.ui.Login
	if a.ui.Dialog != views.DialogLogin {
		t.Fatalf("startup dialog = %v, want the login panel", a.ui.Dialog)
	}
	if !m.Remembered || m.Focus != views.LoginFocusSubmit || m.Username != "frank" {
		t.Fatalf("panel not offering the saved session: %+v", m)
	}
	if a.tr != nil {
		t.Fatal("a transport exists before the user pressed anything")
	}
	if a.st.State == client.StateReady {
		t.Fatal("signed in before the user pressed anything")
	}
}

// Without a saved session the panel is an ordinary form; the cursor goes to the
// first thing still missing.
func TestStartupWithoutSavedSessionIsOrdinaryForm(t *testing.T) {
	a, _ := newLoginApp(t, "", "", true)
	if a.ui.Dialog != views.DialogLogin || a.ui.Login.Remembered {
		t.Fatalf("fresh install should show a plain login form: %+v", a.ui.Login)
	}
	if a.ui.Login.Focus != views.LoginFocusUsername {
		t.Fatalf("focus = %v, want the username", a.ui.Login.Focus)
	}

	b, _ := newLoginApp(t, "frank", "", true)
	if b.ui.Login.Remembered || b.ui.Login.Focus != views.LoginFocusPassword {
		t.Fatalf("known name, no token: want password focus, got %+v", b.ui.Login)
	}
}

// Run() must not authenticate by itself. The transport stays nil because dialling
// only happens from submitLogin.
func TestRunDoesNotAuthenticate(t *testing.T) {
	a, _ := newLoginApp(t, "frank", "saved-token", true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.Run(ctx)
	time.Sleep(700 * time.Millisecond)

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.tr != nil || a.st.State == client.StateReady || a.ui.Login.Busy {
		t.Fatalf("Run authenticated unprompted: tr=%v state=%v busy=%v", a.tr != nil, a.st.State, a.ui.Login.Busy)
	}
	if !a.ui.Login.Remembered {
		t.Fatal("saved session was dropped without the user acting")
	}
}

// ---------- 4.3 giving up the saved session ----------

// Each way of saying "not that session" clears Remembered, and none of them throws
// the stored token away: only the panel stops offering it.
func TestAbandoningSavedSession(t *testing.T) {
	cases := []struct {
		name string
		do   func(a *App, ctx context.Context)
	}{
		{"type in username", func(a *App, ctx context.Context) {
			a.ui.Login.Focus = views.LoginFocusUsername
			a.handleKey(ctx, key('x'))
		}},
		{"backspace in username", func(a *App, ctx context.Context) {
			a.ui.Login.Focus = views.LoginFocusUsername
			a.handleKey(ctx, specialKey(tcell.KeyBackspace2))
		}},
		{"type in password", func(a *App, ctx context.Context) {
			a.ui.Login.Focus = views.LoginFocusPassword
			a.handleKey(ctx, key('p'))
		}},
		{"backspace in password", func(a *App, ctx context.Context) {
			a.ui.Login.Focus = views.LoginFocusPassword
			a.handleKey(ctx, specialKey(tcell.KeyBackspace2))
		}},
	}

	for _, c := range cases {
		a, b := newLoginApp(t, "frank", "saved-token", true)
		ctx := context.Background()
		if !a.ui.Login.Remembered {
			t.Fatalf("%s: precondition: not remembered", c.name)
		}
		c.do(a, ctx)
		if a.ui.Login.Remembered {
			t.Fatalf("%s: still remembered after the user changed course", c.name)
		}
		if got := b.values["default"]; got != "saved-token" {
			t.Fatalf("%s: token discarded just by typing: %q", c.name, got)
		}
	}
}

// Moving focus around without typing keeps the saved session on offer.
func TestNavigatingDoesNotAbandonSavedSession(t *testing.T) {
	a, _ := newLoginApp(t, "frank", "saved-token", true)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		a.handleKey(ctx, specialKey(tcell.KeyTab))
	}
	if !a.ui.Login.Remembered {
		t.Fatal("tabbing around dropped the saved session")
	}
}

// ---------- 4.2 which path a submit takes ----------

// A remembered submit needs no password and does not complain about its absence;
// an ordinary one insists on it.
func TestSubmitChoosesPathByRemembered(t *testing.T) {
	ctx := context.Background()

	// Remembered, empty password: accepted, goes busy.
	a, _ := newLoginApp(t, "frank", "saved-token", true)
	a.submitLogin(ctx)
	if a.ui.Login.Err != "" {
		t.Fatalf("remembered submit rejected: %q", a.ui.Login.Err)
	}
	if !a.ui.Login.Busy {
		t.Fatal("remembered submit did not start signing in")
	}

	// Not remembered, empty password: refused without dialling.
	b, _ := newLoginApp(t, "frank", "", true)
	b.submitLogin(ctx)
	if b.ui.Login.Err == "" || b.ui.Login.Busy {
		t.Fatalf("password login with no password was not refused: %+v", b.ui.Login)
	}
	if b.ui.Login.Focus != views.LoginFocusPassword {
		t.Fatalf("focus = %v, want the password row", b.ui.Login.Focus)
	}
}

// ---------- 4.4 fallback ----------

// A network failure keeps the saved session; a dead token drops it. This pins the
// two different outcomes at the helper level.
func TestFailureKindsTreatTokenDifferently(t *testing.T) {
	a, b := newLoginApp(t, "frank", "saved-token", true)
	a.ui.Login.Busy = true
	a.failLoginKeepRemembered("server down")
	if !a.ui.Login.Remembered || a.st.Token != "saved-token" || b.values["default"] != "saved-token" {
		t.Fatalf("network failure discarded the saved session: %+v", a.ui.Login)
	}
	if a.ui.Login.Busy || a.ui.Login.Err == "" {
		t.Fatalf("failure not reported: %+v", a.ui.Login)
	}

	a.fallBackToPassword("TOKEN_EXPIRED")
	if a.ui.Login.Remembered || a.st.Token != "" {
		t.Fatalf("dead token kept: %+v", a.ui.Login)
	}
	if _, ok := b.values["default"]; ok {
		t.Fatal("dead token still in the credential store")
	}
	if a.ui.Login.Focus != views.LoginFocusPassword || a.ui.Login.Err == "" {
		t.Fatalf("fallback did not move to the password with a reason: %+v", a.ui.Login)
	}
}

// ---------- 4.5 remember off ----------

func TestRememberOffShowsOrdinaryPasswordInput(t *testing.T) {
	// A token is sitting in the store, but the user turned remembering off.
	a, _ := newLoginApp(t, "frank", "stale-token", false)
	if a.ui.Login.Remembered {
		t.Fatal("remembered state offered with remembering switched off")
	}
	if a.st.Token != "" {
		t.Fatal("token loaded despite remembering being off")
	}
	if a.ui.Login.Focus != views.LoginFocusPassword {
		t.Fatalf("focus = %v, want the password row", a.ui.Login.Focus)
	}
}

// ---------- 3.5 frame budget ----------

func TestNeedsFramesForLoginButton(t *testing.T) {
	a, _ := newLoginApp(t, "frank", "saved-token", true)
	now := freezeClock(t, time.Unix(7000, 0))
	// Start from a settled marker so only the button is under test.
	a.anim.slide.MoveTo(a.regionX(a.st.Focus), *now, focusSlideDuration, false)

	cases := []struct {
		name  string
		setup func()
		want  bool
	}{
		{"panel open, button focused, animation on", func() {}, true},
		{"focus on a field stops it", func() { a.ui.Login.Focus = views.LoginFocusUsername }, false},
		{"focus on a link stops it", func() { a.ui.Login.Focus = views.LoginFocusForgot }, false},
		{"animation off stops it", func() { a.st.MotionOff = true }, false},
		{"busy stops it", func() { a.ui.Login.Busy = true }, false},
		{"another dialog stops it", func() { a.ui.Dialog = views.DialogHelp }, false},
	}
	for _, c := range cases {
		a.ui.Dialog = views.DialogLogin
		a.ui.Login.Focus = views.LoginFocusSubmit
		a.ui.Login.Busy = false
		a.st.MotionOff = false
		a.st.State = client.StateDisconnected
		a.st.Focus = client.FocusList // keep the composer shimmer out of it

		c.setup()
		if got := a.needsFrames(*now); got != c.want {
			t.Errorf("%s: needsFrames = %v, want %v", c.name, got, c.want)
		}
	}
}

// ---------- 3.6 left / right between the two links ----------

// pressLoginKey sends one key through the real key handler.
func pressLoginKey(a *App, k tcell.Key) {
	a.handleKey(context.Background(), specialKey(k))
}

// The links sit side by side, so ← and → move between them.
func TestLoginLinksMoveWithLeftRight(t *testing.T) {
	a, _ := newLoginApp(t, "frank", "", true)

	a.ui.Login.Focus = views.LoginFocusForgot
	pressLoginKey(a, tcell.KeyRight)
	if a.ui.Login.Focus != views.LoginFocusRegister {
		t.Fatalf("→ from the left link went to %v, want the right link", a.ui.Login.Focus)
	}
	pressLoginKey(a, tcell.KeyLeft)
	if a.ui.Login.Focus != views.LoginFocusForgot {
		t.Fatalf("← from the right link went to %v, want the left link", a.ui.Login.Focus)
	}
}

// At either end of the row the key does nothing: no wrap to the other link, and
// no leaving the row.
func TestLoginLinkRowEdgesAreNoOps(t *testing.T) {
	a, _ := newLoginApp(t, "frank", "", true)

	a.ui.Login.Focus = views.LoginFocusForgot
	pressLoginKey(a, tcell.KeyLeft)
	if a.ui.Login.Focus != views.LoginFocusForgot {
		t.Fatalf("← at the left edge moved focus to %v", a.ui.Login.Focus)
	}

	a.ui.Login.Focus = views.LoginFocusRegister
	pressLoginKey(a, tcell.KeyRight)
	if a.ui.Login.Focus != views.LoginFocusRegister {
		t.Fatalf("→ at the right edge moved focus to %v", a.ui.Login.Focus)
	}
}

// Off the link row, ← and → leave the focus alone: they must not jump into the
// links from a field or the button, and a field has no caret to move.
func TestLeftRightDoNothingOffTheLinkRow(t *testing.T) {
	for _, f := range []views.LoginFocus{views.LoginFocusUsername, views.LoginFocusPassword, views.LoginFocusSubmit} {
		for _, k := range []tcell.Key{tcell.KeyLeft, tcell.KeyRight} {
			a, _ := newLoginApp(t, "frank", "", true)
			a.ui.Login.Focus = f
			a.ui.Login.Username, a.ui.Login.Password = "frank", "secret"
			pressLoginKey(a, k)
			if a.ui.Login.Focus != f {
				t.Errorf("key %v moved focus from %v to %v", k, f, a.ui.Login.Focus)
			}
			if a.ui.Login.Username != "frank" || a.ui.Login.Password != "secret" {
				t.Errorf("key %v changed the field text on %v", k, f)
			}
		}
	}
}

// Up, down and Tab behave exactly as before, links included.
func TestUpDownAndTabStillMoveFromTheLinks(t *testing.T) {
	cases := []struct {
		name string
		from views.LoginFocus
		key  tcell.Key
		want views.LoginFocus
	}{
		{"up from the left link", views.LoginFocusForgot, tcell.KeyUp, views.LoginFocusSubmit},
		{"down from the left link", views.LoginFocusForgot, tcell.KeyDown, views.LoginFocusRegister},
		{"up from the right link", views.LoginFocusRegister, tcell.KeyUp, views.LoginFocusForgot},
		{"down from the right link wraps", views.LoginFocusRegister, tcell.KeyDown, views.LoginFocusUsername},
		{"tab from the left link", views.LoginFocusForgot, tcell.KeyTab, views.LoginFocusRegister},
		{"backtab from the left link", views.LoginFocusForgot, tcell.KeyBacktab, views.LoginFocusSubmit},
	}
	for _, c := range cases {
		a, _ := newLoginApp(t, "frank", "", true)
		a.ui.Login.Focus = c.from
		pressLoginKey(a, c.key)
		if a.ui.Login.Focus != c.want {
			t.Errorf("%s: focus = %v, want %v", c.name, a.ui.Login.Focus, c.want)
		}
	}
}

// The Tab ring still visits all five elements and returns: left/right is an extra
// route, not a replacement.
func TestTabRingIsUnchanged(t *testing.T) {
	a, _ := newLoginApp(t, "frank", "", true)
	a.ui.Login.Focus = views.LoginFocusUsername
	seen := map[views.LoginFocus]bool{}
	for i := 0; i < 5; i++ {
		seen[a.ui.Login.Focus] = true
		pressLoginKey(a, tcell.KeyTab)
	}
	if len(seen) != 5 || a.ui.Login.Focus != views.LoginFocusUsername {
		t.Fatalf("tab ring visited %d of 5 and ended on %v", len(seen), a.ui.Login.Focus)
	}
}

// ---------- 3.4 typing ends the remembered state ----------

// Typing into the password turns the placeholder dots into an ordinary field: the
// remembered state is gone and the typed text is what the field holds.
func TestTypingEndsTheRememberedPlaceholder(t *testing.T) {
	a, _ := newLoginApp(t, "frank", "saved-token", true)
	if !a.ui.Login.Remembered {
		t.Fatal("precondition: should open remembered")
	}
	a.ui.Login.Focus = views.LoginFocusPassword
	a.handleKey(context.Background(), key('p'))

	if a.ui.Login.Remembered {
		t.Fatal("still remembered after typing a password")
	}
	if a.ui.Login.Password != "p" {
		t.Fatalf("password = %q, want the typed character", a.ui.Login.Password)
	}
}

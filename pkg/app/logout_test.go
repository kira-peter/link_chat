package app

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/client"
	"link-chat/pkg/i18n"
	"link-chat/pkg/protocol"
	"link-chat/pkg/tui/anim"
	"link-chat/pkg/tui/views"
	"link-chat/pkg/tui/widget"
)

// signedInApp is an app on the main interface, signed in, with two conversations
// and the settings dialog open on "sign out" — about to confirm.
func signedInApp(t *testing.T) (*App, *time.Time) {
	t.Helper()
	a, now := signInFromStage(t)
	a.skipEntrance()
	a.st.Token = "tok"
	a.render()
	return a, now
}

// ---------- the confirmation ----------

func TestLogoutConfirmDefaultsToCancelAndMovesWithArrows(t *testing.T) {
	a, _ := signedInApp(t)
	a.openLogoutConfirm()
	if a.ui.Logout.Focus != views.LogoutCancel {
		t.Fatal("the confirmation does not start on cancel")
	}
	press := func(k tcell.Key) { a.handleKey(t.Context(), specialKey(k)) }

	press(tcell.KeyLeft) // already leftmost: stays
	if a.ui.Logout.Focus != views.LogoutCancel {
		t.Fatal("← past the left edge moved the focus")
	}
	press(tcell.KeyRight)
	if a.ui.Logout.Focus != views.LogoutConfirm {
		t.Fatal("→ did not move to sign out")
	}
	press(tcell.KeyRight) // rightmost: stays
	if a.ui.Logout.Focus != views.LogoutConfirm {
		t.Fatal("→ past the right edge moved the focus")
	}
	press(tcell.KeyTab)
	if a.ui.Logout.Focus != views.LogoutCancel {
		t.Fatal("Tab did not cycle back to cancel")
	}

	// Enter on cancel: still signed in, back on the settings page.
	press(tcell.KeyEnter)
	if a.ui.Dialog != views.DialogNone || a.st.Token == "" {
		t.Fatalf("Enter on cancel signed out or went elsewhere (dialog %v)", a.ui.Dialog)
	}

	// Esc is cancel.
	a.openLogoutConfirm()
	press(tcell.KeyRight)
	press(tcell.KeyEscape)
	if a.ui.Dialog != views.DialogNone || a.st.Token == "" {
		t.Fatal("Esc on the confirmation signed out")
	}
}

// confirmButtons finds the two buttons on the confirmation: each one's left and
// right frame columns and its frame colour.
type foundButton struct {
	left, right int
	fg          tcell.Color
}

func confirmButtons(t *testing.T, scr tcell.SimulationScreen) (widget.Rect, []foundButton) {
	t.Helper()
	w, h := scr.Size()
	p := views.LogoutPanelRect(w, h)
	y := p.Y + views.ConfirmButtonRow
	var out []foundButton
	for x := p.X + 1; x < p.X+p.W-1; x++ {
		if r, _, _, _ := scr.GetContent(x, y); r == '┌' {
			b := foundButton{left: x, fg: cellFG(scr, x, y)}
			for rx := x + 1; rx < p.X+p.W-1; rx++ {
				if r, _, _, _ := scr.GetContent(rx, y); r == '┐' {
					b.right = rx
					break
				}
			}
			out = append(out, b)
		}
	}
	return p, out
}

func TestLogoutConfirmIsFormalAndSymmetric(t *testing.T) {
	for _, sw := range []bool{false} {
		a, _ := signedInApp(t)
		a.openLogoutConfirm()
		a.render()
		scr := a.scr.(tcell.SimulationScreen)
		flat := strings.ReplaceAll(strings.ReplaceAll(screenText(scr), " ", ""), "\n", "")
		title, msg, ok := i18n.LogoutTitle, i18n.LogoutMessage, i18n.LogoutConfirm
		wants := []string{title.Get(a.lang), i18n.Cancel.Get(a.lang), ok.Get(a.lang)}
		wants = append(wants, strings.Split(msg.Get(a.lang), "\n")...)
		for _, want := range wants {
			if !strings.Contains(flat, strings.ReplaceAll(want, " ", "")) {
				t.Fatalf("switch=%v: the confirmation is missing %q:\n%s", sw, want, screenText(scr))
			}
		}
		if strings.Contains(screenText(scr), i18n.Delete.Get(a.lang)) {
			t.Fatal("the confirmation talks about deleting")
		}
		p, bs := confirmButtons(t, scr)
		if len(bs) != 2 {
			t.Fatalf("switch=%v: want two framed buttons, found %d", sw, len(bs))
		}
		w0, w1 := bs[0].right-bs[0].left, bs[1].right-bs[1].left
		if w0 != w1 {
			t.Errorf("switch=%v: the buttons are %d and %d wide", sw, w0+1, w1+1)
		}
		leftGap := bs[0].left - p.X
		rightGap := (p.X + p.W - 1) - bs[1].right
		if leftGap != rightGap {
			t.Errorf("switch=%v: %d cells left of the buttons, %d right", sw, leftGap, rightGap)
		}
	}
}

// The focused button wears the focus colour; the operation's button, focused,
// wears danger.
func TestLogoutButtonColours(t *testing.T) {
	a, _ := signedInApp(t)
	a.st.MotionOff = true // static colours, no band in the way
	a.openLogoutConfirm()
	a.render()
	_, bs := confirmButtons(t, a.scr.(tcell.SimulationScreen))
	if bs[0].fg != a.tokens.Accent {
		t.Errorf("focused cancel's frame is %v, want the accent %v", bs[0].fg, a.tokens.Accent)
	}
	a.handleKey(t.Context(), specialKey(tcell.KeyRight))
	a.render()
	_, bs = confirmButtons(t, a.scr.(tcell.SimulationScreen))
	if bs[1].fg != a.tokens.Danger {
		t.Errorf("focused sign-out's frame is %v, want danger %v", bs[1].fg, a.tokens.Danger)
	}
	if bs[0].fg == a.tokens.Accent {
		t.Error("the unfocused cancel still has the focus colour")
	}
}

// The band runs only with animation on, at the full frame rate.
func TestLogoutConfirmBandAndFrames(t *testing.T) {
	a, now := signedInApp(t)
	a.st.Focus = client.FocusInput
	a.st.RestoreInput("draft") // nothing else animating
	a.openLogoutConfirm()
	if d, ok := a.frameDelay(*now); !ok || d != frameInterval {
		t.Fatalf("with the confirmation open the frame delay is %v (%v), want %v", d, ok, frameInterval)
	}
	a.st.MotionOff = true
	if a.needsFrames(*now) {
		t.Fatal("with animation off the confirmation still asks for frames")
	}
}

// ---------- signing out ----------

// Signing out never waits for the network: here closing the connection takes two
// seconds, and the screen has moved on long before. The saved sign-in is kept,
// and the panel offers it with the focus on the button.
func TestLogoutDoesNotWaitAndKeepsTheSavedSignIn(t *testing.T) {
	a, _ := signedInApp(t)
	a.cfg.SetRemember(true)
	if err := a.creds.forUser(a.st.Username).save("tok"); err != nil {
		t.Fatal(err)
	}
	a.tr.Store(client.NewTransport("ws://127.0.0.1:1/ws"))
	closed := make(chan struct{}, 1)
	a.closeTransport = func() {
		time.Sleep(2 * time.Second)
		closed <- struct{}{}
	}
	a.openLogoutConfirm()
	a.handleKey(t.Context(), specialKey(tcell.KeyRight))
	start := time.Now()
	a.handleKey(t.Context(), specialKey(tcell.KeyEnter))
	if took := time.Since(start); took > 200*time.Millisecond {
		t.Fatalf("signing out blocked the UI for %v", took)
	}
	if a.ui.Dialog != views.DialogLogin || len(a.st.Conversations) != 0 {
		t.Fatalf("not signed out: dialog %v, %d conversations", a.ui.Dialog, len(a.st.Conversations))
	}
	if tok, err := a.creds.forUser("alice").load(); err != nil || tok != "tok" {
		t.Fatalf("signing out removed the saved sign-in: %q, %v", tok, err)
	}
	if !a.ui.Login.Remembered || a.ui.Login.Focus != views.LoginFocusSubmit || a.ui.Login.Username != "alice" {
		t.Fatalf("the panel after signing out: %+v", a.ui.Login)
	}
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("the connection was never closed")
	}
}

func confirmLogout(t *testing.T, a *App) {
	t.Helper()
	a.openLogoutConfirm()
	a.handleKey(t.Context(), specialKey(tcell.KeyRight))
	a.handleKey(t.Context(), specialKey(tcell.KeyEnter))
}

func TestExitPlaysTheEntranceBackwards(t *testing.T) {
	a, now := signedInApp(t)
	confirmLogout(t, a)
	start := *now
	scr := a.scr.(tcell.SimulationScreen)
	w, h := scr.Size()
	panel := views.LoginPanelRect(w, h)
	topEnd, botEnd := views.ChromeRuleRows(h)

	// 1. Closing: two rules inside the main interface's rules, the old main
	// interface (its list) between them, background outside.
	closing := at(a, now, start, 0.25)
	rows := ruleRows(scr, w/2)
	if len(rows) != 2 || rows[0] <= topEnd || rows[1] >= botEnd {
		t.Fatalf("closing: want two rules strictly inside %d..%d, got %v:\n%s", topEnd, botEnd, rows, closing)
	}
	// The state has already moved to the stage, so the conversation list can only
	// be there if the picture of the old main interface is being shown.
	if !strings.Contains(closing, "planet") {
		t.Fatalf("closing: the old main interface is not between the rules:\n%s", closing)
	}

	// The two close in as time goes on.
	at(a, now, start, 0.40)
	later := ruleRows(scr, w/2)
	if len(later) != 2 || later[1]-later[0] >= rows[1]-rows[0] {
		t.Fatalf("the rules did not close in: %v then %v", rows, later)
	}

	// 2. Shrinking: one rule, narrowing towards the panel's width.
	at(a, now, start, 0.62)
	if one := ruleRows(scr, panel.W+2); len(one) != 1 {
		t.Fatalf("shrinking: want one rule wider than the panel, got %v:\n%s", one, screenText(scr))
	}

	// 3. Opening into the panel: a box shorter than the panel, with the panel's
	// real contents in its middle row band.
	at(a, now, start, 0.90)
	top, bot := -1, -1
	for y := 0; y < h; y++ {
		r, _, _, _ := scr.GetContent(panel.X, y)
		if r == '┌' && top < 0 {
			top = y
		}
		if r == '└' {
			bot = y
		}
	}
	if top < 0 || bot < 0 || bot-top+1 >= panel.H {
		t.Fatalf("opening into the panel: box rows %d..%d, want shorter than %d", top, bot, panel.H)
	}

	// 4. Done: the stage exactly as it is when opened directly.
	final := at(a, now, start, 1.0)
	if a.exitRunning(*now) {
		t.Fatal("the exit is still running at its full duration")
	}
	a.anim.exit = nil
	a.render()
	if direct := screenText(scr); direct != final {
		t.Fatalf("the exit's last frame is not the stage:\n--- exit\n%s\n--- stage\n%s", final, direct)
	}
}

func TestAKeyDuringTheExitGoesToThePanel(t *testing.T) {
	a, now := signedInApp(t)
	confirmLogout(t, a)
	at(a, now, *now, 0.3)
	a.ui.Login.Focus = views.LoginFocusUsername
	before := a.ui.Login.Username
	a.handleKey(t.Context(), key('z'))
	if a.anim.exit != nil {
		t.Fatal("the key did not end the exit")
	}
	if a.ui.Login.Username != before+"z" {
		t.Fatalf("the key did not reach the panel: username %q", a.ui.Login.Username)
	}
}

func TestResizeDuringTheExitEndsIt(t *testing.T) {
	a, now := signedInApp(t)
	confirmLogout(t, a)
	at(a, now, *now, 0.3)
	a.scr.(tcell.SimulationScreen).SetSize(90, 26)
	a.render()
	if a.anim.exit != nil {
		t.Fatal("the exit kept playing after the window changed size")
	}
	if !a.onStage() {
		t.Fatal("after the resize the stage is not showing")
	}
}

func TestNoExitWithAnimationOff(t *testing.T) {
	a, _ := signedInApp(t)
	a.st.MotionOff = true
	confirmLogout(t, a)
	if a.anim.exit != nil {
		t.Fatal("an exit was started with animation off")
	}
	a.render()
	if hasChromeRules(a.scr.(tcell.SimulationScreen)) {
		t.Fatal("with animation off the frame after signing out is not the stage")
	}
}

// The exit's timeline is the entrance's, backwards.
func TestExitTimelineIsTheEntranceReversed(t *testing.T) {
	d := anim.EntranceDuration
	for ms := 0; ms < int(d/time.Millisecond); ms += 7 {
		e := time.Duration(ms) * time.Millisecond
		xp, xv := anim.ExitAt(e)
		ep, ev := anim.EntranceAt(d - e - 1)
		if xp != ep || xv != ev {
			t.Fatalf("at %v the exit is (%v,%.3f), the entrance backwards is (%v,%.3f)", e, xp, xv, ep, ev)
		}
	}
	if p, _ := anim.ExitAt(0); p != anim.PhaseOpen {
		t.Fatalf("the exit starts in %v, want the opening", p)
	}
	if p, _ := anim.ExitAt(d); p != anim.PhaseDone {
		t.Fatalf("the exit is %v at its full duration", p)
	}
	_ = protocol.Version
}

package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/client"
	"link-chat/pkg/protocol"
	"link-chat/pkg/tui/anim"
	"link-chat/pkg/tui/theme"
	"link-chat/pkg/tui/views"
)

// signInFromStage puts the app on the stage and signs it in the way a successful
// login does, on the frozen clock, with two conversations about to arrive.
func signInFromStage(t *testing.T) (*App, *time.Time) {
	t.Helper()
	a, now := stageApp(t)
	a.render() // one stage frame, as the user would have seen
	a.onAuthenticated(protocol.AuthRes{User: protocol.User{ID: "u1", Username: "alice"}})
	land(a, []protocol.Conversation{
		{ID: "d1", Name: "carol", Kind: protocol.KindDM, LastMessageAt: 300},
		{ID: "c1", Name: "planet", Kind: protocol.KindChannel, LastMessageAt: 200},
	})
	return a, now
}

func at(a *App, now *time.Time, start time.Time, frac float64) string {
	*now = start.Add(time.Duration(float64(anim.EntranceDuration) * frac))
	a.render()
	return screenText(a.scr.(tcell.SimulationScreen))
}

// rowsWith counts the rows that contain s.
func rowsWith(screen, s string) int {
	n := 0
	for _, ln := range strings.Split(screen, "\n") {
		if strings.Contains(ln, s) {
			n++
		}
	}
	return n
}

// heavyOrBrightRows is the rows holding a long run of the entrance's rule glyphs.
func ruleRows(s tcell.SimulationScreen, minRun int) []int {
	w, h := s.Size()
	var out []int
	for y := 0; y < h; y++ {
		run, best := 0, 0
		for x := 0; x < w; x++ {
			if r, _, _, _ := s.GetContent(x, y); r == '─' || r == '━' {
				run++
				if run > best {
					best = run
				}
			} else {
				run = 0
			}
		}
		if best >= minRun {
			out = append(out, y)
		}
	}
	return out
}

func TestReadingTheBudgetDoesNotEndTheEntrance(t *testing.T) {
	// Asking whether frames are needed is a read: it must not end the entrance.
	a, now := signInFromStage(t)
	if !a.needsFrames(*now) || !a.needsFrames(*now) || a.anim.entrance == nil {
		t.Fatal("reading the frame budget changed the entrance")
	}
}

func TestEntrancePlaysItsFourPhases(t *testing.T) {
	a, now := signInFromStage(t)
	start := *now
	scr := a.scr.(tcell.SimulationScreen)
	w, h := scr.Size()
	panel := views.LoginPanelRect(w, h)

	// 1. Collapse: the panel's box is shorter than the panel was, no form inside,
	// and nothing of the main interface yet.
	early := at(a, now, start, 0.12)
	if strings.Contains(early, "planet") || strings.Contains(early, "私聊") {
		t.Fatalf("the main interface shows during the collapse:\n%s", early)
	}
	if strings.Contains(early, "用户名") {
		t.Fatalf("the form is still drawn while the panel collapses:\n%s", early)
	}
	top, bot := -1, -1
	for y := 0; y < h; y++ {
		if r, _, _, _ := scr.GetContent(panel.X, y); r == '┌' {
			top = y
		}
		if r, _, _, _ := scr.GetContent(panel.X, y); r == '└' {
			bot = y
		}
	}
	if top < 0 || bot < 0 || bot-top+1 >= panel.H {
		t.Fatalf("mid-collapse the box spans rows %d..%d, want shorter than the panel's %d", top, bot, panel.H)
	}

	// 2. Stretch: one rule wider than the panel, still no main interface.
	stretch := at(a, now, start, 0.40)
	if rows := ruleRows(scr, panel.W+2); len(rows) != 1 {
		t.Fatalf("mid-stretch want exactly one rule wider than the panel, got rows %v:\n%s", rows, stretch)
	}
	if strings.Contains(stretch, "planet") {
		t.Fatal("the main interface shows during the stretch")
	}

	// 3. Open: two rules, the main interface between them only.
	opening := at(a, now, start, 0.75)
	rows := ruleRows(scr, w/2)
	if len(rows) != 2 {
		t.Fatalf("mid-open want two rules, got rows %v:\n%s", rows, opening)
	}
	topRule, botRule := views.ChromeRuleRows(h)
	if rows[0] <= topRule || rows[1] >= botRule {
		t.Fatalf("mid-open the rules are at %v, want strictly between %d and %d", rows, topRule, botRule)
	}
	lines := strings.Split(opening, "\n")
	for y, ln := range lines {
		if (y < rows[0] || y > rows[1]) && strings.TrimSpace(strings.Trim(ln, ".· ")) != "" {
			t.Fatalf("row %d outside the two rules is not background: %q", y, ln)
		}
	}
	between := strings.Join(lines[rows[0]:rows[1]+1], "\n")
	if !strings.ContainsAny(between, "│") {
		t.Fatalf("between the two rules there is no main interface:\n%s", between)
	}

	// 4. Done.
	at(a, now, start, 1.0)
	if a.entranceRunning(*now) {
		t.Fatal("the entrance is still running at its full duration")
	}
}

// The last frame is the main interface cell for cell: the same state rendered with
// no entrance at all.
func TestEntranceEndsOnTheMainInterfaceExactly(t *testing.T) {
	a, now := signInFromStage(t)
	start := *now
	final := at(a, now, start, 1.0)

	b, bnow := stageApp(t)
	*bnow = *now
	b.st.MotionOff = true // signs in with no entrance
	b.onAuthenticated(protocol.AuthRes{User: protocol.User{ID: "u1", Username: "alice"}})
	land(b, []protocol.Conversation{
		{ID: "d1", Name: "carol", Kind: protocol.KindDM, LastMessageAt: 300},
		{ID: "c1", Name: "planet", Kind: protocol.KindChannel, LastMessageAt: 200},
	})
	b.st.MotionOff = false
	b.render()
	if direct := screenText(b.scr.(tcell.SimulationScreen)); direct != final {
		t.Fatalf("the entrance's last frame differs from the main interface:\n--- entrance\n%s\n--- direct\n%s", final, direct)
	}
	if a.needsFrames(*now) && !a.listSweeping() {
		t.Fatal("frames are still requested after the entrance, with nothing else animating")
	}
}

// Turning animation off means no entrance: the next frame is the main interface.
func TestNoEntranceWithAnimationOff(t *testing.T) {
	a, _ := stageApp(t)
	a.st.MotionOff = true
	a.onAuthenticated(protocol.AuthRes{User: protocol.User{ID: "u1", Username: "alice"}})
	if a.anim.entrance != nil {
		t.Fatal("an entrance was started with animation off")
	}
	a.render()
	if !hasChromeRules(a.scr.(tcell.SimulationScreen)) {
		t.Fatal("with animation off the first frame after sign-in is not the main interface")
	}
}

// A reconnect never left the main interface, so it has nothing to enter.
func TestNoEntranceOnReconnect(t *testing.T) {
	a, _ := stageApp(t)
	a.ui.Dialog = views.DialogNone // signed in earlier; the connection dropped
	a.st.State = client.StateDisconnected
	a.onAuthenticated(protocol.AuthRes{User: protocol.User{ID: "u1", Username: "alice"}})
	if a.anim.entrance != nil {
		t.Fatal("a reconnect started the sign-in entrance")
	}
}

// Signing in from registration or recovery plays it too.
func TestEntranceFromRegisterAndRecover(t *testing.T) {
	for _, d := range []views.DialogKind{views.DialogRegister, views.DialogRecover} {
		a, _ := stageApp(t)
		a.ui.Dialog = d
		a.onAuthenticated(protocol.AuthRes{User: protocol.User{ID: "u1", Username: "alice"}})
		if a.anim.entrance == nil {
			t.Errorf("signing in from dialog %v played no entrance", d)
		}
	}
}

// A key during the entrance ends it and is handled as usual.
func TestAKeyDuringTheEntranceSkipsItAndWorks(t *testing.T) {
	a, now := signInFromStage(t)
	start := *now
	at(a, now, start, 0.3)
	before := a.ui.Selected
	a.handleKey(context.Background(), specialKey(tcell.KeyDown))
	if a.anim.entrance != nil {
		t.Fatal("the key did not end the entrance")
	}
	if a.ui.Selected == before {
		t.Fatal("the key was swallowed by the entrance instead of moving the selection")
	}
	a.render()
	if !hasChromeRules(a.scr.(tcell.SimulationScreen)) {
		t.Fatal("after the key the main interface is not shown whole")
	}
}

// The two halves of the rule open together: at any moment of the opening each is
// the same distance from the rule it lands on (within a cell of rounding), so
// neither arrives early and waits for the other.
func TestTheTwoHalvesArriveTogether(t *testing.T) {
	a, now := signInFromStage(t)
	scr := a.scr.(tcell.SimulationScreen)
	// Use the taller registration panel, whose middle row is not the rules'
	// midpoint, so the split's starting row matters.
	a.ui.Dialog = views.DialogRegister
	a.beginEntrance()
	a.ui.Dialog = views.DialogNone
	start := *now
	w, h := scr.Size()
	topEnd, botEnd := views.ChromeRuleRows(h)
	for _, f := range []float64{0.55, 0.65, 0.75, 0.85, 0.95} {
		at(a, now, start, f)
		rows := ruleRows(scr, w/2)
		if len(rows) != 2 {
			t.Fatalf("at %.0f%%: want two rules, got %v", f*100, rows)
		}
		up, down := rows[0]-topEnd, botEnd-rows[1]
		if d := up - down; d < -1 || d > 1 {
			t.Fatalf("at %.0f%%: the top half has %d rows to go and the bottom %d", f*100, up, down)
		}
	}
}

// What arrives during the entrance shows in the uncovered part straight away.
func TestTheListArrivingDuringTheEntranceShowsAtOnce(t *testing.T) {
	a, now := stageApp(t)
	a.onAuthenticated(protocol.AuthRes{User: protocol.User{ID: "u1", Username: "alice"}})
	start := *now
	at(a, now, start, 0.8) // the opening, with an empty list
	land(a, []protocol.Conversation{{ID: "c9", Name: "latecomer", Kind: protocol.KindChannel, LastMessageAt: 9}})
	if screen := at(a, now, start, 0.95); !strings.Contains(screen, "latecomer") {
		t.Fatalf("a conversation that arrived mid-entrance is not in the uncovered part:\n%s", screen)
	}
}

// themeTokens builds ink tokens at a reduced terminal capability.
func themeTokens(mono, box bool) theme.Tokens {
	cap := theme.ColorTrue
	if mono {
		cap = theme.ColorMono
	}
	return theme.NewTokens(theme.Ink, cap, box)
}

// True colour draws a thin rule in a bright colour that is not the plain border
// colour; 256 colours cannot carry that, so the rule goes heavy in the accent.
func TestEntranceRuleAtTheColourTiers(t *testing.T) {
	for _, c := range []struct {
		name  string
		cap   theme.ColorCapacity
		glyph rune
	}{
		{"true colour", theme.ColorTrue, '─'},
		{"256 colours", theme.Color256, '━'},
	} {
		a, now := signInFromStage(t)
		a.tokens = theme.NewTokens(theme.Ink, c.cap, true)
		at(a, now, *now, 0.40)
		scr := a.scr.(tcell.SimulationScreen)
		rows := ruleRows(scr, 60)
		if len(rows) != 1 {
			t.Fatalf("%s: want one rule mid-stretch, got %v", c.name, rows)
		}
		w, _ := scr.Size()
		r, _, st, _ := scr.GetContent(w/2, rows[0])
		if r != c.glyph {
			t.Errorf("%s: the rule is drawn with %q, want %q", c.name, r, c.glyph)
		}
		if fg, _, _ := st.Decompose(); fg == a.tokens.BorderSoft {
			t.Errorf("%s: the bright rule has the plain border colour", c.name)
		}
	}
}

// Monochrome and no-box terminals: heavy rule, or ASCII.
func TestEntranceRuleDegrades(t *testing.T) {
	for _, c := range []struct {
		name       string
		mono, box  bool
		want, dont rune
	}{
		{"monochrome", true, true, '━', '='},
		{"no box glyphs", true, false, '=', '━'},
	} {
		a, now := signInFromStage(t)
		a.tokens = themeTokens(c.mono, c.box)
		start := *now
		at(a, now, start, 0.40)
		scr := a.scr.(tcell.SimulationScreen)
		w, h := scr.Size()
		found := false
		for y := 0; y < h && !found; y++ {
			for x := 0; x < w; x++ {
				r, _, _, _ := scr.GetContent(x, y)
				if r == c.dont {
					t.Fatalf("%s: found %q in the rule", c.name, r)
				}
				if r == c.want {
					found = true
					break
				}
			}
		}
		if !found {
			t.Fatalf("%s: no %q rule mid-stretch", c.name, c.want)
		}
	}
}

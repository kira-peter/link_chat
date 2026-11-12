package app

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/client"
	"link-chat/pkg/i18n"
	"link-chat/pkg/tui/views"
)

// stageApp is an app on the sign-in stage, as at startup, with a frozen clock.
func stageApp(t *testing.T) (*App, *time.Time) {
	t.Helper()
	a, _ := newKeyTestApp(t)
	now := freezeClock(t, time.Unix(40000, 0))
	a.st.State = client.StateConnecting
	a.ui.Dialog = views.DialogLogin
	a.anim.stageStart = *now
	return a, now
}

// The words that only the main interface shows. None of them may appear on the
// stage.
func mainOnlyWords(lang i18n.Lang) []string {
	return []string{
		i18n.AppName.Get(lang) + " ", // the title bar's "link_chat ●"
		i18n.SectionDMTitle.Get(lang),
		i18n.SectionChannelTitle.Get(lang),
		i18n.EntrySettings.Get(lang),
		"Alt+N", "Alt+O", "Tab",
	}
}

// hasChromeRules reports whether the main interface's top or bottom rule is on
// screen: a rule starting at the left gutter on the row under the title bar or the
// row above the hint bar. The panel's own border is centred, never at the gutter.
func hasChromeRules(s tcell.SimulationScreen) bool {
	w, h := s.Size()
	for _, y := range []int{1, h - 2} {
		run := 0
		for x := 2; x < w-2; x++ {
			if r, _, _, _ := s.GetContent(x, y); r == '─' {
				run++
			} else {
				break
			}
		}
		if run > 10 {
			return true
		}
	}
	return false
}

func TestStageShowsNothingOfTheMainInterface(t *testing.T) {
	for _, d := range []views.DialogKind{views.DialogLogin, views.DialogRegister, views.DialogRecover} {
		a, _ := stageApp(t)
		a.ui.Dialog = d
		a.render()
		text := screenText(a.scr.(tcell.SimulationScreen))
		for _, w := range mainOnlyWords(a.lang) {
			// The login panel's own hint line says "Tab 切换"; what must be absent
			// is the main interface's hint bar, so look for that row's shape.
			if w == "Tab" {
				continue
			}
			if strings.Contains(text, w) {
				t.Errorf("dialog %v: the stage shows %q from the main interface:\n%s", d, w, text)
			}
		}
		if hasChromeRules(a.scr.(tcell.SimulationScreen)) {
			t.Errorf("dialog %v: the main interface's rules are drawn on the stage", d)
		}
	}
}

// The panel is centred whatever the size, and the background fills the new size.
func TestStageRecentresOnResize(t *testing.T) {
	a, _ := stageApp(t)
	scr := a.scr.(tcell.SimulationScreen)
	for _, sz := range [][2]int{{100, 30}, {80, 24}, {140, 40}} {
		scr.SetSize(sz[0], sz[1])
		a.render()
		want := views.LoginPanelRect(sz[0], sz[1])
		if r, _, _, _ := scr.GetContent(want.X, want.Y); r != '┌' {
			t.Fatalf("%dx%d: the panel's corner is not at %v (found %q)", sz[0], sz[1], want, r)
		}
		for _, w := range mainOnlyWords(a.lang) {
			if w == "Tab" {
				continue
			}
			if strings.Contains(screenText(scr), w) {
				t.Fatalf("%dx%d: %q from the main interface after resizing", sz[0], sz[1], w)
			}
		}
		if hasChromeRules(scr) {
			t.Fatalf("%dx%d: the main interface's rules after resizing", sz[0], sz[1])
		}
	}
}

// The check above is only worth anything if it sees the rules on the main
// interface; prove it does.
func TestChromeRuleDetectorSeesTheMainInterface(t *testing.T) {
	a, _ := stageApp(t)
	a.st.State = client.StateReady
	a.ui.Dialog = views.DialogNone
	a.render()
	if !hasChromeRules(a.scr.(tcell.SimulationScreen)) {
		t.Fatal("the rule detector does not see the main interface's rules")
	}
}

// The displaced-session notice is not part of the stage: it sits over the main
// interface, which stays visible behind it.
func TestDisplacedNoticeStaysOverTheMainInterface(t *testing.T) {
	a, _ := stageApp(t)
	a.st.State = client.StateAuthFailed
	a.ui.Dialog = views.DialogSessionReplaced
	if a.onStage() {
		t.Fatal("the displaced-session notice is treated as the stage")
	}
	a.render()
	if !strings.Contains(screenText(a.scr.(tcell.SimulationScreen)), i18n.SectionDMTitle.Get(a.lang)) {
		t.Fatal("the main interface is not visible behind the displaced-session notice")
	}
}

// With animation off the dots hold still: two frames a second apart are the same.
func TestStageDotsHoldStillWithAnimationOff(t *testing.T) {
	a, now := stageApp(t)
	a.st.MotionOff = true
	a.render()
	before := screenText(a.scr.(tcell.SimulationScreen))
	advance(now, 3*time.Second)
	a.render()
	if after := screenText(a.scr.(tcell.SimulationScreen)); after != before {
		t.Fatal("the stage changed between frames with animation off")
	}
	if !strings.ContainsAny(before, ".·") {
		t.Fatal("with animation off the dots should still be shown, only still")
	}
}

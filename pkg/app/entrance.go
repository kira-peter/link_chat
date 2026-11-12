package app

import (
	"time"

	"link-chat/pkg/tui/anim"
	"link-chat/pkg/tui/views"
	"link-chat/pkg/tui/widget"
)

// The sign-in entrance (spec tui-login-stage / 登录成功的入场动画).
//
// It is started when the user signs in from the stage, plays for
// anim.EntranceDuration as a layer over the main interface, and is gone. Any key
// ends it at once. It never plays on a reconnect: there the main interface was on
// screen all along, so there is nothing to enter.

// entrance is the sign-in entrance in progress.
type entrance struct {
	start time.Time
	// panel is where the sign-in panel was, which is where the collapse happens.
	panel widget.Rect
	// stageElapsed freezes the dots where they were, so they fade out in place.
	stageElapsed time.Duration
}

// beginEntrance starts the entrance if the user is signing in from the stage and
// animation is on. It must be called before the panel is taken down, since it
// needs to know the panel was up and where it was.
func (a *App) beginEntrance() {
	if !a.onStage() || !a.animated() {
		a.anim.entrance = nil
		return
	}
	w, h := a.scr.Size()
	a.anim.entrance = &entrance{
		start:        nowTime(),
		panel:        a.stagePanel(w, h),
		stageElapsed: a.stageElapsed(),
	}
}

// entranceRunning reports whether the entrance is still playing at now. It only
// reads: asking whether frames are needed must never change what is animating.
// A finished entrance is simply one whose time is up.
func (a *App) entranceRunning(now time.Time) bool {
	e := a.anim.entrance
	return e != nil && now.Sub(e.start) < anim.EntranceDuration
}

// skipEntrance ends the entrance or the exit at once. A key pressed during either
// is meant for the screen it is arriving at, and the user should see that screen
// whole.
func (a *App) skipEntrance() {
	a.anim.entrance = nil
	a.anim.exit = nil
}

// renderEntrance draws the entrance layer over the main interface, if one is
// playing.
func (a *App) renderEntrance(w, h int) {
	now := nowTime()
	if !a.entranceRunning(now) {
		return
	}
	e := a.anim.entrance
	phase, p := anim.EntranceAt(now.Sub(e.start))
	views.EntranceOverlay{T: a.tokens}.Render(a.scr, w, h, views.EntranceModel{
		Phase: phase, P: p, Panel: e.panel, StageElapsed: e.stageElapsed, Depth: a.animDepth(),
	})
}

// The sign-out exit (spec tui-login-stage / 退出登录的退场动画) is the entrance
// played backwards: the main interface's two rules close in over it, meet as one
// bright line, the line shrinks to the panel's width and opens into the sign-in
// panel.
//
// The state moves to the stage the moment the user confirms — the session is over
// and nothing on the old screen works any more — so the main interface the exit
// closes over is a picture taken just before.

// exit is the sign-out exit in progress.
type exit struct {
	start time.Time
	// under is the main interface as it was when the user confirmed.
	under *views.Snapshot
	w, h  int
}

// beginExit takes the picture of the main interface and starts the exit, if
// animation is on. Call it before the state is cleared.
func (a *App) beginExit() {
	a.anim.entrance = nil
	if !a.animated() {
		a.anim.exit = nil
		return
	}
	// Draw the main interface as it stands, without the confirmation over it, and
	// keep that picture.
	d := a.ui.Dialog
	a.ui.Dialog = views.DialogNone
	a.render()
	a.ui.Dialog = d
	w, h := a.scr.Size()
	a.anim.exit = &exit{start: nowTime(), under: views.TakeSnapshot(a.scr), w: w, h: h}
}

// exitRunning reports whether the exit is still playing at now. Read only.
func (a *App) exitRunning(now time.Time) bool {
	e := a.anim.exit
	return e != nil && now.Sub(e.start) < anim.EntranceDuration
}

// renderExit draws the exit layer over the sign-in stage, if one is playing. A
// change of window size ends it: the picture no longer fits the screen.
func (a *App) renderExit(w, h int) {
	now := nowTime()
	if !a.exitRunning(now) {
		return
	}
	e := a.anim.exit
	if e.w != w || e.h != h {
		a.anim.exit = nil
		return
	}
	phase, p := anim.ExitAt(now.Sub(e.start))
	views.EntranceOverlay{T: a.tokens}.Render(a.scr, w, h, views.EntranceModel{
		Phase: phase, P: p, Panel: a.stagePanel(w, h), StageElapsed: a.stageElapsed(),
		Depth: a.animDepth(), Under: e.under, Reveal: true,
	})
}

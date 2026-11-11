package app

import (
	"time"

	"link-chat/pkg/client"
	"link-chat/pkg/tui/layout"
	"link-chat/pkg/tui/views"
)

// Folding.
//
// The sidebar folds away while a conversation has the keyboard, and unfolds when
// the list gets it back. The rule is one sentence — the fold target follows the
// focus — and that is deliberately all there is to it: Enter on a conversation,
// →, Esc and Tab are just different ways of moving focus, so none of them needs
// its own animation code (design.md decision 2).

// canFold reports whether folding applies at all. In the single-pane layout the
// window is too narrow for two columns, so one pane is shown at a time and there
// is no sidebar beside a conversation to fold: the pane simply switches.
func (a *App) canFold() bool {
	w, h := a.scr.Size()
	return views.ContentRect(w, h).W >= layout.MinViewWidth
}

// foldTarget is where the fold is heading: 1 (folded) while the composer has the
// keyboard and there is a conversation open, 0 (open) otherwise.
//
// "A conversation is open" matters. Without one the composer has nothing to type
// into, so folding the list would leave a full-width blank welcome screen with no
// way to pick anything — the list stays open until there is something to fold it
// for.
func (a *App) foldTarget() float64 {
	if !a.canFold() {
		return 0
	}
	if a.st.Focus == client.FocusInput && a.st.ActiveID != "" {
		return 1
	}
	return 0
}

// foldNow is the current fold, 0..1. With folding not applicable it is always 0,
// so the wide-layout renderer sees an open sidebar.
//
// It re-aims the fold before reading it. The fold's target depends on the focus
// and on whether a conversation is open, and several things can change the latter
// without passing through the app's own methods — the state layer clears the open
// conversation when the server deletes it, for one. Re-aiming on every read makes
// the fold correct however the state got there, instead of depending on every
// such path remembering to call syncFold. syncFold is idempotent, so doing it
// here costs a comparison and cannot restart an animation in flight.
func (a *App) foldNow(now time.Time) float64 {
	if !a.canFold() {
		return 0
	}
	a.syncFold()
	return a.anim.fold.At(now)
}

// syncFold points the fold at its target. It is called wherever focus or the open
// conversation can change, and is idempotent: if the fold is already heading to
// the target it does nothing, so calling it too often costs nothing and calling it
// from several places cannot restart an animation in flight.
//
// With animation off the move is instant. An in-flight fold that is redirected
// continues from its current value, so reversing mid-way never snaps back to the
// start (spec tui-design-system / 版面过渡的时间预算).
func (a *App) syncFold() {
	target := a.foldTarget()
	if a.anim.fold.Target() == target {
		return
	}
	a.anim.fold.MoveTo(target, nowTime(), foldDuration, a.animated())
}

// syncMarker re-aims the focus marker at where the focused region's title now is.
//
// The marker sits beside that title, and where the title is depends on the fold:
// with the composer focused and the list folded it is at the left gutter, not to
// the right of an open sidebar. Focus changes already slide the marker, but opening
// a conversation while the composer already has the keyboard moves the title with
// no focus change to trigger anything.
//
// This is deliberately not part of syncFold. syncFold is called from read paths —
// drawing a frame, asking whether frames are needed — where it is harmless because
// it only ever re-aims something already in motion toward the right target. Moving
// the marker starts a new animation, and a read must never start one: asking
// whether the screen is at rest would itself make it not at rest. So this is called
// only where state actually changes.
func (a *App) syncMarker() {
	if want := a.regionX(a.st.Focus); a.anim.slide.Target() != want {
		a.anim.slide.MoveTo(want, nowTime(), foldDuration, a.animated())
	}
}

// settleFold jumps the fold to its target with no animation. Used where an
// animation is not wanted — switching animation off, or a resize that changes
// whether folding applies at all.
func (a *App) settleFold() {
	a.anim.fold.MoveTo(a.foldTarget(), nowTime(), 0, false)
}

// reconcileFocus keeps the keyboard somewhere it can be used. The composer needs an
// open conversation to type into, so when the open one disappears — the server
// deleted the channel, or a refresh no longer lists it — focus returns to the list,
// which the fold has just reopened. Without this the list would unfold around a
// keyboard still parked in a composer that goes nowhere.
//
// It belongs with the code that changes the open conversation, not in the render
// path: drawing a frame must not move focus.
func (a *App) reconcileFocus() {
	if a.st.ActiveID == "" && a.st.Focus == client.FocusInput {
		a.moveFocus(client.FocusList)
		return
	}
	a.syncFold()
	a.syncMarker()
}

// folding reports whether a fold is in flight, for the frame budget. It re-aims
// first for the same reason foldNow does: a fold that has just been invalidated by
// a state change must be seen as in flight, or the frame that would draw it never
// gets scheduled.
func (a *App) folding(now time.Time) bool {
	if !a.canFold() {
		return false
	}
	a.syncFold()
	return !a.anim.fold.Done(now)
}

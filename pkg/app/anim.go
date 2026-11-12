package app

import (
	"time"

	"link-chat/pkg/client"
	"link-chat/pkg/tui/anim"
	"link-chat/pkg/tui/theme"
	"link-chat/pkg/tui/views"
)

// Animation timing.
//
// A frame is drawn at most every frameInterval while something is animating, and
// not at all otherwise. 30fps is plenty for motion this slow, and it is gentle on
// a remote terminal, where every frame costs bytes over the wire
// (design.md decision 4).
const (
	frameInterval      = 33 * time.Millisecond
	focusSlideDuration = 180 * time.Millisecond
	// foldDuration is how long the sidebar takes to fold or unfold. Longer than the
	// focus marker's slide because far more moves at once: the sidebar narrows, the
	// conversation widens and a name flies across. Under the 400 ms ceiling the
	// spec allows (spec tui-chat-client / 会话打开时的折叠过渡).
	foldDuration = 320 * time.Millisecond
)

// animation is the app's animation state. The marker's position is the only thing
// that has to persist between frames; everything else is derived from the current
// time at render.
type animation struct {
	// slide is the focus marker's horizontal position, in cells.
	slide anim.Slide
	// fold is how far the sidebar has folded, from 0 (open) to 1 (folded). It is
	// the single number the whole fold is read from: the sidebar's width, the
	// conversation's extent and the flying name all derive from it, so they cannot
	// drift apart (design.md decision 1).
	fold anim.Slide
	// shimmerStart is when the composer's hint sweep began its first pass. It is
	// reset whenever the composer regains focus, so the sweep always starts from
	// the left edge instead of mid-pass.
	shimmerStart time.Time
	// buttonStart is when the sign-in button sweep began. It is reset whenever the
	// button gains focus, so the band always enters from the left.
	buttonStart time.Time
	// listStart is when the sidebar's selection sweep began. It is reset whenever
	// the cursor lands on a new row or the list regains focus, so the band always
	// enters the newly selected row from the left instead of arriving mid-pass.
	listStart time.Time
}

// animated reports whether the user has animation switched on.
func (a *App) animated() bool { return !a.st.MotionOff }

// regionX is the horizontal cell where a region's focus marker sits: just left of
// that region's title.
//
// It is measured against where the layout will have settled once the focus has
// moved, not against the layout as it is this instant. The fold follows the focus,
// so with the composer focused the conversation spans the row and its title — and
// the marker beside it — sits at the top centre; with the list focused the marker
// is beside the list's title. With no conversation open there is no title on the
// right, so the marker goes to the transcript's left edge.
func (a *App) regionX(f client.Focus) float64 {
	w, h := a.scr.Size()
	content := views.ContentRect(w, h)
	if f == client.FocusList {
		return float64(views.SidebarRectAt(content, 0).X)
	}
	fold := 0.0
	if a.canFold() && a.st.ActiveID != "" {
		fold = 1
	}
	cv := a.st.Active()
	if cv == nil {
		return float64(views.MainRectAt(content, fold).X)
	}
	x, _ := views.TitleAnchor(content, fold, cv, a.lang)
	return float64(x)
}

// startFocusTransition begins sliding the marker to a region.
//
// If animation is off the move is instant, which is the contract for the global
// switch. If a slide is already running it is retargeted from wherever the marker
// currently is, so mashing the arrow keys never snaps it back to an old start.
func (a *App) startFocusTransition(to client.Focus) {
	now := nowTime()
	a.anim.slide.MoveTo(a.regionX(to), now, focusSlideDuration, a.animated())

	// Landing in the composer restarts its hint sweep from the left; landing in the
	// list restarts the selection sweep the same way.
	if to == client.FocusInput {
		a.anim.shimmerStart = now
	}
	if to == client.FocusList {
		a.anim.listStart = now
	}
}

// needsFrames decides whether the screen must keep redrawing on a timer.
//
// This is the whole animation budget in one place. It returns false whenever the
// interface is at rest, so a static screen costs nothing — no ticking, no
// repainting — which is what keeps this usable over SSH
// (spec tui-design-system / 静止界面不消耗刷新).
func (a *App) needsFrames(now time.Time) bool {
	if !a.animated() {
		return false
	}
	// A focus slide in flight.
	if !a.anim.slide.Done(now) {
		return true
	}
	// A fold or unfold in flight.
	if a.folding(now) {
		return true
	}
	// The sign-in button sweep.
	if a.loginButtonSweeping() {
		return true
	}
	// The sidebar's selection sweep.
	if a.listSweeping() {
		return true
	}
	// The composer's hint sweep: only while it is visible and means something.
	return a.shimmerActive()
}

// listSweeping reports whether the sidebar's selected row has a band running: the
// list must be on screen with the keyboard, with no dialog over it, and have a row
// to light. Anywhere else the band stops and the row falls back to the plain
// selection fill, so a resting screen costs no frames. The band also holds off
// while the sidebar is mid-fold: it is narrowing then, and two animations at once
// is noise
// (spec tui-chat-client / 选中项的光效).
func (a *App) listSweeping() bool {
	if a.ui.Dialog != views.DialogNone || a.st.Focus != client.FocusList {
		return false
	}
	if a.st.State != client.StateReady {
		return false
	}
	if a.canFold() && a.anim.fold.At(nowTime()) > 0 {
		return false
	}
	rows := a.st.SidebarRows()
	return a.ui.Selected >= 0 && a.ui.Selected < len(rows) && rows[a.ui.Selected].Selectable()
}

// loginButtonSweeping reports whether the sign-in button is on screen with focus,
// which is the only time its band runs. With focus elsewhere the panel is still,
// and a still panel costs no frames.
func (a *App) loginButtonSweeping() bool {
	return a.ui.Dialog == views.DialogLogin &&
		a.ui.Login.Focus == views.LoginFocusSubmit &&
		!a.ui.Login.Busy
}

// animDepth maps the terminal's colour capability onto the animation tiers:
// smooth blend, a few steps, or a single moving marker.
func (a *App) animDepth() anim.Depth {
	switch a.tokens.ColorCap {
	case theme.ColorTrue:
		return anim.DepthTrue
	case theme.Color256:
		return anim.Depth256
	default:
		return anim.DepthMono
	}
}

// shimmerActive reports whether the composer's hint is on screen and focused.
func (a *App) shimmerActive() bool {
	if a.ui.Dialog != views.DialogNone {
		return false
	}
	if a.st.Focus != client.FocusInput {
		return false
	}
	if len(a.st.Input) != 0 {
		return false
	}
	return a.st.State == client.StateReady
}

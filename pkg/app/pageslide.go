package app

import (
	"time"

	"link-chat/pkg/tui/anim"
	"link-chat/pkg/tui/views"
	"link-chat/pkg/tui/widget"
)

// pageSlideDuration is how long the page slide takes when the sidebar's cursor
// moves. Short: the user is skimming, and a slide that lags behind the cursor
// would be in the way. A new move while one is running starts from what is on
// screen, so holding ↓ never queues up slides.
const pageSlideDuration = 200 * time.Millisecond

// pageSlide is the slide in progress: the picture of the page that is leaving,
// which way the cursor went and when it started.
type pageSlide struct {
	old   *views.RectShot
	down  bool
	start time.Time
}

// startPageSlide is called just before the cursor moves to another row: the page
// on screen now is what slides away. Nothing slides with animation off, in the
// single-pane layout (the list stands alone there) or while the list is folding.
func (a *App) startPageSlide(down bool) {
	a.anim.page = nil
	if !a.animated() || !a.canFold() {
		return
	}
	now := nowTime()
	if a.anim.fold.At(now) != 0 || !a.anim.fold.Done(now) {
		return
	}
	r := a.pageSlideRect()
	if r.Empty() {
		return
	}
	// The screen still holds the last frame — the page as the user sees it, or,
	// mid-slide, the strip as it stands — so that is what leaves.
	a.anim.page = &pageSlide{old: views.TakeRectShot(a.scr, r), down: down, start: now}
}

// pageSlideRect is the area that slides: the page beside the open list.
func (a *App) pageSlideRect() widget.Rect {
	w, h := a.scr.Size()
	return views.MainRectAt(views.ContentRect(w, h), 0)
}

// pageSliding reports whether a slide is still moving, for the frame budget.
func (a *App) pageSliding(now time.Time) bool {
	p := a.anim.page
	return p != nil && now.Sub(p.start) < pageSlideDuration
}

// paintPageSlide lays the slide over the page just drawn in main. A slide that
// has ended, or whose area no longer matches (a resize, a fold), is dropped.
func (a *App) paintPageSlide(main widget.Rect, now time.Time) {
	p := a.anim.page
	if p == nil {
		return
	}
	el := now.Sub(p.start)
	if el >= pageSlideDuration || p.old.R != main || !a.animated() {
		a.anim.page = nil
		return
	}
	t := anim.EaseOutCubic(float64(el) / float64(pageSlideDuration))
	shift := int(t*float64(main.H) + 0.5)
	views.PaintPageSlide(a.scr, p.old, shift, p.down)
}

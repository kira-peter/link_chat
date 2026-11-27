package live

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/mattn/go-runewidth"
	"github.com/rivo/uniseg"

	"link-chat/pkg/i18n"
	"link-chat/pkg/server"
	"link-chat/pkg/tui/anim"
	"link-chat/pkg/tui/widget"
)

const (
	crtDuration     = 480 * time.Millisecond
	moduleDuration  = 720 * time.Millisecond
	completionPause = 280 * time.Millisecond
	blackPause      = 65 * time.Millisecond
	progressSpeed   = 220 // percentage points per second
)

type dashboardTransition struct {
	started, stopping, finished time.Time
	progressFrame, curtainAt    time.Time
	progress                    float64
}

func fraction(elapsed, duration time.Duration) float64 {
	return min(1, max(0, float64(elapsed)/float64(duration)))
}

func (t *dashboardTransition) drawStartup(screen tcell.Screen, d *dashboard, now time.Time) bool {
	elapsed := now.Sub(t.started)
	if elapsed < crtDuration {
		drawCRT(screen, fraction(elapsed, crtDuration))
		return true
	}
	if elapsed < crtDuration+moduleDuration {
		d.drawModules(screen, now, fraction(elapsed-crtDuration, moduleDuration))
		return true
	}
	return false
}

// drawShutdown returns true after the final black frame has been held.
func (t *dashboardTransition) drawShutdown(screen tcell.Screen, d *dashboard, now time.Time, phase server.ShutdownPhase) bool {
	openAtStop := fraction(t.stopping.Sub(t.started)-crtDuration, moduleDuration)
	open := openAtStop - fraction(now.Sub(t.stopping), moduleDuration)
	if open > 0 && !d.still {
		d.drawModules(screen, now, open)
		return false
	}
	if !t.curtainAt.IsZero() && !now.Before(t.curtainAt) {
		if d.still {
			return true
		}
		elapsed := now.Sub(t.curtainAt)
		drawCRT(screen, 1-fraction(elapsed, crtDuration))
		return elapsed >= crtDuration+blackPause
	}

	l := d.lang
	target, detail := shutdownCheckpoint(phase, l)
	if !t.finished.IsZero() {
		target, detail = 100, tr(l, "通信与存储已关闭", "Connections and storage are closed")
		if phase == server.ShutdownGraceExpired {
			detail = tr(l, "连接等待超时，存储已关闭", "Connection drain timed out. Storage is closed.")
		}
	}
	if t.progressFrame.IsZero() {
		t.progressFrame = now
	}
	delta := max(0, now.Sub(t.progressFrame).Seconds())
	t.progress = min(float64(target), t.progress+delta*progressSpeed)
	t.progressFrame = now
	if t.progress >= 100 && t.curtainAt.IsZero() {
		t.curtainAt = now.Add(completionPause)
	}
	drawShutdownProgress(screen, int(t.progress), detail, now.Sub(t.stopping), l)
	return false
}

func shutdownCheckpoint(phase server.ShutdownPhase, lang i18n.Lang) (int, string) {
	switch phase {
	case server.ShutdownNotifying:
		return 22, tr(lang, "通知客户端并停止接收新连接", "Notifying clients and stopping new connections")
	case server.ShutdownStoppingHTTP:
		return 42, tr(lang, "正在关闭 HTTP 监听", "Closing the HTTP listener")
	case server.ShutdownDraining:
		return 72, tr(lang, "等待现有连接和请求退出", "Waiting for connections and requests to finish")
	case server.ShutdownDrained:
		return 95, tr(lang, "连接已退出，正在关闭数据存储", "Connections closed. Closing storage.")
	case server.ShutdownGraceExpired:
		return 95, tr(lang, "连接等待超时，正在关闭数据存储", "Connection drain timed out. Closing storage.")
	default:
		return 10, tr(lang, "正在准备关停", "Preparing shutdown")
	}
}

func drawShutdownProgress(screen tcell.Screen, percent int, detail string, elapsed time.Duration, lang i18n.Lang) {
	screen.Clear()
	w, h := screen.Size()
	middle := h / 2
	title := fmt.Sprintf(tr(lang, "正在关停  %d%%", "Shutting down  %d%%"), percent)
	if percent == 100 {
		title = tr(lang, "关停完成  100%", "Shutdown complete  100%")
	}
	if h < 5 || w < 8 {
		centerText(screen, w, max(0, middle-1), title, accent.Bold(true))
		if h >= 2 {
			centerText(screen, w, middle, detail, bright)
		}
		screen.Show()
		return
	}
	centerText(screen, w, middle-2, title, accent.Bold(true))
	barWidth := min(44, w-4)
	barX := (w - barWidth) / 2
	drawRule(screen, barX, middle, barWidth, muted)
	filled := barWidth * percent / 100
	if filled > 0 {
		drawRule(screen, barX, middle, filled-1, accent.Bold(true))
		drawRule(screen, barX+filled-1, middle, 1, bright.Bold(true))
	}
	centerText(screen, w, middle+1, detail, bright)
	if middle+2 < h && percent < 100 {
		centerText(screen, w, middle+2, fmt.Sprintf(tr(lang, "已用时 %.1f 秒", "Elapsed %.1f s"), elapsed.Seconds()), muted)
	}
	screen.Show()
}

func centerText(screen tcell.Screen, width, y int, text string, style tcell.Style) {
	text = runewidth.Truncate(text, width, "")
	screen.PutStrStyled(max(0, (width-uniseg.StringWidth(text))/2), y, text, style)
}

// drawCRT uses one openness value in both directions. At the center the two
// edges join into a flash, which narrows to a single point before going dark.
func drawCRT(screen tcell.Screen, openness float64) {
	screen.Fill(' ', tcell.StyleDefault.Background(tcell.ColorBlack))
	w, h := screen.Size()
	if openness > 0 && w > 0 && h > 0 {
		const flash = 0.25
		if openness < flash {
			length := max(1, int(math.Round(float64(w)*openness/flash)))
			drawRule(screen, (w-length)/2, (h-1)/2, length, bright.Background(tcell.ColorBlack).Bold(true))
		} else {
			spread := anim.EaseOutCubic((openness - flash) / (1 - flash))
			upper := int(math.Round(float64((h-1)/2) * (1 - spread)))
			lower := h - 1 - upper
			edge := accent.Background(tcell.ColorBlack).Bold(true)
			drawRule(screen, 0, upper, w, edge)
			if lower != upper {
				drawRule(screen, 0, lower, w, edge)
			}
		}
	}
	screen.Show()
}

func (d *dashboard) drawModules(screen tcell.Screen, now time.Time, openness float64) {
	d.paint(screen, now)
	w, h := screen.Size()
	clip := func(index int, region widget.Rect) {
		visible := anim.EaseOutCubic((openness - 0.074*float64(index)) / 0.48)
		clipModule(screen, region, visible)
	}
	if d.systemExpanded || h < 6 {
		clip(0, widget.Rect{W: w, H: h})
	} else {
		divider, split := dashboardBody(w, h)
		clip(0, widget.Rect{W: w, H: 2})
		clip(1, widget.Rect{Y: 2, W: split, H: divider - 2})
		clip(2, widget.Rect{X: split, Y: 2, W: w - split, H: divider - 2})
		clip(3, widget.Rect{Y: divider, W: w, H: 1})
		for i := range server.ActivityCategories() {
			left, right := panelBounds(w, i)
			clip(4+i, widget.Rect{X: left, Y: divider + 1, W: right - left, H: h - divider - 1})
		}
	}
	screen.Show()
}

// clipModule keeps a shrinking rectangle inside the module. Cells occupied by
// a wide glyph are cleared together when its glyph touches the clip edge.
func clipModule(screen tcell.Screen, r widget.Rect, visible float64) {
	if r.Empty() || visible >= 1 {
		return
	}
	width := int(math.Round(float64(r.W) * visible))
	height := int(math.Round(float64(r.H) * visible))
	left := r.X + (r.W-width)/2
	top := r.Y + (r.H-height)/2
	for y := r.Y; y < r.Y+r.H; y++ {
		for x := r.X; x < r.X+r.W; {
			_, _, cells := screen.Get(x, y)
			cells = max(1, cells)
			if y < top || y >= top+height || x < left || x+cells > left+width {
				screen.PutStrStyled(x, y, strings.Repeat(" ", cells), tcell.StyleDefault)
			}
			x += cells
		}
	}
}

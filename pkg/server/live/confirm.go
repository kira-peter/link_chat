package live

import (
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/uniseg"

	"link-chat/pkg/i18n"
	"link-chat/pkg/tui/anim"
	"link-chat/pkg/tui/theme"
	"link-chat/pkg/tui/views"
)

type quitChoice int

const (
	quitHidden quitChoice = iota
	quitCancel
	quitConfirm
)

func (choice *quitChoice) handle(event *tcell.EventKey) bool {
	switch event.Key() {
	case tcell.KeyLeft:
		*choice = quitCancel
	case tcell.KeyRight:
		*choice = quitConfirm
	case tcell.KeyTab, tcell.KeyBacktab:
		if *choice == quitCancel {
			*choice = quitConfirm
		} else {
			*choice = quitCancel
		}
	case tcell.KeyEscape:
		*choice = quitHidden
	case tcell.KeyEnter:
		shutdown := *choice == quitConfirm
		*choice = quitHidden
		return shutdown
	}
	return false
}

func (choice quitChoice) sweeping(age time.Duration) bool {
	return choice != quitHidden && age < anim.ButtonShimmer.Travel
}

func dashboardOverlayTheme(screen tcell.Screen) theme.Tokens {
	capacity := theme.ColorTrue
	colors := screen.Colors()
	switch {
	case colors <= 8:
		capacity = theme.ColorMono
	case colors <= 256:
		capacity = theme.Color256
	}
	return theme.NewTokens(theme.Ink, capacity, uniseg.StringWidth("─") == 1)
}

func drawQuitDialog(screen tcell.Screen, choice quitChoice, focusAge time.Duration, lang i18n.Lang) {
	dimDashboard(screen)
	views.Dialog{T: dashboardOverlayTheme(screen)}.RenderShutdownConfirm(screen, choice == quitConfirm, max(0, focusAge), lang)
	screen.Show()
}

func dimDashboard(screen tcell.Screen) {
	w, h := screen.Size()
	black := tcell.ColorBlack
	for y := range h {
		for x := 0; x < w; {
			glyph, style, width := screen.Get(x, y)
			if glyph == " " {
				screen.Put(x, y, glyph, tcell.StyleDefault.Background(black))
			} else {
				fg, _, _ := style.Decompose()
				shadow := tcell.ColorDarkGray
				if fg.Valid() {
					shadow = anim.Blend(black.TrueColor(), fg.TrueColor(), 0.32)
				}
				screen.Put(x, y, glyph, style.Foreground(shadow).Background(black).Dim(true))
			}
			x += max(1, width)
		}
	}
}

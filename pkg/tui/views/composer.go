package views

import (
	"time"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/client"
	"link-chat/pkg/i18n"
	"link-chat/pkg/tui/anim"
	"link-chat/pkg/tui/layout"
	"link-chat/pkg/tui/theme"
	"link-chat/pkg/tui/widget"
)

// Composer renders the message input.
//
// The frame appears only when the composer has focus. An unfocused input is just
// a line of dim placeholder text; that difference is what makes focus legible in
// a terminal, where there is no cursor to move around
// (spec tui-design-system / 对话框使用边框).
type Composer struct {
	T theme.Tokens
}

// ComposerModel is what the composer needs from the app.
type ComposerModel struct {
	Input       []rune
	Cursor      int
	Focus       client.Focus
	ReadOnly    bool
	Placeholder string
	Err         string
	MaxBytes    int

	// Shimmer, when set, sweeps a band of light across the empty-input hint.
	// Elapsed is how long the sweep has been running, passed in rather than read
	// from a clock so a render is a pure function of its inputs and a test can
	// freeze any moment (design.md decision 3).
	Shimmer bool
	Elapsed time.Duration
	// Depth says how much colour the terminal has, which decides how the band is
	// drawn: smooth blend, a few steps, or a single moving marker.
	Depth anim.Depth
}

// Render draws the composer into r.
func (v Composer) Render(s tcell.Screen, r widget.Rect, m ComposerModel, lang i18n.Lang) {
	if r.Empty() {
		return
	}
	c := widget.NewCanvas(s, v.T, r)
	c.Fill(v.T.Base())

	// Leave one blank row above the box so it does not collide with the
	// transcript's last line.
	box := widget.Rect{X: r.X, Y: r.Y + 1, W: r.W, H: r.H - 1}
	bc := widget.NewCanvas(s, v.T, box)

	focused := m.Focus == client.FocusInput && !m.ReadOnly

	// The prompt glyph carries focus: a filled caret when active, a faint dot
	// otherwise. Cheap, unambiguous, and it does not shift any text.
	prompt := "›"
	promptStyle := v.T.Faint()
	if focused {
		promptStyle = v.T.Accented()
	}

	inner := widget.Rect{X: box.X, Y: box.Y, W: box.W, H: box.H}

	if focused {
		bc.Box(v.T.BorderStyle())
		inner = widget.Rect{X: box.X + 2, Y: box.Y + 1, W: box.W - 4, H: box.H - 2}
		if inner.W <= 0 || inner.H <= 0 {
			return
		}
	}

	ic := widget.NewCanvas(s, v.T, inner)
	ic.Fill(v.T.Base())

	// Prompt.
	ic.Text(0, 0, prompt, promptStyle)

	textX := 2
	textW := inner.W - textX
	if textW <= 0 {
		return
	}

	if len(m.Input) == 0 {
		ph := m.Placeholder
		if m.ReadOnly {
			ph = i18n.ReadOnlyOffline.Get(lang)
		}
		ph = layout.Truncate(ph, textW)

		// Only an active, focused composer shimmers. Anything else shows plain
		// dim text, which is also what the animation-off setting produces.
		if m.Shimmer && focused && !m.ReadOnly {
			v.renderShimmer(ic, textX, 0, ph, m)
			return
		}
		ic.Text(textX, 0, ph, dimIf(v.T.Muted(), !focused))
		return
	}

	// Wrap the draft across the box. Cursor placement is computed on the same
	// wrapped grid, so typing past the first line keeps the caret visible.
	lines := layout.Wrap(string(m.Input), textW)
	caretLine, caretCol := cursorPosition(lines, m.Input, m.Cursor, textW)

	// Scroll so the caret's line is inside the box.
	h := inner.H
	start := 0
	if caretLine >= h {
		start = caretLine - h + 1
	}
	for i := start; i < len(lines) && i-start < h; i++ {
		ic.Text(textX, i-start, lines[i], v.T.Body())
	}

	// Caret: a block over the character at the cursor, or a space at end of
	// line. Drawing it ourselves (rather than relying on the terminal cursor)
	// keeps it visible with the display hidden and in tests.
	if focused {
		cy := caretLine - start
		if cy >= 0 && cy < h {
			cx := textX + caretCol
			ch := ' '
			col := 0
			for _, r := range lines[caretLine] {
				if col >= caretCol {
					ch = r
					break
				}
				col += runeCells(r)
			}
			style := v.T.Selected()
			if cx < inner.W {
				ic.S.SetContent(inner.X+cx, inner.Y+cy, ch, nil, style)
			}
		}
	}

	// Error takes precedence over the length counter: an error is actionable,
	// a counter is informational.
	if m.Err != "" {
		ec := widget.NewCanvas(s, v.T, widget.Rect{X: r.X, Y: r.Y + r.H - 1, W: r.W, H: 1})
		ec.Fill(v.T.Base())
		ec.Text(0, 0, layout.Truncate(m.Err, r.W-12), v.T.DangerStyle())
		return
	}

	// Remaining length, shown only when it starts to matter.
	if m.MaxBytes > 0 {
		used := len(string(m.Input))
		left := m.MaxBytes - used
		if left <= m.MaxBytes/5 {
			lc := widget.NewCanvas(s, v.T, widget.Rect{X: r.X, Y: r.Y + r.H - 1, W: r.W, H: 1})
			lc.Fill(v.T.Base())
			txt := i18n.Num(left)
			style := v.T.Faint()
			if left <= 0 {
				style = v.T.DangerStyle()
				txt = i18n.InputTooLong.Get(lang)
			}
			lc.TextRight(0, txt, style)
		}
	}
}

// cursorPosition maps a rune index in the input to (line, cell) in the wrapped
// output.
func cursorPosition(lines []string, input []rune, cursor, width int) (int, int) {
	if len(input) == 0 {
		return 0, 0
	}
	if cursor > len(input) {
		cursor = len(input)
	}
	// Walk the wrapped lines accumulating the rune count each consumed.
	consumed := 0
	col := 0
	for li, ln := range lines {
		lnRunes := []rune(ln)
		// A line may have had a space trimmed at its end when wrapping; the
		// rune count is still the best local estimate for caret placement.
		if cursor <= consumed+len(lnRunes) {
			// Position inside this line.
			col = 0
			for i := consumed; i < cursor && i-consumed < len(lnRunes); i++ {
				col += runeCells(lnRunes[i-consumed])
			}
			return li, col
		}
		consumed += len(lnRunes)
		// Wrapping usually eats one separating space between lines.
		if consumed < len(input) && input[consumed] == ' ' {
			consumed++
			if cursor == consumed {
				return li, layout.StringWidth(ln)
			}
		}
	}
	return len(lines) - 1, col
}

func runeCells(r rune) int {
	if r < 0x20 {
		return 0
	}
	return layout.RuneWidth(r)
}

// dimIf keeps a style as-is; the placeholder's dimness comes from the token it
// was built with. The helper exists so call sites read intentionally.
func dimIf(st tcell.Style, _ bool) tcell.Style { return st }

// renderShimmer draws the hint text with a band of light sweeping across it.
//
// Brightness is computed per *cell*, not per character. A Chinese glyph covers two
// cells, so indexing by character would make the band lurch two cells at a time on
// CJK text; indexing by cell keeps its motion smooth in either language. Each
// glyph takes the brightness at its leading cell.
//
// The text never moves — only colour changes — so the layout around it is
// untouched (spec tui-design-system / 动画不改变布局).
func (v Composer) renderShimmer(c *widget.Canvas, x, y int, text string, m ComposerModel) {
	total := layout.StringWidth(text)
	if total == 0 {
		return
	}
	band := anim.DefaultShimmer

	rest := v.T.TextFaint
	hi := v.T.Accent

	// Mono terminals cannot show brightness, so they get a moving marker instead:
	// one cell is drawn emphasised, and it travels along the text.
	marker := -1
	if m.Depth == anim.DepthMono {
		marker = band.ShimmerMarker(total, m.Elapsed)
	}

	cell := 0
	for _, r := range text {
		w := layout.RuneWidth(r)
		if w == 0 {
			continue
		}
		style := v.T.Base().Foreground(rest)

		switch {
		case m.Depth == anim.DepthMono:
			if cell == marker || (w == 2 && cell+1 == marker) {
				style = style.Bold(true).Underline(true)
			}
		default:
			b := band.Brightness(cell, total, m.Elapsed)
			style = v.T.Base().Foreground(anim.ShimmerColor(rest, hi, b, m.Depth))
		}

		c.Text(x+cell, y, string(r), style)
		cell += w
	}
}

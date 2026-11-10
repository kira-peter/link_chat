// Package layout holds the spacing grid and the pure text-measuring helpers.
//
// Everything here is a pure function over strings and integers, which is what
// lets layout be tested without a terminal. Terminal layout bugs are almost
// always width bugs: a CJK glyph counted as one cell, an emoji cluster split in
// half, a run of text that overflowed by one column. Centralising the arithmetic
// here removes that whole class of problem from the views
// (spec tui-design-system / 间距网格, CJK 与表情宽度正确).
package layout

import (
	"os"
	"strings"

	"github.com/mattn/go-runewidth"
)

// Grid constants. Every horizontal indent in the interface is a multiple of
// GutterX, and every vertical gap is a multiple of GutterY. Views must build
// positions from these rather than inventing offsets.
const (
	// GutterX is the base left margin, in cells.
	GutterX = 2
	// GutterY is the base vertical gap, in rows.
	GutterY = 1
	// IndentNested steps content in one level under a parent label.
	IndentNested = 2
	// ColumnGap is the empty space between two side-by-side regions.
	ColumnGap = 2
	// MinViewWidth is the narrowest usable window before collapsing to one pane.
	MinViewWidth = 48
	// SidebarMin and SidebarMax bound the conversation list width.
	SidebarMin = 18
	SidebarMax = 30
	// InputMinRows is how tall the composer is when empty.
	InputMinRows = 1
	// InputMaxRows caps the composer so a long draft cannot eat the transcript.
	InputMaxRows = 8
)

// cond fixes the width rules for the whole interface.
//
// It must agree with whatever the renderer believes, or every measured column is
// off by one for "ambiguous" code points — box drawing (│ ─ ┌), the ellipsis, ●.
// tcell reads RUNEWIDTH_EASTASIAN once at startup and switches its own width
// table accordingly, so the same switch is honoured here and nowhere else: views
// must never call runewidth directly.
//
// Off by default, which is what terminals actually draw.
var cond = &runewidth.Condition{
	EastAsianWidth:     eastAsianEnv(),
	StrictEmojiNeutral: true,
}

// eastAsianEnv mirrors tcell's rule so measurement and rendering cannot disagree.
func eastAsianEnv() bool {
	switch strings.ToLower(os.Getenv("RUNEWIDTH_EASTASIAN")) {
	case "1", "true", "yes":
		return true
	}
	return false
}

// RuneWidth returns the cells a single rune occupies under the interface's width
// rules.
func RuneWidth(r rune) int {
	return cond.RuneWidth(r)
}

// StringWidth returns the number of terminal cells a string occupies.
//
// Never use len() for this: it counts bytes, so a single Chinese character counts
// as three and an emoji as four.
func StringWidth(s string) int {
	return cond.StringWidth(s)
}

// Truncate shortens s to at most width cells, appending an ellipsis when it had
// to cut. The result never exceeds width; callers rely on that to keep alignment.
func Truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if StringWidth(s) <= width {
		return s
	}
	// The ellipsis is not always one cell: under East Asian width rules it is
	// ambiguous and some terminals render it as two. Measure it rather than
	// assuming, or the result silently overflows by a cell.
	ellipsisW := StringWidth(Ellipsis)
	if width < ellipsisW {
		return clipToWidth(s, width)
	}
	target := width - ellipsisW

	body := clipToWidth(s, target)
	if body == "" {
		return Ellipsis
	}
	return body + Ellipsis
}

// TruncateMiddle keeps both ends of s, which is right for paths and usernames
// where the tail carries the distinguishing part.
func TruncateMiddle(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if StringWidth(s) <= width {
		return s
	}
	if width <= 3 {
		return Truncate(s, width)
	}
	// Build from runes but measure in cells, so wide glyphs cannot push the
	// result past the budget.
	budget := width - StringWidth(Ellipsis)
	if budget < 2 {
		return clipToWidth(s, width)
	}
	headBudget := budget / 2
	tailBudget := budget - headBudget

	runes := []rune(s)
	var head strings.Builder
	used := 0
	for _, r := range runes {
		w := RuneWidth(r)
		if used+w > headBudget {
			break
		}
		head.WriteRune(r)
		used += w
	}

	var tail []rune
	used = 0
	for i := len(runes) - 1; i >= 0; i-- {
		w := RuneWidth(runes[i])
		if used+w > tailBudget {
			break
		}
		tail = append([]rune{runes[i]}, tail...)
		used += w
	}
	return head.String() + Ellipsis + string(tail)
}

// PadRight pads s with spaces to exactly width cells. If s is too wide it is
// truncated first, so the caller always gets exactly width cells back — which is
// what keeps columns aligned.
func PadRight(s string, width int) string {
	w := StringWidth(s)
	if w == width {
		return s
	}
	if w > width {
		s = Truncate(s, width)
		w = StringWidth(s)
		if w > width { // pathological width-0 runes
			w = width
		}
	}
	return s + strings.Repeat(" ", width-w)
}

// PadLeft right-aligns s in width cells.
func PadLeft(s string, width int) string {
	w := StringWidth(s)
	if w > width {
		s = Truncate(s, width)
		w = StringWidth(s)
	}
	return strings.Repeat(" ", max(0, width-w)) + s
}

// Wrap breaks s into lines of at most width cells.
//
// Wrapping is done rune by rune on display width, so wide characters and emoji
// are never split across lines, and existing newlines are honoured. It prefers
// breaking at the last space when one exists on the line, which keeps prose
// readable, but it will break mid-word rather than overflow.
func Wrap(s string, width int) []string {
	if width <= 0 {
		return nil
	}
	var lines []string
	for _, para := range strings.Split(s, "\n") {
		if para == "" {
			lines = append(lines, "")
			continue
		}
		lineW := 0
		lastSpace := -1 // byte index into the current line where we may break
		var b strings.Builder
		flush := func() {
			lines = append(lines, b.String())
			b.Reset()
			lineW, lastSpace = 0, -1
		}
		for _, r := range para {
			w := RuneWidth(r)
			if lineW+w > width {
				if lastSpace >= 0 && lastSpace > 0 {
					// Break at the space: emit everything before it, carry the
					// remainder to the next line.
					cur := b.String()
					head := cur[:lastSpace]
					rest := strings.TrimLeft(cur[lastSpace:], " ")
					lines = append(lines, head)
					b.Reset()
					b.WriteString(rest)
					lineW = StringWidth(rest)
					lastSpace = -1
					// The carried remainder might itself overflow if the word is
					// longer than width; the loop below handles that by breaking
					// again without a space candidate.
				} else {
					lines = append(lines, b.String())
					b.Reset()
					lineW = 0
				}
			}
			b.WriteRune(r)
			lineW += w
			if r == ' ' {
				lastSpace = b.Len()
			}
		}
		flush()
	}
	return lines
}

// WrapIndent wraps s and indents every line after the first by indent cells, so
// continued lines align under the text rather than the label
// (spec tui-chat-client / 长消息折行).
func WrapIndent(s string, width, indent int) []string {
	if indent >= width {
		return Wrap(s, width)
	}
	lines := Wrap(s, width)
	if len(lines) <= 1 {
		return lines
	}
	pad := strings.Repeat(" ", indent)
	sub := width - indent
	out := make([]string, 0, len(lines))
	out = append(out, lines[0])
	for _, l := range lines[1:] {
		// Re-wrap at the narrower width so continuation lines are correct when
		// the first line happened to be short.
		for _, sub2 := range Wrap(l, sub) {
			out = append(out, pad+sub2)
		}
	}
	return out
}

// SidebarWidth picks the conversation list width for a given terminal width.
// It grows with the window but is clamped, so the transcript keeps a sane
// minimum share of the screen.
func SidebarWidth(total int) int {
	w := total / 4
	if w < SidebarMin {
		w = SidebarMin
	}
	if w > SidebarMax {
		w = SidebarMax
	}
	// Never let the sidebar take more than 40% of a narrow window.
	if w > total*2/5 {
		w = total * 2 / 5
	}
	return w
}

// TwoColumn splits a width into [left, gutter, right].
func TwoColumn(total, left int) (leftW, rightW int) {
	rightW = total - left - ColumnGap
	return left, rightW
}

// Ellipsis is the truncation mark. It is one variable so that its measured width
// and its rendered width can never disagree.
const Ellipsis = "…"

// clipToWidth returns the longest prefix of s that fits in width cells,
// counting whole runes only. A wide rune that does not fit ends the string
// rather than being split.
func clipToWidth(s string, width int) string {
	if width <= 0 {
		return ""
	}
	var b strings.Builder
	used := 0
	for _, r := range s {
		w := RuneWidth(r)
		if used+w > width {
			break
		}
		b.WriteRune(r)
		used += w
	}
	return b.String()
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

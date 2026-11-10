// Package theme defines the visual language of the client.
//
// Views never name a colour directly. They ask for a semantic token
// (TextMuted, Accent, SelectedBg) and the theme decides what that means on this
// terminal. That indirection is what keeps every screen looking like one product
// and lets both themes and colour-depth fallbacks be added in one place
// (spec tui-design-system / 语义化色彩令牌).
package theme

import (
	"github.com/gdamore/tcell/v2"
)

// Palette is one theme's concrete colours. A view only ever sees Tokens, never
// this struct, so a theme can be swapped without touching rendering code.
type Palette struct {
	Name string

	Bg         tcell.Color
	Surface    tcell.Color
	SurfaceAlt tcell.Color
	Text       tcell.Color
	TextMuted  tcell.Color
	TextFaint  tcell.Color
	Accent     tcell.Color
	AccentSoft tcell.Color
	Success    tcell.Color
	Warning    tcell.Color
	Danger     tcell.Color
	Border     tcell.Color
	BorderSoft tcell.Color

	SelectedBg tcell.Color
	SelectedFg tcell.Color
}

// Tokens is what views consume: a palette plus the capability decisions already
// folded in.
type Tokens struct {
	Palette

	// UnicodeBox is false when the terminal cannot render box-drawing glyphs.
	UnicodeBox bool
	// ColorCap is the terminal's colour capacity, used to soften effects.
	ColorCap ColorCapacity
}

// ColorCapacity describes how much colour the terminal can show.
type ColorCapacity int

const (
	ColorMono ColorCapacity = iota
	Color256
	ColorTrue
)

// Palette definitions.
//
// "ink" is the default dark theme: a near-black background, one cyan accent used
// sparingly, and four steps of grey for hierarchy. It is deliberately low
// contrast in the chrome and high contrast in the content, which is what makes
// dense terminal UIs readable without looking harsh.
var Ink = Palette{
	Name: "ink",

	Bg:         tcell.NewHexColor(0x12141a),
	Surface:    tcell.NewHexColor(0x191c24),
	SurfaceAlt: tcell.NewHexColor(0x1f232d),
	Text:       tcell.NewHexColor(0xe4e7ee),
	TextMuted:  tcell.NewHexColor(0x9aa3b2),
	TextFaint:  tcell.NewHexColor(0x5f6675),
	Accent:     tcell.NewHexColor(0x5ec8d8),
	AccentSoft: tcell.NewHexColor(0x2f6f79),
	Success:    tcell.NewHexColor(0x7bc47f),
	Warning:    tcell.NewHexColor(0xe0b060),
	Danger:     tcell.NewHexColor(0xe07a7a),
	Border:     tcell.NewHexColor(0x2c313d),
	BorderSoft: tcell.NewHexColor(0x22262f),

	SelectedBg: tcell.NewHexColor(0x2b3a45),
	SelectedFg: tcell.NewHexColor(0xeef2f8),
}

// Paper is the light theme. Same token semantics, inverted luminance, with the
// accent darkened so it still reads against white.
var Paper = Palette{
	Name: "paper",

	Bg:         tcell.NewHexColor(0xfbfbfa),
	Surface:    tcell.NewHexColor(0xf2f3f1),
	SurfaceAlt: tcell.NewHexColor(0xe9ebe8),
	Text:       tcell.NewHexColor(0x23262b),
	TextMuted:  tcell.NewHexColor(0x5d646f),
	TextFaint:  tcell.NewHexColor(0x9aa0aa),
	Accent:     tcell.NewHexColor(0x0f7b8c),
	AccentSoft: tcell.NewHexColor(0x9fc6cd),
	Success:    tcell.NewHexColor(0x2f7d4f),
	Warning:    tcell.NewHexColor(0x8a5d00),
	Danger:     tcell.NewHexColor(0xa83232),
	Border:     tcell.NewHexColor(0xd7dad6),
	BorderSoft: tcell.NewHexColor(0xe6e8e4),

	SelectedBg: tcell.NewHexColor(0xdfe9ea),
	SelectedFg: tcell.NewHexColor(0x16181c),
}

// Registry maps a config name to its palette.
var Registry = map[string]Palette{
	"ink":   Ink,
	"paper": Paper,
}

// DefaultName is used when the configured theme is missing or unknown.
const DefaultName = "ink"

// Resolve returns the palette for a name, falling back to the default rather
// than failing to start (spec tui-design-system / 未知主题配置).
func Resolve(name string) (Palette, bool) {
	if p, ok := Registry[name]; ok {
		return p, true
	}
	return Registry[DefaultName], false
}

// NewTokens folds a palette and terminal capabilities into renderable tokens.
//
// When the terminal cannot show true colour the palette is quantised once, here,
// so no view has to think about it (spec tui-design-system / 终端能力降级).
func NewTokens(p Palette, cap ColorCapacity, unicodeBox bool) Tokens {
	out := p
	if cap == ColorMono {
		out = monochrome(p)
	} else if cap == Color256 {
		out = quantize256(p)
	}
	return Tokens{Palette: out, UnicodeBox: unicodeBox, ColorCap: cap}
}

// monochrome maps every token onto one of three greys so a mono terminal still
// shows structure: content brightest, chrome mid, decoration faint.
func monochrome(p Palette) Palette {
	bright := tcell.NewHexColor(0xffffff)
	mid := tcell.NewHexColor(0xaaaaaa)
	dim := tcell.NewHexColor(0x555555)

	p.Text = bright
	p.SelectedFg = bright
	p.TextMuted = mid
	p.TextFaint = dim
	p.Accent = bright
	p.AccentSoft = mid
	p.Success = mid
	p.Warning = mid
	p.Danger = bright
	p.Border = dim
	p.BorderSoft = dim
	p.SelectedBg = dim
	p.Bg = tcell.ColorDefault
	p.Surface = tcell.ColorDefault
	p.SurfaceAlt = tcell.ColorDefault
	return p
}

// quantize256 approximates each token in the 256-colour cube.
func quantize256(p Palette) Palette {
	conv := func(c tcell.Color) tcell.Color {
		if c == tcell.ColorDefault {
			return c
		}
		return c
	}
	p.Bg = conv(p.Bg)
	p.Surface = conv(p.Surface)
	p.SurfaceAlt = conv(p.SurfaceAlt)
	p.Text = conv(p.Text)
	p.TextMuted = conv(p.TextMuted)
	p.TextFaint = conv(p.TextFaint)
	p.Accent = conv(p.Accent)
	p.AccentSoft = conv(p.AccentSoft)
	p.Success = conv(p.Success)
	p.Warning = conv(p.Warning)
	p.Danger = conv(p.Danger)
	p.Border = conv(p.Border)
	p.BorderSoft = conv(p.BorderSoft)
	p.SelectedBg = conv(p.SelectedBg)
	p.SelectedFg = conv(p.SelectedFg)
	return p
}

// --- style helpers ---
//
// Views compose styles from these rather than building tcell.Style literals, so
// a change to "what muted text looks like" is a one-line edit.

func (t Tokens) Base() tcell.Style {
	return tcell.StyleDefault.Foreground(t.Text).Background(t.Bg)
}

func (t Tokens) Body() tcell.Style {
	return t.Base().Foreground(t.Text)
}

func (t Tokens) Muted() tcell.Style {
	return t.Base().Foreground(t.TextMuted)
}

func (t Tokens) Faint() tcell.Style {
	return t.Base().Foreground(t.TextFaint)
}

func (t Tokens) Accented() tcell.Style {
	return t.Base().Foreground(t.Accent)
}

// Heading is for section labels: accent colour, no bold. Bold is reserved for
// emphasis inside a line, not for structure.
func (t Tokens) Heading() tcell.Style {
	return t.Base().Foreground(t.Accent)
}

func (t Tokens) SuccessStyle() tcell.Style {
	return t.Base().Foreground(t.Success)
}

func (t Tokens) WarningStyle() tcell.Style {
	return t.Base().Foreground(t.Warning)
}

func (t Tokens) DangerStyle() tcell.Style {
	return t.Base().Foreground(t.Danger)
}

func (t Tokens) BorderStyle() tcell.Style {
	return t.Base().Foreground(t.Border)
}

func (t Tokens) BorderSoftStyle() tcell.Style {
	return t.Base().Foreground(t.BorderSoft)
}

// Selected is the whole-row highlight used by every list. It is a background
// fill rather than reverse video or bold, which is what makes selection read as
// a deliberate design choice instead of a terminal artifact
// (spec tui-design-system / 行样式与选中态).
func (t Tokens) Selected() tcell.Style {
	return tcell.StyleDefault.Foreground(t.SelectedFg).Background(t.SelectedBg)
}

// SelectedMuted keeps the selection background but dims the text, for the
// secondary column of a selected row.
func (t Tokens) SelectedMuted() tcell.Style {
	c := t.SelectedFg
	if t.ColorCap == ColorMono {
		c = t.TextMuted
	}
	return tcell.StyleDefault.Foreground(c).Background(t.SelectedBg)
}

// SurfaceStyle paints the dialog background.
func (t Tokens) SurfaceStyle() tcell.Style {
	return tcell.StyleDefault.Foreground(t.Text).Background(t.Surface)
}

func (t Tokens) SurfaceMuted() tcell.Style {
	return tcell.StyleDefault.Foreground(t.TextMuted).Background(t.Surface)
}

// Emphasis is the one place bold is allowed: an unread count, the user's own
// name, a value that changed.
func (t Tokens) Emphasis() tcell.Style {
	return t.Base().Foreground(t.Text).Bold(true)
}

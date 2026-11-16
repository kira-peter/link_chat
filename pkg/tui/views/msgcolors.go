package views

import (
	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/i18n"
	"link-chat/pkg/tui/theme"
)

// Message colours (spec tui-settings / 消息样式).
//
// The user picks a message's text colour and border colour per side from a short
// named list. "default" is what the theme says — body text, and the theme's own
// border colour for that side — so a config without these settings looks as it
// always did. The named colours are mid-bright tones that read on both the dark
// and the light theme. A monochrome terminal has nothing to show them with and
// keeps the theme's greys.

// MessageColors are the colour choices, in the order they are offered.
var MessageColors = []string{"default", "cyan", "blue", "green", "amber", "rose", "violet"}

var messageColorHex = map[string]int32{
	"cyan":   0x3fb8c8,
	"blue":   0x5a8de0,
	"green":  0x5bb873,
	"amber":  0xd9a23a,
	"rose":   0xe06a8a,
	"violet": 0x9a78d8,
}

// MessageColorName is how a colour choice reads on the page.
func MessageColorName(name string, lang i18n.Lang) string {
	switch name {
	case "cyan":
		return i18n.ColorCyan.Get(lang)
	case "blue":
		return i18n.ColorBlue.Get(lang)
	case "green":
		return i18n.ColorGreen.Get(lang)
	case "amber":
		return i18n.ColorAmber.Get(lang)
	case "rose":
		return i18n.ColorRose.Get(lang)
	case "violet":
		return i18n.ColorViolet.Get(lang)
	}
	return i18n.ColorDefault.Get(lang)
}

// ValidMessageColor reports whether name is one of the choices.
func ValidMessageColor(name string) bool {
	for _, c := range MessageColors {
		if c == name {
			return true
		}
	}
	return false
}

// messageColor resolves a colour choice to a colour on this terminal, or def for
// "default", an unknown name, or a monochrome terminal.
func messageColor(t theme.Tokens, name string, def tcell.Color) tcell.Color {
	hex, ok := messageColorHex[name]
	if !ok || t.ColorCap == theme.ColorMono {
		return def
	}
	return tcell.NewHexColor(hex)
}

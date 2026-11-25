package views

import (
	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/i18n"
	"link-chat/pkg/protocol"
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
var MessageColors = protocol.SettingColors

var messageColorHex = map[string]int32{
	"red":    0xe05a5a,
	"orange": 0xe8853a,
	"yellow": 0xd8c23a,
	"amber":  0xd9a23a,
	"lime":   0x9ac43c,
	"green":  0x5bb873,
	"teal":   0x2fa89a,
	"cyan":   0x3fb8c8,
	"sky":    0x5cb4ec,
	"blue":   0x5a8de0,
	"violet": 0x9a78d8,
	"pink":   0xe88ac8,
	"rose":   0xe06a8a,
	"gray":   0x9aa0aa,
}

// MessageColorValue is a colour choice's colour on this terminal, or 0 for
// "default" or when the terminal has no colour to show it with.
func MessageColorValue(t theme.Tokens, name string) tcell.Color {
	return messageColor(t, name, 0)
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
	case "red":
		return i18n.ColorRed.Get(lang)
	case "orange":
		return i18n.ColorOrange.Get(lang)
	case "yellow":
		return i18n.ColorYellow.Get(lang)
	case "lime":
		return i18n.ColorLime.Get(lang)
	case "teal":
		return i18n.ColorTeal.Get(lang)
	case "sky":
		return i18n.ColorSky.Get(lang)
	case "pink":
		return i18n.ColorPink.Get(lang)
	case "gray":
		return i18n.ColorGray.Get(lang)
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

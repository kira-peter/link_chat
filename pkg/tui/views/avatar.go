package views

import (
	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/protocol"
	"link-chat/pkg/tui/layout"
	"link-chat/pkg/tui/theme"
	"link-chat/pkg/tui/widget"
)

// AvatarCells is how wide an avatar is drawn: one wide glyph, or a narrow one
// and a space, so names after it line up (spec tui-chat-client / 头像显示).
const AvatarCells = 2

// avatarText pads a one-cell avatar to AvatarCells.
func avatarText(avatar string) string {
	if avatar == "" {
		avatar = "?"
	}
	if layout.StringWidth(avatar) < AvatarCells {
		return avatar + " "
	}
	return avatar
}

// AvatarStyle is the style an avatar is drawn in: its colour on bg, bold. A
// monochrome terminal keeps the text colour.
func AvatarStyle(t theme.Tokens, color string, bg tcell.Style) tcell.Style {
	_, back, _ := bg.Decompose()
	return tcell.StyleDefault.Background(back).Foreground(messageColor(t, color, t.Text)).Bold(true)
}

// DrawAvatar draws an avatar at x, y on bg's background.
func DrawAvatar(c *widget.Canvas, t theme.Tokens, x, y int, avatar, color string, bg tcell.Style) {
	c.Text(x, y, avatarText(avatar), AvatarStyle(t, color, bg))
}

// drawUserAvatar is DrawAvatar for a user.
func drawUserAvatar(c *widget.Canvas, t theme.Tokens, x, y int, u protocol.User, bg tcell.Style) {
	avatar, color := u.Avatar, u.Color
	if avatar == "" {
		avatar, color = protocol.DefaultAvatar(u.Username)
	}
	DrawAvatar(c, t, x, y, avatar, color, bg)
}

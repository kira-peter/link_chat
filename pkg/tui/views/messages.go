package views

import (
	"link-chat/pkg/i18n"
	"link-chat/pkg/protocol"
	"link-chat/pkg/tui/layout"
)

// Message layout.
//
// A conversation is laid out in two steps. LayoutMessages turns messages into a
// list of Items — bubbles, time lines, system lines and blank rows — each with a
// known height and, for bubbles, a known width and side. It is a pure function of
// its arguments: no screen, no theme, no clock. Drawing then walks the Items.
//
// Keeping the two apart is what makes this testable without a terminal and what
// lets the scroll maths stay in whole lines: the total height is just the sum of
// the Items' heights (design.md decision 5).

// ItemKind says what an Item is.
type ItemKind int

const (
	// ItemBubble is one message in a bordered box.
	ItemBubble ItemKind = iota
	// ItemName is the author's name above a group of someone else's messages.
	ItemName
	// ItemTime is a centred line of time between messages.
	ItemTime
	// ItemSystem is a centred, dimmed line for a join or leave.
	ItemSystem
	// ItemDeleted is the dimmed placeholder where a message used to be.
	ItemDeleted
	// ItemBlank is an empty row of breathing space.
	ItemBlank
)

// Side is which edge of the conversation a bubble hugs.
type Side int

const (
	SideLeft Side = iota
	SideRight
)

// Item is one thing in the conversation, with its geometry worked out.
type Item struct {
	Kind ItemKind
	// Side is which edge a bubble or name sits against. Unused for centred items.
	Side Side
	// X is the item's left edge relative to the conversation area; W its width.
	X, W int
	// Lines is the text. For a bubble it is the wrapped body, one entry per
	// interior row; for a time, system or deleted line it holds the one line.
	Lines []string
	// Own is true for the current user's messages.
	Own bool
	// Boxed is true when a bubble is drawn with a border. A very narrow area drops
	// the border and shows the text alone: the border and padding would take too
	// much of each line to be worth it. Recording it here, rather than letting the
	// drawing code guess it back from the widths, keeps the two from disagreeing.
	Boxed bool
	// Footer is the small text drawn on a bubble's bottom border: the time, and
	// "edited" when the message has been edited. Empty if none.
	Footer string
	// TextColor and BorderColor are the colour choices the bubble is drawn with
	// (see MessageColors); "" or "default" is the theme's.
	TextColor, BorderColor string
	// Avatar and AvatarColor are drawn before an ItemName's text, in the
	// avatar's colour (spec tui-chat-client / 头像显示).
	Avatar, AvatarColor string
}

// Height is how many rows the item occupies. A bordered bubble adds a top and a
// bottom border row; an unboxed one is just its text.
func (it Item) Height() int {
	switch it.Kind {
	case ItemBubble:
		if it.Boxed {
			return len(it.Lines) + 2
		}
		return len(it.Lines)
	default:
		return 1
	}
}

// Layout constants.
const (
	// bubblePad is the empty space between a bubble's border and its text on each
	// side.
	bubblePad = 1
	// bubbleBorder is the width a border takes on each side.
	bubbleBorder = 1
	// bubbleChrome is everything a bubble adds around its text, both sides.
	bubbleChrome = 2 * (bubbleBorder + bubblePad)
	// minBubbleText is the narrowest text column a bubble will wrap to.
	minBubbleText = 8
	// boxlessBelow is the conversation width under which bubbles drop their border:
	// at that width the border and padding eat too much of the line to be worth it.
	boxlessBelow = 28
	// TimeGap is how long a silence must last before a time line is shown.
	TimeGap = 5 * 60 * 1000
)

// Layout is the user's chat settings that shape how messages are laid out
// (spec tui-settings / 聊天类设置的效果). The zero value is not valid; use
// DefaultLayout, which is how the client looked before these were settings.
type Layout struct {
	// BubblePercent caps a bubble's width as a share of the conversation's, so a
	// long message wraps inside the bubble instead of running the full width and
	// losing the left/right distinction.
	BubblePercent int
	// Compact drops the blank row between groups from different speakers. The
	// blank rows around a time line stay: they are part of what makes it readable.
	Compact bool
	// Clock24 shows times on a 24-hour clock; otherwise a 12-hour one.
	Clock24 bool

	// NoBorder draws every message without its border: the text alone, coloured
	// by side, as a very narrow area always does.
	NoBorder bool
	// NoTime leaves the time off each message's bottom border. The time lines
	// between groups stay: they are what dates the conversation.
	NoTime bool
	// SelfText, OtherText, SelfBorder and OtherBorder are the colour choices for
	// each side's text and border (see MessageColors); "" is the default.
	SelfText, OtherText     string
	SelfBorder, OtherBorder string
}

// DefaultLayout is the layout with no settings changed.
var DefaultLayout = Layout{BubblePercent: 70, Clock24: true}

// LayoutMessages arranges msgs with the default layout. See LayoutMessagesWith.
func LayoutMessages(msgs []protocol.Message, width int, userID string, now int64, lang i18n.Lang) []Item {
	return LayoutMessagesWith(msgs, width, userID, now, lang, DefaultLayout)
}

// LayoutMessagesWith arranges msgs for a conversation area width cells wide.
//
// Grouping follows the established rule: a run of one author's messages within
// MergeWindow of each other is a group, and only the first message of a group
// carries the name. Here a group is simply consecutive bubbles on the same side
// with nothing between them.
//
// now decides whether a time line shows the day: a message from today shows the
// clock only, an older one shows the date as well.
func LayoutMessagesWith(msgs []protocol.Message, width int, userID string, now int64, lang i18n.Lang, lay Layout) []Item {
	return layoutMessages(msgs, width, userID, now, lang, lay, "")
}

func layoutMessages(msgs []protocol.Message, width int, userID string, now int64, lang i18n.Lang, lay Layout, creatorID string) []Item {
	var out []Item
	if width <= 0 {
		return out
	}
	boxed := width >= boxlessBelow && !lay.NoBorder

	var prev *protocol.Message
	for i := range msgs {
		msg := &msgs[i]

		// Membership events are system lines, centred and without a box, so they
		// cannot be mistaken for conversation.
		if isSystemMessage(msg) {
			out = append(out, systemItem(msg, width, lang))
			prev = nil
			continue
		}

		// A time line where a timestamp actually carries information: the first
		// message, or after a long silence.
		if prev == nil || msg.CreatedAt-prev.CreatedAt > TimeGap {
			if len(out) > 0 {
				out = append(out, Item{Kind: ItemBlank})
			}
			out = append(out, timeItem(msg.CreatedAt, now, width, lay, lang))
			out = append(out, Item{Kind: ItemBlank})
		}

		if msg.Deleted {
			text := i18n.MessageDeleted.Get(lang)
			out = append(out, Item{Kind: ItemDeleted, Lines: []string{text}, X: 0, W: layout.StringWidth(text)})
			prev = nil
			continue
		}

		own := msg.AuthorID == userID
		side := SideLeft
		if own {
			side = SideRight
		}

		grouped := prev != nil &&
			prev.AuthorID == msg.AuthorID &&
			msg.CreatedAt-prev.CreatedAt <= MergeWindow &&
			!prev.Deleted

		if !grouped {
			// Breathing room between groups, but not straight after a time line,
			// which already brought its own, and not at all when compact.
			if !lay.Compact && prev != nil && lastKind(out) != ItemBlank {
				out = append(out, Item{Kind: ItemBlank})
			}
			creator := creatorID != "" && msg.AuthorID == creatorID
			if !own || creator {
				name := msg.AuthorName
				if name == "" {
					name = "?"
				}
				if creator {
					name = "★ " + name
				}
				it := nameItem(name, width, boxed)
				if msg.AuthorAvatar != "" {
					it = withAvatar(it, msg.AuthorAvatar, msg.AuthorColor, width)
				}
				if own {
					it.Side = SideRight
					it.X = width - it.X - it.W
				}
				out = append(out, it)
			}
		}

		out = append(out, bubbleItem(msg, side, own, width, boxed, lang, lay))
		prev = msg
	}
	return out
}

// lastKind is the kind of the last item, or ItemBlank for an empty list so that
// "is there a gap above" reads naturally at the very start.
func lastKind(items []Item) ItemKind {
	if len(items) == 0 {
		return ItemBlank
	}
	return items[len(items)-1].Kind
}

// TotalHeight is the number of rows the items occupy.
func TotalHeight(items []Item) int {
	n := 0
	for _, it := range items {
		n += it.Height()
	}
	return n
}

// maxTextWidth is the widest a bubble's text may be at a conversation width.
func maxTextWidth(width int, boxed bool, percent int) int {
	chrome := bubbleChrome
	if !boxed {
		chrome = 0
	}
	limit := width*percent/100 - chrome
	if limit < minBubbleText {
		limit = minBubbleText
	}
	// Never wider than the area itself.
	if room := width - chrome; limit > room {
		limit = room
	}
	if limit < 1 {
		limit = 1
	}
	return limit
}

// bubbleItem lays out one message.
func bubbleItem(msg *protocol.Message, side Side, own bool, width int, boxed bool, lang i18n.Lang, lay Layout) Item {
	textMax := maxTextWidth(width, boxed, lay.BubblePercent)
	lines := layout.Wrap(msg.Body, textMax)
	if len(lines) == 0 {
		lines = []string{""}
	}

	// The bubble is as wide as its longest line, so a short message gets a short
	// bubble.
	textW := 0
	for _, ln := range lines {
		if w := layout.StringWidth(ln); w > textW {
			textW = w
		}
	}

	footer := ""
	if !lay.NoTime {
		footer = i18n.Clock(msg.CreatedAt, lay.Clock24, lang)
	}
	if msg.EditedAt > 0 {
		if footer != "" {
			footer += " · "
		}
		footer += i18n.MessageEdited.Get(lang)
	}
	// The footer sits on the bottom border and must fit inside it, so the bubble is
	// at least as wide as the footer needs.
	if boxed && footer != "" {
		if fw := layout.StringWidth(footer) + 2; fw > textW+2*bubblePad {
			textW = fw - 2*bubblePad
		}
	}

	w := textW
	if boxed {
		w = textW + bubbleChrome
	}
	x := 0
	if side == SideRight {
		x = width - w
		if x < 0 {
			x = 0
		}
	}

	it := Item{Kind: ItemBubble, Side: side, X: x, W: w, Lines: lines, Own: own, Boxed: boxed,
		TextColor: lay.OtherText, BorderColor: lay.OtherBorder}
	if own {
		it.TextColor, it.BorderColor = lay.SelfText, lay.SelfBorder
	}
	if boxed {
		it.Footer = footer
	}
	return it
}

// nameItem is the author's name above someone else's group. It aligns with the
// bubble's left edge, inside the border, so it reads as the bubble's heading.
func nameItem(name string, width int, boxed bool) Item {
	x := 0
	if boxed {
		x = bubbleBorder + bubblePad
	}
	avail := width - x
	if avail < 1 {
		avail = 1
	}
	text := layout.Truncate(name, avail)
	return Item{Kind: ItemName, Side: SideLeft, X: x, W: layout.StringWidth(text), Lines: []string{text}}
}

// withAvatar puts a two-cell avatar and a space before a name item's text.
func withAvatar(it Item, avatar, color string, width int) Item {
	avail := width - it.X - AvatarCells - 1
	if avail < 1 {
		return it
	}
	it.Avatar, it.AvatarColor = avatar, color
	it.Lines[0] = layout.Truncate(it.Lines[0], avail)
	it.W = AvatarCells + 1 + layout.StringWidth(it.Lines[0])
	return it
}

// timeItem is a centred time line.
func timeItem(at, now int64, width int, lay Layout, lang i18n.Lang) Item {
	label := i18n.Clock(at, lay.Clock24, lang)
	if !sameDayLabel(at, now) {
		label = i18n.DateClock(at, lay.Clock24, lang)
	}
	label = layout.Truncate(label, width)
	w := layout.StringWidth(label)
	return Item{Kind: ItemTime, X: centreX(width, w), W: w, Lines: []string{label}}
}

// systemItem is a centred, dimmed line for a join or leave.
func systemItem(msg *protocol.Message, width int, lang i18n.Lang) Item {
	what := msg.Body
	switch msg.Body {
	case "joined":
		what = i18n.JoinedChannel.Get(lang)
	case "left":
		what = i18n.LeftChannel.Get(lang)
	}
	line := layout.Truncate("· "+msg.AuthorName+" "+what, width)
	w := layout.StringWidth(line)
	return Item{Kind: ItemSystem, X: centreX(width, w), W: w, Lines: []string{line}}
}

// centreX is the left edge that centres something w cells wide in width cells.
func centreX(width, w int) int {
	if x := (width - w) / 2; x > 0 {
		return x
	}
	return 0
}

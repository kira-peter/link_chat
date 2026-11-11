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
	// bubbleMaxPercent caps a bubble's width as a share of the conversation's, so a
	// long message wraps inside the bubble instead of running the full width and
	// losing the left/right distinction.
	bubbleMaxPercent = 70
	// minBubbleText is the narrowest text column a bubble will wrap to.
	minBubbleText = 8
	// boxlessBelow is the conversation width under which bubbles drop their border:
	// at that width the border and padding eat too much of the line to be worth it.
	boxlessBelow = 28
	// TimeGap is how long a silence must last before a time line is shown.
	TimeGap = 5 * 60 * 1000
)

// LayoutMessages arranges msgs for a conversation area width cells wide.
//
// Grouping follows the established rule: a run of one author's messages within
// MergeWindow of each other is a group, and only the first message of a group
// carries the name. Here a group is simply consecutive bubbles on the same side
// with nothing between them.
//
// now decides whether a time line shows the day: a message from today shows the
// clock only, an older one shows the date as well.
func LayoutMessages(msgs []protocol.Message, width int, userID string, now int64, lang i18n.Lang) []Item {
	var out []Item
	if width <= 0 {
		return out
	}
	boxed := width >= boxlessBelow

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
			out = append(out, timeItem(msg.CreatedAt, now, width))
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
			// which already brought its own.
			if prev != nil && lastKind(out) != ItemBlank {
				out = append(out, Item{Kind: ItemBlank})
			}
			// Someone else's group opens with their name; your own never does.
			if !own {
				name := msg.AuthorName
				if name == "" {
					name = "?"
				}
				out = append(out, nameItem(name, width, boxed))
			}
		}

		out = append(out, bubbleItem(msg, side, own, width, boxed, lang))
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
func maxTextWidth(width int, boxed bool) int {
	chrome := bubbleChrome
	if !boxed {
		chrome = 0
	}
	limit := width*bubbleMaxPercent/100 - chrome
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
func bubbleItem(msg *protocol.Message, side Side, own bool, width int, boxed bool, lang i18n.Lang) Item {
	textMax := maxTextWidth(width, boxed)
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

	footer := i18n.ClockOnly(msg.CreatedAt)
	if msg.EditedAt > 0 {
		footer += " · " + i18n.MessageEdited.Get(lang)
	}
	// The footer sits on the bottom border and must fit inside it, so the bubble is
	// at least as wide as the footer needs.
	if boxed {
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

	it := Item{Kind: ItemBubble, Side: side, X: x, W: w, Lines: lines, Own: own, Boxed: boxed}
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

// timeItem is a centred time line.
func timeItem(at, now int64, width int) Item {
	label := i18n.ClockOnly(at)
	if !sameDayLabel(at, now) {
		label = i18n.FullTime(at)
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

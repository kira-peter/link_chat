package views

import (
	"strings"
	"testing"

	"link-chat/pkg/i18n"
	"link-chat/pkg/protocol"
)

// The default layout is exactly the old fixed one: same items, same geometry.
func TestDefaultLayoutIsThePreviousLayout(t *testing.T) {
	msgs := []protocol.Message{
		msg("1", "bob", "bob", strings.Repeat("一段很长的话", 20), at(10, 0, 0)),
		msg("2", me, "me", "好", at(10, 1, 0)),
		msg("3", "bob", "bob", "再说一句", at(10, 30, 0)),
	}
	a := LayoutMessages(msgs, 60, me, at(23, 0, 0), i18n.ZH)
	b := LayoutMessagesWith(msgs, 60, me, at(23, 0, 0), i18n.ZH, Layout{BubblePercent: 70, Clock24: true})
	if len(a) != len(b) {
		t.Fatalf("%d items vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i].Kind != b[i].Kind || a[i].X != b[i].X || a[i].W != b[i].W || a[i].Footer != b[i].Footer ||
			strings.Join(a[i].Lines, "|") != strings.Join(b[i].Lines, "|") {
			t.Fatalf("item %d differs: %+v vs %+v", i, a[i], b[i])
		}
	}
}

// The bubble cap follows the setting: a long message's bubble is never wider than
// that share of the area, and a wider setting gives a wider bubble.
func TestBubbleWidthCapFollowsTheSetting(t *testing.T) {
	const width = 80
	long := msg("1", "bob", "bob", strings.Repeat("长消息需要折行", 30), at(10, 0, 0))
	prev := 0
	for _, pct := range []int{60, 70, 80} {
		items := LayoutMessagesWith([]protocol.Message{long}, width, me, at(23, 0, 0), i18n.ZH,
			Layout{BubblePercent: pct, Clock24: true})
		bs := bubbles(items)
		if len(bs) != 1 {
			t.Fatalf("%d%%: %d bubbles", pct, len(bs))
		}
		if w := bs[0].W; w > width*pct/100 {
			t.Errorf("%d%%: the bubble is %d wide, over %d", pct, w, width*pct/100)
		}
		if bs[0].W <= prev {
			t.Errorf("%d%%: the bubble (%d) is not wider than at the smaller setting (%d)", pct, bs[0].W, prev)
		}
		prev = bs[0].W
	}
}

// Compact drops the blank row between two speakers' groups and nothing else: the
// blank rows around a time line stay.
func TestCompactDropsOnlyTheGapBetweenSpeakers(t *testing.T) {
	msgs := []protocol.Message{
		msg("1", "bob", "bob", "a", at(10, 0, 0)),
		msg("2", me, "me", "b", at(10, 1, 0)),
		msg("3", "bob", "bob", "c", at(10, 2, 0)),
		msg("4", me, "me", "d", at(10, 30, 0)), // after a long gap: a time line
	}
	comfy := LayoutMessagesWith(msgs, 60, me, at(23, 0, 0), i18n.ZH, Layout{BubblePercent: 70, Clock24: true})
	compact := LayoutMessagesWith(msgs, 60, me, at(23, 0, 0), i18n.ZH, Layout{BubblePercent: 70, Clock24: true, Compact: true})

	// Two speaker changes without a time line between them: two fewer blanks.
	if got := countKind(comfy, ItemBlank) - countKind(compact, ItemBlank); got != 2 {
		t.Fatalf("compact removed %d blank rows, want 2 (one per change of speaker)", got)
	}
	// Each time line still has a blank row on both sides where it can.
	for i, it := range compact {
		if it.Kind != ItemTime {
			continue
		}
		if i+1 >= len(compact) || compact[i+1].Kind != ItemBlank {
			t.Errorf("compact: no blank row after the time line at %d", i)
		}
		if i > 0 && compact[i-1].Kind != ItemBlank {
			t.Errorf("compact: no blank row before the time line at %d", i)
		}
	}
	// And nothing else changed: same bubbles, same order.
	if len(bubbles(comfy)) != len(bubbles(compact)) {
		t.Fatal("compact changed the bubbles")
	}
}

// The 12-hour clock reaches both the bubble footers and the time lines.
func TestTwelveHourClockInFootersAndTimeLines(t *testing.T) {
	msgs := []protocol.Message{msg("1", "bob", "bob", "晚上好", at(20, 26, 0))}
	items := LayoutMessagesWith(msgs, 60, me, at(23, 0, 0), i18n.ZH, Layout{BubblePercent: 70, Clock24: false})
	var footer, line string
	for _, it := range items {
		switch it.Kind {
		case ItemBubble:
			footer = it.Footer
		case ItemTime:
			line = it.Lines[0]
		}
	}
	if footer != "下午 8:26" {
		t.Errorf("footer %q, want 下午 8:26", footer)
	}
	if line != "下午 8:26" {
		t.Errorf("time line %q, want 下午 8:26", line)
	}

	en := LayoutMessagesWith(msgs, 60, me, at(23, 0, 0), i18n.EN, Layout{BubblePercent: 70, Clock24: false})
	if b := bubbles(en); len(b) != 1 || b[0].Footer != "8:26 PM" {
		t.Errorf("English 12-hour footer %+v, want 8:26 PM", b)
	}
}

// The transcript's zero Layout means the defaults, so callers that never heard of
// settings draw exactly as before.
func TestTranscriptZeroLayoutIsTheDefault(t *testing.T) {
	cv := twoSided()
	render := func(lay Layout) string {
		s := newScreen(t, 80, 30)
		Transcript{T: toks()}.Render(s, ContentRect(80, 30), TranscriptModel{
			Conversation: cv, UserID: me, Now: at(23, 0, 0), Layout: lay,
		}, i18n.ZH)
		return screenString(s)
	}
	if render(Layout{}) != render(DefaultLayout) {
		t.Fatal("a zero Layout renders differently from the default")
	}
	if render(DefaultLayout) == render(Layout{BubblePercent: 70, Clock24: false}) {
		t.Fatal("the 12-hour setting did not reach the transcript")
	}
}

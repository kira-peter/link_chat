package views

import (
	"strings"
	"testing"
	"time"

	"link-chat/pkg/i18n"
	"link-chat/pkg/protocol"
	"link-chat/pkg/tui/layout"
)

const me = "me"

// at builds a timestamp on a fixed day, so a test can place messages minutes apart
// without depending on when it runs.
func at(h, m, s int) int64 {
	return time.Date(2026, 11, 10, h, m, s, 0, time.Local).UnixMilli()
}

func msg(id, author, name, body string, ts int64) protocol.Message {
	return protocol.Message{ID: id, AuthorID: author, AuthorName: name, Body: body, CreatedAt: ts}
}

func layoutOf(msgs ...protocol.Message) []Item {
	return LayoutMessages(msgs, 60, me, at(23, 0, 0), i18n.ZH)
}

// kinds lists the item kinds, for readable assertions about structure.
func kinds(items []Item) []ItemKind {
	out := make([]ItemKind, len(items))
	for i, it := range items {
		out[i] = it.Kind
	}
	return out
}

func bubbles(items []Item) []Item {
	var out []Item
	for _, it := range items {
		if it.Kind == ItemBubble {
			out = append(out, it)
		}
	}
	return out
}

func countKind(items []Item, k ItemKind) int {
	n := 0
	for _, it := range items {
		if it.Kind == k {
			n++
		}
	}
	return n
}

// ---------- 6.1 sides and widths ----------

// Other people's messages hug the left edge, the current user's the right.
func TestBubblesSitOnTheirOwnSides(t *testing.T) {
	const width = 60
	items := LayoutMessages([]protocol.Message{
		msg("1", "bob", "bob", "hi", at(10, 0, 0)),
		msg("2", me, "me", "hello", at(10, 0, 30)),
	}, width, me, at(23, 0, 0), i18n.ZH)

	bs := bubbles(items)
	if len(bs) != 2 {
		t.Fatalf("want 2 bubbles, got %d", len(bs))
	}
	if bs[0].Side != SideLeft || bs[0].X != 0 || bs[0].Own {
		t.Errorf("bob's bubble: side %v x %d own %v, want left edge and not own", bs[0].Side, bs[0].X, bs[0].Own)
	}
	if bs[1].Side != SideRight || !bs[1].Own {
		t.Errorf("my bubble: side %v own %v, want right and own", bs[1].Side, bs[1].Own)
	}
	if got := bs[1].X + bs[1].W; got != width {
		t.Errorf("my bubble ends at %d, want the right edge %d", got, width)
	}
}

// A short message gets a short bubble: its width follows the text, not a fixed
// share of the area.
func TestBubbleWidthFollowsTheContent(t *testing.T) {
	short := bubbles(layoutOf(msg("1", "bob", "bob", "ok", at(10, 0, 0))))[0]
	long := bubbles(layoutOf(msg("1", "bob", "bob", strings.Repeat("a", 30), at(10, 0, 0))))[0]

	if short.W >= long.W {
		t.Fatalf("a 2-character message (%d cells) is not narrower than a 30-character one (%d)", short.W, long.W)
	}
	// The footer (the time) sits on the bottom border and sets a floor, but a
	// two-character body must not produce a wide bubble.
	if short.W > 14 {
		t.Fatalf("a two-character message produced a %d-cell bubble", short.W)
	}
}

// No bubble exceeds 70% of the area, however long the message; the text wraps
// inside it instead. The 70 is written out here rather than read from the
// constant: a test that computes its limit from the value under test can never
// disagree with it, so changing the constant would change the test along with it.
func TestLongMessagesWrapInsideTheCap(t *testing.T) {
	for _, width := range []int{40, 60, 100, 140} {
		body := strings.Repeat("word ", 80)
		items := LayoutMessages([]protocol.Message{msg("1", "bob", "bob", body, at(10, 0, 0))}, width, me, at(23, 0, 0), i18n.ZH)
		b := bubbles(items)[0]

		if limit := width * 70 / 100; b.W > limit {
			t.Errorf("width %d: bubble is %d cells, over the 70%% cap (%d)", width, b.W, limit)
		}
		if len(b.Lines) < 2 {
			t.Errorf("width %d: a long message did not wrap (%d lines)", width, len(b.Lines))
		}
		for i, ln := range b.Lines {
			if w := layout.StringWidth(ln); w > b.W-bubbleChrome {
				t.Errorf("width %d line %d: %d cells wide, text column is %d", width, i, w, b.W-bubbleChrome)
			}
		}
	}
}

// Wrapping is by display width: CJK and emoji are never split, and no line is wider
// than the text column.
func TestWrapIsByDisplayWidthForCJKAndEmoji(t *testing.T) {
	body := strings.Repeat("你好世界👍", 12)
	items := LayoutMessages([]protocol.Message{msg("1", "bob", "bob", body, at(10, 0, 0))}, 40, me, at(23, 0, 0), i18n.ZH)
	b := bubbles(items)[0]

	textCol := b.W - bubbleChrome
	var rebuilt strings.Builder
	for i, ln := range b.Lines {
		if w := layout.StringWidth(ln); w > textCol {
			t.Fatalf("line %d is %d cells, wider than the %d-cell text column: %q", i, w, textCol, ln)
		}
		rebuilt.WriteString(ln)
	}
	if rebuilt.String() != body {
		t.Fatalf("wrapping lost or reordered characters:\n got %q\nwant %q", rebuilt.String(), body)
	}
}

// An empty body still produces a bubble with one blank interior row.
func TestEmptyBodyStillHasAnInterior(t *testing.T) {
	b := bubbles(layoutOf(msg("1", "bob", "bob", "", at(10, 0, 0))))
	if len(b) != 1 {
		t.Fatalf("an empty message laid out as %d bubbles, want 1", len(b))
	}
	if len(b[0].Lines) != 1 || b[0].Height() != 3 {
		t.Fatalf("an empty bubble has %d lines and height %d, want 1 line and height 3", len(b[0].Lines), b[0].Height())
	}
}

// ---------- 6.2 grouping and names ----------

// Someone else's group opens with their name above the bubble; your own never does.
func TestOnlyOthersGroupsCarryAName(t *testing.T) {
	items := layoutOf(
		msg("1", "bob", "bob", "hi", at(10, 0, 0)),
		msg("2", me, "me", "hello", at(10, 0, 30)),
	)
	names := 0
	var shown string
	for _, it := range items {
		if it.Kind == ItemName {
			names++
			shown = it.Lines[0]
		}
	}
	if names != 1 || shown != "bob" {
		t.Fatalf("want exactly one name line (bob), got %d (%q)", names, shown)
	}
}

// Three messages from one person inside the window: one name, three bubbles, all on
// one side and back to back.
func TestConsecutiveMessagesShareOneNameAndStayTogether(t *testing.T) {
	items := layoutOf(
		msg("1", "bob", "bob", "one", at(10, 0, 0)),
		msg("2", "bob", "bob", "two", at(10, 0, 40)),
		msg("3", "bob", "bob", "three", at(10, 1, 20)),
	)
	if got := countKind(items, ItemName); got != 1 {
		t.Fatalf("a run of one author's messages showed %d names, want 1", got)
	}
	bs := bubbles(items)
	if len(bs) != 3 {
		t.Fatalf("want 3 bubbles, got %d", len(bs))
	}
	for i, b := range bs {
		if b.Side != SideLeft || b.X != 0 {
			t.Errorf("bubble %d left its side: side %v x %d", i, b.Side, b.X)
		}
	}
	// Back to back: no blank row between two bubbles of the group.
	for i := 0; i+2 < len(items); i++ {
		if items[i].Kind == ItemBubble && items[i+1].Kind == ItemBlank && items[i+2].Kind == ItemBubble {
			t.Errorf("a gap opened inside a group after item %d", i)
		}
	}
}

// The merge window is exact: two minutes apart still groups, a second more does not.
func TestMergeWindowBoundary(t *testing.T) {
	inside := layoutOf(
		msg("1", "bob", "bob", "a", at(10, 0, 0)),
		msg("2", "bob", "bob", "b", at(10, 2, 0)), // exactly MergeWindow
	)
	outside := layoutOf(
		msg("1", "bob", "bob", "a", at(10, 0, 0)),
		msg("2", "bob", "bob", "b", at(10, 2, 1)), // one second over
	)
	if got := countKind(inside, ItemName); got != 1 {
		t.Errorf("messages exactly MergeWindow apart showed %d names, want 1 (grouped)", got)
	}
	if got := countKind(outside, ItemName); got != 2 {
		t.Errorf("messages a second past MergeWindow showed %d names, want 2 (separate groups)", got)
	}
}

// Another speaker starts a new group, and the sides alternate.
func TestAnotherSpeakerStartsANewGroup(t *testing.T) {
	items := layoutOf(
		msg("1", "bob", "bob", "a", at(10, 0, 0)),
		msg("2", me, "me", "b", at(10, 0, 20)),
		msg("3", "bob", "bob", "c", at(10, 0, 40)),
	)
	if got := countKind(items, ItemName); got != 2 {
		t.Fatalf("bob spoke twice around me, so two groups: got %d names", got)
	}
	bs := bubbles(items)
	if len(bs) != 3 || bs[0].Side != SideLeft || bs[1].Side != SideRight || bs[2].Side != SideLeft {
		t.Fatalf("sides should alternate left/right/left, got %+v", bs)
	}
}

// A missing author name does not leave a blank heading.
func TestMissingAuthorNameShowsAPlaceholder(t *testing.T) {
	for _, it := range layoutOf(msg("1", "bob", "", "hi", at(10, 0, 0))) {
		if it.Kind == ItemName && it.Lines[0] == "" {
			t.Fatal("a name line is blank")
		}
	}
}

// ---------- 6.3 time lines ----------

// A time line appears at the start and after a long silence, centred, with a blank
// row on each side and no rule characters.
func TestTimeLineIsCentredWithSpaceAndNoRule(t *testing.T) {
	const width = 60
	items := LayoutMessages([]protocol.Message{
		msg("1", "bob", "bob", "a", at(10, 0, 0)),
		msg("2", "bob", "bob", "b", at(10, 30, 0)), // half an hour later
	}, width, me, at(23, 0, 0), i18n.ZH)

	var times []int
	for i, it := range items {
		if it.Kind == ItemTime {
			times = append(times, i)
		}
	}
	if len(times) != 2 {
		t.Fatalf("want a time line at the start and after the gap, got %d: %v", len(times), kinds(items))
	}
	for _, i := range times {
		it := items[i]
		if want := (width - it.W) / 2; it.X != want {
			t.Errorf("time %q starts at %d, want centred at %d", it.Lines[0], it.X, want)
		}
		if strings.ContainsAny(it.Lines[0], "─-—═━") {
			t.Errorf("time line %q contains a rule character", it.Lines[0])
		}
		// A blank row below always; above too except at the very top of the list.
		if i+1 < len(items) && items[i+1].Kind != ItemBlank {
			t.Errorf("no blank row below the time line at item %d", i)
		}
		if i > 0 && items[i-1].Kind != ItemBlank {
			t.Errorf("no blank row above the time line at item %d", i)
		}
	}
}

// No time line inside a quick conversation, beyond the opening one.
func TestNoTimeLineWithinAShortExchange(t *testing.T) {
	items := layoutOf(
		msg("1", "bob", "bob", "a", at(10, 0, 0)),
		msg("2", me, "me", "b", at(10, 1, 0)),
		msg("3", "bob", "bob", "c", at(10, 2, 0)),
	)
	if got := countKind(items, ItemTime); got != 1 {
		t.Fatalf("a two-minute exchange showed %d time lines, want only the opening one", got)
	}
}

// The gap is exact: five minutes still no line, a second more gives one.
func TestTimeGapBoundary(t *testing.T) {
	count := func(second int) int {
		return countKind(layoutOf(
			msg("1", "bob", "bob", "a", at(10, 0, 0)),
			msg("2", "bob", "bob", "b", at(10, 5, second)),
		), ItemTime)
	}
	if got := count(0); got != 1 {
		t.Errorf("exactly TimeGap apart gave %d time lines, want 1", got)
	}
	if got := count(1); got != 2 {
		t.Errorf("a second past TimeGap gave %d time lines, want 2", got)
	}
}

// Same day shows the clock only; an earlier day shows the date too.
func TestTimeLineFormatSameDayAndEarlier(t *testing.T) {
	now := at(23, 0, 0)
	today := LayoutMessages([]protocol.Message{msg("1", "bob", "bob", "a", at(9, 5, 0))}, 60, me, now, i18n.ZH)
	older := LayoutMessages([]protocol.Message{
		msg("1", "bob", "bob", "a", time.Date(2026, 10, 1, 9, 5, 0, 0, time.Local).UnixMilli()),
	}, 60, me, now, i18n.ZH)

	first := func(items []Item) string {
		for _, it := range items {
			if it.Kind == ItemTime {
				return it.Lines[0]
			}
		}
		return ""
	}
	clock := i18n.ClockOnly(at(9, 5, 0))
	if got := first(today); got != clock {
		t.Errorf("today's time line is %q, want the clock only (%q)", got, clock)
	}
	if got := first(older); len(got) <= len(clock) || !strings.HasSuffix(got, clock) {
		t.Errorf("an older message's time line is %q, want a date followed by %q", got, clock)
	}
}

// ---------- 6.4 system lines and deleted messages ----------

func TestSystemEventIsCentredAndUnboxed(t *testing.T) {
	items := layoutOf(msg("1", "bob", "bob", "joined", at(10, 0, 0)))
	var sys *Item
	for i := range items {
		if items[i].Kind == ItemSystem {
			sys = &items[i]
		}
	}
	if sys == nil {
		t.Fatalf("no system line: %v", kinds(items))
	}
	if want := (60 - sys.W) / 2; sys.X != want {
		t.Errorf("system line at %d, want centred at %d", sys.X, want)
	}
	if sys.Height() != 1 {
		t.Errorf("a system line is %d rows tall, want 1 (no box)", sys.Height())
	}
	if len(bubbles(items)) != 0 {
		t.Error("a membership event produced a bubble")
	}
}

func TestDeletedMessageIsAPlaceholderWithNoBox(t *testing.T) {
	items := layoutOf(protocol.Message{ID: "1", AuthorID: "bob", AuthorName: "bob", Deleted: true, CreatedAt: at(10, 0, 0)})
	var del *Item
	for i := range items {
		if items[i].Kind == ItemDeleted {
			del = &items[i]
		}
	}
	if del == nil {
		t.Fatalf("no placeholder: %v", kinds(items))
	}
	if !strings.Contains(del.Lines[0], i18n.MessageDeleted.Get(i18n.ZH)) {
		t.Errorf("placeholder text is %q", del.Lines[0])
	}
	if del.Height() != 1 || len(bubbles(items)) != 0 {
		t.Error("a deleted message is boxed")
	}
}

// A deleted message never exposes the old body, even if the field is still set.
func TestDeletedMessageHidesItsBody(t *testing.T) {
	items := layoutOf(protocol.Message{ID: "1", AuthorID: "bob", AuthorName: "bob", Body: "secret", Deleted: true, CreatedAt: at(10, 0, 0)})
	for _, it := range items {
		for _, ln := range it.Lines {
			if strings.Contains(ln, "secret") {
				t.Fatalf("a deleted message leaked its body: %q", ln)
			}
		}
	}
}

// A deleted message breaks a group: what follows starts afresh, with a name.
func TestAMessageAfterADeletedOneStartsAFreshGroup(t *testing.T) {
	items := layoutOf(
		protocol.Message{ID: "1", AuthorID: "bob", AuthorName: "bob", Deleted: true, CreatedAt: at(10, 0, 0)},
		msg("2", "bob", "bob", "after", at(10, 0, 20)),
	)
	if got := countKind(items, ItemName); got != 1 {
		t.Fatalf("want a name above the message after the deleted one, got %d names", got)
	}
}

// ---------- heights and edge cases ----------

func TestHeightsAddUp(t *testing.T) {
	items := layoutOf(
		msg("1", "bob", "bob", "short", at(10, 0, 0)),
		msg("2", me, "me", strings.Repeat("word ", 30), at(10, 0, 30)),
	)
	sum := 0
	for _, it := range items {
		h := it.Height()
		if h < 1 {
			t.Fatalf("item %v has height %d", it.Kind, h)
		}
		if it.Kind == ItemBubble && h != len(it.Lines)+2 {
			t.Errorf("bubble height %d, want %d lines plus two borders", h, len(it.Lines))
		}
		sum += h
	}
	if got := TotalHeight(items); got != sum {
		t.Fatalf("TotalHeight = %d, want the sum %d", got, sum)
	}
}

func TestLayoutHandlesDegenerateInput(t *testing.T) {
	if got := LayoutMessages(nil, 60, me, 0, i18n.ZH); len(got) != 0 {
		t.Errorf("no messages gave %d items", len(got))
	}
	if got := LayoutMessages([]protocol.Message{msg("1", "bob", "bob", "x", 1)}, 0, me, 0, i18n.ZH); len(got) != 0 {
		t.Errorf("zero width gave %d items", len(got))
	}
	// Very narrow: still sane, never a negative geometry.
	for _, w := range []int{1, 2, 3, 5, 8} {
		for _, it := range LayoutMessages([]protocol.Message{
			msg("1", "bob", "bob", "hello there", at(10, 0, 0)),
			msg("2", me, "me", "hi", at(10, 0, 5)),
		}, w, me, at(23, 0, 0), i18n.ZH) {
			if it.X < 0 || it.W < 0 || it.Height() < 1 {
				t.Errorf("width %d: item %v has X=%d W=%d H=%d", w, it.Kind, it.X, it.W, it.Height())
			}
		}
	}
}

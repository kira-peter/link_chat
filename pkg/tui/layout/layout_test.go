package layout

import (
	"strings"
	"testing"
)

func TestStringWidthCountsCellsNotBytes(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"abc", 3},
		{"中文", 4},  // two wide glyphs
		{"a中b", 4}, // 1 + 2 + 1
		{"", 0},
		{"👍", 2},  // emoji is wide
		{"é", 1}, // combining accent stays one cell
	}
	for _, c := range cases {
		if got := StringWidth(c.in); got != c.want {
			t.Errorf("StringWidth(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestTruncateNeverExceedsWidth(t *testing.T) {
	inputs := []string{
		"short",
		"a much longer sentence that has to be cut down",
		"中文消息内容很长需要截断处理",
		"emoji 👍👍👍👍👍👍👍👍 sequence",
	}
	for _, in := range inputs {
		for width := 1; width <= 40; width++ {
			out := Truncate(in, width)
			if got := StringWidth(out); got > width {
				t.Fatalf("Truncate(%q,%d) width %d exceeds %d: %q", in, width, got, width, out)
			}
		}
	}
}

func TestPadRightAlwaysExactWidth(t *testing.T) {
	for _, in := range []string{"", "ab", "中文", "a longer string than fits"} {
		for width := 0; width <= 20; width++ {
			out := PadRight(in, width)
			if got := StringWidth(out); got != width {
				t.Fatalf("PadRight(%q,%d) width %d, want %d", in, width, got, width)
			}
		}
	}
}

func TestWrapHonoursWidthAndKeepsAllText(t *testing.T) {
	text := "The quick brown fox jumps over the lazy dog and keeps running for a while."
	for width := 10; width <= 40; width++ {
		lines := Wrap(text, width)
		for i, l := range lines {
			if got := StringWidth(l); got > width {
				t.Fatalf("width %d: line %d is %d cells: %q", width, i, got, l)
			}
		}
		// No word may be lost: rejoining must contain every original rune in order.
		joined := strings.Join(lines, " ")
		for _, word := range strings.Fields(text) {
			if !strings.Contains(joined, word) {
				t.Fatalf("width %d lost word %q", width, word)
			}
		}
	}
}

func TestWrapDoesNotSplitWideCharacters(t *testing.T) {
	text := "这是一段中文消息，需要按宽度折行显示。"
	for width := 4; width <= 20; width++ {
		lines := Wrap(text, width)
		var rebuilt strings.Builder
		for _, l := range lines {
			if got := StringWidth(l); got > width {
				t.Fatalf("width %d: line %q is %d cells", width, l, got)
			}
			rebuilt.WriteString(l)
		}
		if rebuilt.String() != text {
			t.Fatalf("width %d: runes lost or reordered: %q", width, rebuilt.String())
		}
	}
}

func TestWrapPreservesExplicitNewlines(t *testing.T) {
	lines := Wrap("first\nsecond", 40)
	if len(lines) != 2 || lines[0] != "first" || lines[1] != "second" {
		t.Fatalf("got %q", lines)
	}
}

func TestWrapEmptyLineStaysEmpty(t *testing.T) {
	lines := Wrap("a\n\nb", 20)
	if len(lines) != 3 {
		t.Fatalf("want 3 lines, got %d: %q", len(lines), lines)
	}
	if lines[1] != "" {
		t.Fatalf("middle line should be empty, got %q", lines[1])
	}
}

func TestWrapIndentAlignsContinuation(t *testing.T) {
	lines := WrapIndent("aaaa bbbb cccc dddd eeee", 12, 3)
	if len(lines) < 2 {
		t.Fatalf("expected wrapping, got %q", lines)
	}
	for i, l := range lines[1:] {
		if !strings.HasPrefix(l, "   ") {
			t.Fatalf("line %d not indented: %q", i+1, l)
		}
		if got := StringWidth(l); got > 12 {
			t.Fatalf("line %d too wide: %d", i+1, got)
		}
	}
}

func TestSidebarWidthStaysBounded(t *testing.T) {
	for total := 40; total <= 300; total++ {
		w := SidebarWidth(total)
		if w < 8 {
			t.Fatalf("total %d: sidebar too narrow: %d", total, w)
		}
		if w >= total {
			t.Fatalf("total %d: sidebar leaves no room: %d", total, w)
		}
	}
}

func TestTruncateMiddleKeepsBothEnds(t *testing.T) {
	out := TruncateMiddle("/home/very/long/path/to/file.txt", 15)
	if StringWidth(out) > 15 {
		t.Fatalf("too wide: %q", out)
	}
	if !strings.HasPrefix(out, "/h") || !strings.HasSuffix(out, "xt") {
		t.Fatalf("did not keep both ends: %q", out)
	}
}

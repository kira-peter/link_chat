package version

import (
	"strings"
	"testing"

	linkchat "link-chat"
)

func TestParse(t *testing.T) {
	good := map[string]Version{
		"0.0.0":    {0, 0, 0},
		"1.0.0":    {1, 0, 0},
		"1.12.3":   {1, 12, 3},
		"10.20.30": {10, 20, 30},
	}
	for s, want := range good {
		got, err := Parse(s)
		if err != nil || got != want {
			t.Errorf("Parse(%q) = %v, %v; want %v", s, got, err, want)
		}
		if got.String() != s {
			t.Errorf("round trip %q -> %q", s, got.String())
		}
	}
	for _, s := range []string{"", "v1.2.3", "1.2", "1.2.3.4", "1.02.3", "01.2.3", "1.2.3-beta", "1.2.3+b", "1..3", "a.b.c", " 1.2.3", "-1.2.3", "1.2.3 "} {
		if _, err := Parse(s); err == nil {
			t.Errorf("Parse(%q) accepted an invalid version", s)
		}
	}
}

func TestCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.10.0", "0.9.5", 1},
		{"1.2.3", "1.2.3", 0},
		{"1.2.3", "1.2.4", -1},
		{"2.0.0", "1.99.99", 1},
		{"1.3.0", "1.2.9", 1},
	}
	for _, c := range cases {
		a, _ := Parse(c.a)
		b, _ := Parse(c.b)
		if got := Compare(a, b); got != c.want {
			t.Errorf("Compare(%s, %s) = %d; want %d", c.a, c.b, got, c.want)
		}
		if got := Compare(b, a); got != -c.want {
			t.Errorf("Compare(%s, %s) = %d; want %d", c.b, c.a, got, -c.want)
		}
	}
}

func TestParseTag(t *testing.T) {
	name, v, ok := ParseTag("link-chat-tui/1.4.2")
	if !ok || name != "link-chat-tui" || v != (Version{1, 4, 2}) {
		t.Fatalf("got %q %v %v", name, v, ok)
	}
	for _, tag := range []string{"", "link-chat-tui", "link-chat-tui/", "link-chat-tui/dev", "link-chat-tui/v1.0.0"} {
		if _, _, ok := ParseTag(tag); ok {
			t.Errorf("ParseTag(%q) reported a version", tag)
		}
	}
	if _, v, ok := ParseTag(ClientTag("x")); !ok || v != Current() {
		t.Errorf("ClientTag does not round-trip: %v %v", v, ok)
	}
}

// TestRepositoryVersionIsValid fails the build's tests when someone writes a
// malformed VERSION file (spec build-version / 唯一版本来源).
func TestRepositoryVersionIsValid(t *testing.T) {
	raw := strings.TrimSpace(linkchat.RawVersion)
	if _, err := Parse(raw); err != nil {
		t.Fatalf("VERSION file holds %q: %v", raw, err)
	}
	if !Valid() || String() != raw {
		t.Fatalf("Current() = %s, Valid() = %v; VERSION = %q", String(), Valid(), raw)
	}
}

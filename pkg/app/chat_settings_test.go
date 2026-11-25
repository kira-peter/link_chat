package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/tui/views"
)

// A config written before these settings existed lays out exactly as before.
func TestOldConfigKeepsTheOldLayout(t *testing.T) {
	p := filepath.Join(appTempDir(t), "old.json")
	if err := os.WriteFile(p, []byte(`{"server":"ws://x/ws","theme":"ink","language":"zh"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Layout(); got != views.DefaultLayout {
		t.Fatalf("an old config gives %+v, want the default %+v", got, views.DefaultLayout)
	}
}

// Values outside the choices fall back to the default, each on its own.
func TestUnknownChatSettingsFallBack(t *testing.T) {
	cfg := Config{BubbleWidth: 55, Density: "roomy", TimeFormat: "12h"}
	got := cfg.Layout()
	if got.BubblePercent != 70 || got.Compact {
		t.Fatalf("unknown values did not fall back: %+v", got)
	}
	if got.Clock24 {
		t.Fatal("a valid setting was lost because another was invalid")
	}
}

// What is saved is what is loaded.
func TestChatSettingsRoundTrip(t *testing.T) {
	p := filepath.Join(appTempDir(t), "rt.json")
	want := Config{Server: "ws://x/ws", BubbleWidth: 80, Density: "compact", TimeFormat: "12h"}
	if err := SaveConfig(p, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.BubbleWidth != 80 || got.Density != "compact" || got.TimeFormat != "12h" {
		t.Fatalf("round trip lost settings: %+v", got)
	}
	if lay := got.Layout(); lay != (views.Layout{BubblePercent: 80, Compact: true, Clock24: false}) {
		t.Fatalf("loaded layout %+v", lay)
	}
}

// The message style settings: absent is shown and the theme's colours, unknown
// colours fall back each on their own, and what is saved is what is loaded.
func TestMessageStyleSettings(t *testing.T) {
	cfg := Config{MsgBorder: "hide", MsgTime: "hide", SelfText: "amber", OtherText: "nope", SelfBorder: "default", OtherBorder: "blue"}
	lay := cfg.Layout()
	if !lay.NoBorder || !lay.NoTime {
		t.Fatalf("border/time not hidden: %+v", lay)
	}
	if lay.SelfText != "amber" || lay.OtherText != "" || lay.SelfBorder != "" || lay.OtherBorder != "blue" {
		t.Fatalf("colours %+v, want amber, default, default, blue", lay)
	}

	p := filepath.Join(appTempDir(t), "style.json")
	if err := SaveConfig(p, cfg); err != nil {
		t.Fatal(err)
	}
	got, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.Layout() != lay {
		t.Fatalf("round trip: %+v, want %+v", got.Layout(), lay)
	}
}

// Choosing a colour on the settings page shows it in the preview at once and
// saves it on commit.
func TestChoosingAMessageColourPreviewsAndSaves(t *testing.T) {
	a, _ := newKeyTestApp(t)
	cats := views.Categories()
	cat := -1
	for i, c := range cats {
		for _, it := range c.Items {
			if it.ID == views.SetSelfBorder {
				cat = i
			}
		}
	}
	if cat < 0 {
		t.Fatal("no category holds my border colour")
	}
	item := 0
	for i, it := range cats[cat].Items {
		if it.ID == views.SetSelfBorder {
			item = i
		}
	}
	a.ui.SettingsNav = views.SettingsNav{Cat: cat, Level: views.LevelItem, Item: item}
	a.handleSettingsPageKey(specialKey(tcell.KeyRight))
	a.handleSettingsPageKey(specialKey(tcell.KeyDown)) // default -> red
	if lay, _, _ := a.previewLayout(); lay.SelfBorder != "red" {
		t.Fatalf("the preview shows %q, want red", lay.SelfBorder)
	}
	if a.cfg.SelfBorder != "" {
		t.Fatal("moving the wheel saved the colour")
	}
	a.handleSettingsPageKey(specialKey(tcell.KeyEnter))
	if a.cfg.SelfBorder != "red" || a.cfg.Layout().SelfBorder != "red" {
		t.Fatalf("committing saved %q", a.cfg.SelfBorder)
	}
	if got := a.settingText(views.SetSelfBorder); got != "红" {
		t.Fatalf("the page shows %q, want 红", got)
	}
}

package views

import (
	"strings"
	"testing"

	"link-chat/pkg/client"
	"link-chat/pkg/i18n"
	"link-chat/pkg/tui/layout"
	"link-chat/pkg/tui/widget"
)

// Every piece of sidebar text must fit its row in both languages at the narrowest
// sidebar the layout allows. A string that is cut off mid-word is a bug the user
// sees on first launch.
func TestSidebarTextFitsInBothLanguages(t *testing.T) {
	width := layout.SidebarMin
	// Row text starts at column 2 and keeps one cell clear on the right.
	avail := width - 2 - 1

	type item struct {
		what string
		text func(l i18n.Lang) string
	}
	items := []item{
		{"私聊 header", func(l i18n.Lang) string { return i18n.SectionDMTitle.Get(l) }},
		{"群聊 header", func(l i18n.Lang) string { return i18n.SectionChannelTitle.Get(l) }},
		{"DM empty hint", func(l i18n.Lang) string { return i18n.SectionDMEmpty.Get(l) }},
		{"channel empty hint", func(l i18n.Lang) string { return i18n.SectionChannelEmpty.Get(l) }},
		{"settings entry", func(l i18n.Lang) string { return i18n.EntrySettings.Get(l) }},
		{"help entry", func(l i18n.Lang) string { return i18n.EntryHelp.Get(l) }},
		{"about entry", func(l i18n.Lang) string { return i18n.EntryAbout.Get(l) }},
	}
	for _, lang := range []i18n.Lang{i18n.ZH, i18n.EN} {
		for _, it := range items {
			txt := it.text(lang)
			if w := layout.StringWidth(txt); w > avail {
				t.Errorf("%s (%s): %q is %d cells wide, the row has only %d", it.what, lang, txt, w, avail)
			}
		}
	}
}

// And on screen: at the narrowest sidebar, in each language, every block label is
// found whole.
func TestSidebarRendersWholeLabelsInBothLanguages(t *testing.T) {
	w := layout.SidebarMin
	for _, lang := range []i18n.Lang{i18n.ZH, i18n.EN} {
		s := newScreen(t, w, sideH)
		rows := sidebarState(0, 0).SidebarRows()
		List{T: toks()}.Render(s, widget.Rect{X: 0, Y: 0, W: w, H: sideH}, ListModel{
			Rows: rows, Selected: client.FirstSelectable(rows), Focus: client.FocusList,
		}, lang)
		s.Show()
		var all strings.Builder
		for y := 0; y < sideH; y++ {
			all.WriteString(rowText(s, y, w))
			all.WriteByte('\n')
		}
		for _, want := range []string{
			i18n.SectionDMTitle.Get(lang), i18n.SectionChannelTitle.Get(lang),
			i18n.SectionDMEmpty.Get(lang), i18n.SectionChannelEmpty.Get(lang),
			i18n.EntrySettings.Get(lang), i18n.EntryHelp.Get(lang), i18n.EntryAbout.Get(lang),
		} {
			if !strings.Contains(all.String(), want) {
				t.Errorf("%s: %q is cut off or missing at width %d:\n%s", lang, want, w, all.String())
			}
		}
	}
}

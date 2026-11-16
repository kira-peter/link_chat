package views

import (
	"strings"
	"testing"

	"link-chat/pkg/i18n"
	"link-chat/pkg/tui/widget"
)

func renderAbout(t *testing.T, w, h int, version string, lang i18n.Lang) string {
	t.Helper()
	s := newScreen(t, w, h)
	AboutPage{T: toks()}.Render(s, widget.Rect{X: 0, Y: 0, W: w, H: h}, version, true, false, lang)
	s.Show()
	var all strings.Builder
	for y := 0; y < h; y++ {
		all.WriteString(rowText(s, y, w))
		all.WriteByte('\n')
	}
	return all.String()
}

// About shows its title, the app's name, a line on what it is, and the version.
func TestAboutPageShowsNameBlurbAndVersion(t *testing.T) {
	for _, lang := range []i18n.Lang{i18n.ZH, i18n.EN} {
		all := renderAbout(t, 80, 22, "1.2.3", lang)
		for _, want := range []string{
			i18n.AboutTitle.Get(lang), i18n.AppName.Get(lang), i18n.AboutBlurb.Get(lang),
			i18n.AboutVer.Get(lang), "1.2.3",
		} {
			if !strings.Contains(all, want) {
				t.Fatalf("%s: About is missing %q:\n%s", lang, want, all)
			}
		}
	}
}

// An unreleased build says "dev" rather than showing an empty version.
func TestAboutShowsDevForAnUnreleasedBuild(t *testing.T) {
	if !strings.Contains(renderAbout(t, 80, 22, "dev", i18n.ZH), "dev") {
		t.Fatal("the version row is missing for a dev build")
	}
}

// A narrow page truncates the text rather than running past its edge.
func TestAboutSurvivesANarrowPage(t *testing.T) {
	const w, h = 20, 14
	s := newScreen(t, w+10, h)
	AboutPage{T: toks()}.Render(s, widget.Rect{X: 0, Y: 0, W: w, H: h}, "a-very-long-version-string-that-will-not-fit", true, false, i18n.ZH)
	s.Show()
	for y := 0; y < h; y++ {
		for x := w; x < w+10; x++ {
			if r, _, _, _ := s.GetContent(x, y); r != ' ' && r != 0 {
				t.Fatalf("row %d: text spills past the page at column %d (%q)", y, x, r)
			}
		}
	}
}

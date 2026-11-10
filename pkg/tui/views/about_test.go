package views

import (
	"strings"
	"testing"

	"link-chat/pkg/i18n"
)

// About shows the app's name, a line on what it is, and the version it was given.
func TestAboutDialogShowsNameBlurbAndVersion(t *testing.T) {
	for _, lang := range []i18n.Lang{i18n.ZH, i18n.EN} {
		s := newScreen(t, 80, 24)
		Dialog{T: toks()}.RenderAbout(s, 80, 24, "1.2.3", lang)
		s.Show()

		var all strings.Builder
		for y := 0; y < 24; y++ {
			all.WriteString(rowText(s, y, 80))
			all.WriteByte('\n')
		}
		for _, want := range []string{
			i18n.AboutTitle.Get(lang), i18n.AppName.Get(lang), i18n.AboutBlurb.Get(lang),
			i18n.AboutVer.Get(lang), "1.2.3", i18n.KeyBack.Get(lang),
		} {
			if !strings.Contains(all.String(), want) {
				t.Fatalf("%s: About is missing %q:\n%s", lang, want, all.String())
			}
		}
	}
}

// An unreleased build says "dev" rather than showing an empty version.
func TestAboutShowsDevForAnUnreleasedBuild(t *testing.T) {
	s := newScreen(t, 80, 24)
	Dialog{T: toks()}.RenderAbout(s, 80, 24, "dev", i18n.ZH)
	s.Show()
	found := false
	for y := 0; y < 24; y++ {
		if strings.Contains(rowText(s, y, 80), "dev") {
			found = true
		}
	}
	if !found {
		t.Fatal("the version row is missing for a dev build")
	}
}

// A narrow terminal truncates the text instead of breaking the frame.
func TestAboutSurvivesANarrowTerminal(t *testing.T) {
	s := newScreen(t, 30, 14)
	Dialog{T: toks()}.RenderAbout(s, 30, 14, "a-very-long-version-string-that-will-not-fit", i18n.ZH)
	s.Show()
	top, bottom := -1, -1
	for y := 0; y < 14; y++ {
		row := rowText(s, y, 30)
		if strings.ContainsRune(row, '┌') {
			top = y
		}
		if strings.ContainsRune(row, '└') {
			bottom = y
		}
	}
	if top < 0 || bottom < 0 {
		t.Fatal("the About frame is missing on a narrow terminal")
	}
	left, right := -1, -1
	for x := 0; x < 30; x++ {
		if r, _, _, _ := s.GetContent(x, top); r == '┌' {
			left = x
		}
		if r, _, _, _ := s.GetContent(x, top); r == '┐' {
			right = x
		}
	}
	for y := top + 1; y < bottom; y++ {
		if r, _, _, _ := s.GetContent(right, y); r != '│' {
			t.Fatalf("row %d: the right border is broken (%q)", y, r)
		}
		if r, _, _, _ := s.GetContent(left, y); r != '│' {
			t.Fatalf("row %d: the left border is broken (%q)", y, r)
		}
	}
}

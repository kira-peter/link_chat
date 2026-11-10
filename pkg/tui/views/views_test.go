package views

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/client"
	"link-chat/pkg/i18n"
	"link-chat/pkg/protocol"
	"link-chat/pkg/tui/layout"
	"link-chat/pkg/tui/theme"
	"link-chat/pkg/tui/widget"
)

// newScreen builds an offscreen screen of the given size.
func newScreen(t *testing.T, w, h int) tcell.SimulationScreen {
	t.Helper()
	s := tcell.NewSimulationScreen("UTF-8")
	if err := s.Init(); err != nil {
		t.Fatalf("init simulation screen: %v", err)
	}
	s.SetSize(w, h)
	t.Cleanup(s.Fini)
	return s
}

// rowText reads back one row as a string, which is how these tests assert what
// a user would actually see.
//
// Show() is called first: a simulation screen only publishes its drawable cells
// on Show, so reading before it would see an empty buffer rather than the frame
// the view just painted.
func rowText(s tcell.SimulationScreen, y, w int) string {
	s.Show()
	var b strings.Builder
	// A wide rune occupies two cells and the second is a continuation. The screen
	// itself reports each cell's width, so that is asked directly rather than
	// guessed from a width table: a guess is exactly what once made a narrow
	// marker glyph swallow the character after it.
	skip := 0
	for x := 0; x < w; x++ {
		if skip > 0 {
			skip--
			continue
		}
		r, _, _, width := s.GetContent(x, y)
		if r == 0 {
			r = ' '
		}
		b.WriteRune(r)
		if width == 2 {
			skip = 1
		}
	}
	return b.String()
}

func toks() theme.Tokens {
	return theme.NewTokens(theme.Ink, theme.ColorTrue, true)
}

// The list must show names, and the selected row must be a full-width highlight
// rather than a marker glyph (spec tui-design-system / 选中行整行高亮).
func TestListRendersNamesAndSelection(t *testing.T) {
	s := newScreen(t, 40, 10)
	lt := List{T: toks()}

	convs := []*client.Conversation{
		{Conversation: protocol.Conversation{ID: "a", Name: "general", Kind: "channel"}},
		{Conversation: protocol.Conversation{ID: "b", Name: "random", Kind: "channel"}},
	}
	r := widget.Rect{X: 0, Y: 0, W: 40, H: 10}
	lt.Render(s, r, ListModel{
		Conversations: convs, Selected: 0, ActiveID: "a",
		Focus: client.FocusList, Username: "me",
	}, i18n.ZH)

	// Header row shows the section label.
	if got := rowText(s, 0, 40); !strings.Contains(got, "会话") {
		t.Fatalf("header missing: %q", got)
	}
	// First conversation on the row after the header.
	if got := rowText(s, 1, 40); !strings.Contains(got, "general") {
		t.Fatalf("first conversation missing: %q", got)
	}
	if got := rowText(s, 2, 40); !strings.Contains(got, "random") {
		t.Fatalf("second conversation missing: %q", got)
	}

	// The selected row must have a background across its whole width, which is
	// what "selection is a fill, not a glyph" means in practice.
	s.Show()
	cells, width, _ := s.GetContents()
	_, selBg, _ := toks().Selected().Decompose()
	filled := 0
	for x := 0; x < width; x++ {
		_, bg, _ := cells[1*width+x].Style.Decompose()
		if bg == selBg {
			filled++
		}
	}
	if filled < width-1 {
		t.Fatalf("selection background covers %d of %d cells", filled, width)
	}
}

// Unread counts must appear and must not appear for the open conversation.
func TestListShowsUnreadCount(t *testing.T) {
	s := newScreen(t, 40, 10)
	lt := List{T: toks()}

	convs := []*client.Conversation{
		{Conversation: protocol.Conversation{ID: "a", Name: "general", Kind: "channel"}, Unread: 3},
	}
	lt.Render(s, widget.Rect{X: 0, Y: 0, W: 40, H: 10}, ListModel{
		Conversations: convs, Selected: 0, ActiveID: "", Focus: client.FocusList,
	}, i18n.ZH)

	row := rowText(s, 1, 40)
	if !strings.Contains(row, "general") || !strings.Contains(row, "3") {
		t.Fatalf("unread badge missing: %q", row)
	}
}

// Consecutive messages from one author collapse to a single header
// (spec tui-chat-client / 连续消息合并).
func TestTranscriptMergesConsecutiveMessages(t *testing.T) {
	s := newScreen(t, 70, 20)
	tr := Transcript{T: toks()}

	msgs := []protocol.Message{
		{ID: "m1", Seq: 1, AuthorID: "u1", AuthorName: "alice", Body: "first", CreatedAt: 1000},
		{ID: "m2", Seq: 2, AuthorID: "u1", AuthorName: "alice", Body: "second", CreatedAt: 2000},
		{ID: "m3", Seq: 3, AuthorID: "u1", AuthorName: "alice", Body: "third", CreatedAt: 3000},
	}
	cv := &client.Conversation{}
	cv.ID = "c1"
	cv.Name = "general"
	cv.Messages = msgs

	tr.Render(s, widget.Rect{X: 0, Y: 0, W: 70, H: 20}, TranscriptModel{
		Conversation: cv, UserID: "me", Now: 10000,
	}, i18n.ZH)

	// "alice" should appear exactly once in the whole visible region.
	whole := ""
	for y := 0; y < 20; y++ {
		whole += rowText(s, y, 70) + "\n"
	}
	if n := strings.Count(whole, "alice"); n != 1 {
		t.Fatalf("author should appear once, found %d times:\n%s", n, whole)
	}
	// All three bodies must be present.
	for _, body := range []string{"first", "second", "third"} {
		if !strings.Contains(whole, body) {
			t.Fatalf("body %q missing:\n%s", body, whole)
		}
	}
}

// Wrapped continuation lines must align under the body, not under the author
// (spec tui-chat-client / 长消息折行).
func TestTranscriptAlignsContinuationLines(t *testing.T) {
	s := newScreen(t, 60, 24)
	tr := Transcript{T: toks()}

	long := strings.Repeat("word ", 40)
	cv := &client.Conversation{}
	cv.ID = "c1"
	cv.Name = "general"
	cv.Messages = []protocol.Message{
		{ID: "m1", Seq: 1, AuthorID: "u1", AuthorName: "alice", Body: long, CreatedAt: 1000},
	}

	tr.Render(s, widget.Rect{X: 0, Y: 0, W: 60, H: 24}, TranscriptModel{
		Conversation: cv, UserID: "me", Now: 10000,
	}, i18n.ZH)

	// Find the column where the first body line starts, then require every
	// continuation line to start there too.
	s.Show()
	cells, width, _ := s.GetContents()
	bodyCol := -1
	continuations := 0
	for y := 0; y < 24; y++ {
		row := rowText(s, y, width)
		idx := strings.Index(row, "word")
		if idx < 0 {
			continue
		}
		if bodyCol == -1 {
			bodyCol = idx
			continue
		}
		continuations++
		if idx != bodyCol {
			t.Fatalf("continuation on row %d starts at %d, want %d", y, idx, bodyCol)
		}
	}
	_ = cells
	if continuations == 0 {
		t.Fatal("message did not wrap")
	}
}

// Deleted messages keep a placeholder and never show their old body.
func TestTranscriptShowsDeletedPlaceholder(t *testing.T) {
	s := newScreen(t, 60, 16)
	tr := Transcript{T: toks()}

	cv := &client.Conversation{}
	cv.ID = "c1"
	cv.Name = "general"
	cv.Messages = []protocol.Message{
		{ID: "m1", Seq: 1, AuthorID: "u1", AuthorName: "alice", Body: "", Deleted: true, CreatedAt: 1000},
	}

	tr.Render(s, widget.Rect{X: 0, Y: 0, W: 60, H: 16}, TranscriptModel{
		Conversation: cv, UserID: "me", Now: 10000,
	}, i18n.ZH)

	whole := ""
	for y := 0; y < 16; y++ {
		whole += rowText(s, y, 60) + "\n"
	}
	if !strings.Contains(whole, i18n.MessageDeleted.Get(i18n.ZH)) {
		t.Fatalf("deleted placeholder missing:\n%s", whole)
	}
}

// The composer shows its placeholder when empty and the text when not.
func TestComposerShowsText(t *testing.T) {
	s := newScreen(t, 60, 6)
	cp := Composer{T: toks()}

	cp.Render(s, widget.Rect{X: 0, Y: 0, W: 60, H: 6}, ComposerModel{
		Focus: client.FocusInput, Placeholder: "message",
	}, i18n.ZH)
	whole := ""
	for y := 0; y < 6; y++ {
		whole += rowText(s, y, 60) + "\n"
	}
	if !strings.Contains(whole, "message") {
		t.Fatalf("placeholder missing:\n%s", whole)
	}

	s2 := newScreen(t, 60, 6)
	cp.Render(s2, widget.Rect{X: 0, Y: 0, W: 60, H: 6}, ComposerModel{
		Input: []rune("hello there"), Cursor: 5,
		Focus: client.FocusInput, Placeholder: "message",
	}, i18n.ZH)
	whole2 := ""
	for y := 0; y < 6; y++ {
		whole2 += rowText(s2, y, 60) + "\n"
	}
	if !strings.Contains(whole2, "hello there") {
		t.Fatalf("draft missing:\n%s", whole2)
	}
}

// The login panel must show two fields, a button and both links, and must not
// offer the server address as a field (design.md decision 6).
func TestLoginDialogLayout(t *testing.T) {
	s := newScreen(t, 80, 30)
	d := Dialog{T: toks()}

	d.RenderLogin(s, 80, 30, LoginModel{
		Username: "alice", Focus: LoginFocusPassword,
		ServerShown: "ws://localhost:8100/ws",
	}, i18n.ZH)

	whole := ""
	for y := 0; y < 30; y++ {
		whole += rowText(s, y, 80) + "\n"
	}
	want := []string{
		i18n.LoginTitle.Get(i18n.ZH),
		i18n.LoginUsername.Get(i18n.ZH),
		i18n.LoginPassword.Get(i18n.ZH),
		i18n.LoginSubmit.Get(i18n.ZH),
		i18n.ForgotPassword.Get(i18n.ZH),
		i18n.RegisterNow.Get(i18n.ZH),
		"alice",
	}
	for _, w := range want {
		if !strings.Contains(whole, w) {
			t.Fatalf("login panel missing %q:\n%s", w, whole)
		}
	}
	// The server address must never be offered as a form field: it belongs in
	// configuration, and only appears as a hint after a failure.
	if strings.Contains(whole, "服务器") {
		t.Fatalf("login panel still shows a server field:\n%s", whole)
	}
}

// The links must be reachable and visibly distinct from the button.
func TestLoginFocusCyclesAllElements(t *testing.T) {
	seen := map[LoginFocus]bool{}
	f := LoginFocusUsername
	for i := 0; i < int(loginFocusCount); i++ {
		seen[f] = true
		f = f.Next()
	}
	if f != LoginFocusUsername {
		t.Fatalf("focus cycle does not return to start: %v", f)
	}
	if len(seen) != int(loginFocusCount) {
		t.Fatalf("cycle visited %d of %d elements", len(seen), loginFocusCount)
	}
	if LoginFocusUsername.Prev() != LoginFocusRegister {
		t.Fatal("reverse cycle is wrong")
	}
	if !LoginFocusUsername.IsField() || LoginFocusSubmit.IsField() {
		t.Fatal("IsField misclassifies elements")
	}
}

// Theme switching must reach every token a view uses.
func TestThemeSwitchChangesColours(t *testing.T) {
	ink := theme.NewTokens(theme.Ink, theme.ColorTrue, true)
	paper := theme.NewTokens(theme.Paper, theme.ColorTrue, true)

	if ink.Bg == paper.Bg {
		t.Fatal("themes share a background colour")
	}
	if ink.Text == paper.Text {
		t.Fatal("themes share a text colour")
	}
	// Selection must remain distinguishable from the background in both.
	for name, tk := range map[string]theme.Tokens{"ink": ink, "paper": paper} {
		if tk.SelectedBg == tk.Bg {
			t.Fatalf("%s: selection background equals page background", name)
		}
	}
}

// An unknown theme name must fall back rather than fail.
func TestUnknownThemeFallsBack(t *testing.T) {
	p, ok := theme.Resolve("does-not-exist")
	if ok {
		t.Fatal("unknown theme reported as known")
	}
	if p.Name != theme.Ink.Name {
		t.Fatalf("did not fall back to ink: %s", p.Name)
	}
}

// Narrow terminals must collapse rather than render an unusable sidebar
// (spec tui-chat-client / 窄屏折叠).
func TestNarrowLayoutCollapses(t *testing.T) {
	if w := layout.SidebarWidth(40); w >= 40 {
		t.Fatalf("sidebar consumed the whole narrow window: %d", w)
	}
	if layout.MinViewWidth >= 60 {
		t.Fatalf("collapse threshold suspiciously high: %d", layout.MinViewWidth)
	}
}

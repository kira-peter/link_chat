//go:build ignore

// preview renders sample frames to stdout so the layout can be inspected without a
// terminal session. It draws the real views from the real state, so what it prints
// is what a user sees.
//
//	go run tools/preview.go            # every scene
//	go run tools/preview.go folded     # one scene by name
//
// Scenes: open, folded, mid, empty, narrow, login, remembered, register, recover,
// about, mono.
package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/client"
	"link-chat/pkg/i18n"
	"link-chat/pkg/protocol"
	"link-chat/pkg/tui/anim"
	"link-chat/pkg/tui/layout"
	"link-chat/pkg/tui/theme"
	"link-chat/pkg/tui/views"
	"link-chat/pkg/tui/widget"
)

const now = int64(1_762_000_000_000)

// state builds a populated client state: two DMs, three channels, a busy channel
// open, and a draft in the composer.
func state() *client.AppState {
	st := client.NewAppState()
	st.UserID, st.Username = "u_me", "alice"
	st.State = client.StateReady
	st.ThemeName = "ink"

	add := func(id, name, kind string, at int64, unread int) *client.Conversation {
		c := st.EnsureConversation(id)
		c.Name, c.Kind, c.LastMessageAt, c.Unread = name, kind, at, unread
		return c
	}
	general := add("c1", "general", protocol.KindChannel, now-30_000, 0)
	general.MemberCount = 4
	general.Topic = "产品与工程同步"
	general.Messages = []protocol.Message{
		{ID: "m1", Seq: 1, AuthorID: "u_b", AuthorName: "bob", Body: "新的快捷键都接好了，试试 Tab 切换面板", CreatedAt: now - 3_900_000},
		{ID: "m2", Seq: 2, AuthorID: "u_b", AuthorName: "bob", Body: "选中态改成了整行背景，比反色干净很多", CreatedAt: now - 3_880_000},
		{ID: "m3", Seq: 3, AuthorID: "u_c", AuthorName: "carol", Body: "我看了下折行，中文和 emoji 都没有错位了 👍", CreatedAt: now - 200_000},
		{ID: "m4", Seq: 4, AuthorID: "u_me", AuthorName: "alice", Body: "好，那我把消息合并的窗口定在 2 分钟，再长就看不出是同一个人了", CreatedAt: now - 30_000},
	}
	add("d1", "carol", protocol.KindDM, now-5_000, 2).Messages = []protocol.Message{
		{ID: "d1", Seq: 1, AuthorID: "u_c", AuthorName: "carol", Body: "私聊这条路径也测一下", CreatedAt: now - 5_000},
	}
	add("d2", "bob", protocol.KindDM, now-7_200_000, 0)
	add("c2", "random", protocol.KindChannel, now-3_600_000, 0)
	add("c3", "design", protocol.KindChannel, now-90_000_000, 0)

	st.ActiveID = "c1"
	st.MarkRead("c1")
	st.RestoreInput("先按这个方向往下做")
	return st
}

// mainScene draws the main interface at a given fold. fold 0 is the list open, 1 is
// folded away; anything between is a frame of the transition.
func mainScene(st *client.AppState, tk theme.Tokens, w, h int, fold float64, focus client.Focus, lang i18n.Lang) []string {
	return mainSceneAt(st, tk, w, h, fold, focus, lang, nil)
}

// mainSceneThen draws the main interface with the list open and focused, then
// lets over draw a layer on top.
func mainSceneThen(st *client.AppState, tk theme.Tokens, w, h int, lang i18n.Lang, over func(tcell.Screen)) []string {
	return mainSceneAt(st, tk, w, h, 0, client.FocusList, lang, over)
}

func mainSceneAt(st *client.AppState, tk theme.Tokens, w, h int, fold float64, focus client.Focus, lang i18n.Lang, over func(tcell.Screen)) []string {
	s := tcell.NewSimulationScreen("UTF-8")
	if err := s.Init(); err != nil {
		panic(err)
	}
	defer s.Fini()
	s.SetSize(w, h)

	st.Focus = focus
	chrome := views.Chrome{T: tk}
	content := chrome.Frame(s, st, lang, w, h)

	side := views.SidebarRectAt(content, fold)
	main := views.MainRectAt(content, fold)

	rows := st.SidebarRows()
	sel := client.IndexOfConversation(rows, st.ActiveID)
	lm := views.ListModel{
		Rows: rows, Selected: sel, ActiveID: st.ActiveID, Focus: focus, Now: now, Username: st.Username,
		Shimmer: true, Depth: anim.DepthTrue, Elapsed: anim.ListShimmer.Travel / 2,
	}

	// While the name is in flight the list and the title leave its slot to the
	// flight layer, the same as the app does.
	flying := fold > 0 && fold < 1
	if flying {
		lm.FlyingID = st.ActiveID
	}
	if views.SidebarContentVisible(fold) {
		views.List{T: tk}.Render(s, side, lm, lang)
	}
	views.DrawDividerAt(s, tk, content, fold)

	trRect, compRect := views.ComposerRect(main, 1)
	views.Transcript{T: tk}.Render(s, trRect, views.TranscriptModel{
		Conversation: st.Active(), UserID: st.UserID, Now: now, Focus: focus, NameInFlight: flying,
	}, lang)
	views.Composer{T: tk}.Render(s, compRect, views.ComposerModel{
		Input: st.Input, Cursor: st.InputCursor, Focus: focus,
		Placeholder: i18n.ComposeHint.Get(lang), MaxBytes: 4000,
	}, lang)

	if flying {
		line, ok := views.RowLine(rows, sel, st.ActiveID, content.H, false, lang)
		if ok {
			fx, fy := views.SidebarRowAnchor(content, line)
			tx, ty := views.TitleAnchor(content, 1, st.Active(), lang)
			views.FlightLayer{T: tk}.Render(s, content, views.NameFlight{
				Name: st.Active().Name, Fold: fold, FromX: fx, FromY: fy, ToX: tx, ToY: ty,
			}, focus, lang)
		}
	}
	chrome.StatusBar(s, st, lang, w, h)
	if over != nil {
		over(s)
	}
	return dump(s)
}

// snapshotOf takes the picture the sign-out exit closes over.
func snapshotOf(st *client.AppState, tk theme.Tokens, w, h int, lang i18n.Lang) *views.Snapshot {
	var snap *views.Snapshot
	mainSceneThen(st, tk, w, h, lang, func(s tcell.Screen) { snap = views.TakeSnapshot(s) })
	return snap
}

// exitScene draws one frame of the sign-out exit at fraction f: the stage beneath,
// the reversed entrance over it.
func exitScene(tk theme.Tokens, w, h int, f float64, under *views.Snapshot, lang i18n.Lang) []string {
	s := tcell.NewSimulationScreen("UTF-8")
	if err := s.Init(); err != nil {
		panic(err)
	}
	defer s.Fini()
	s.SetSize(w, h)
	panel := views.LoginPanelRect(w, h)
	views.Stage{T: tk}.Render(s, w, h, views.StageModel{Panel: panel, Elapsed: 4 * time.Second, Fade: 1})
	views.Dialog{T: tk}.RenderLogin(s, w, h, views.LoginModel{Username: "alice", Focus: views.LoginFocusPassword}, lang)
	phase, p := anim.ExitAt(time.Duration(f * float64(anim.EntranceDuration)))
	views.EntranceOverlay{T: tk}.Render(s, w, h, views.EntranceModel{
		Phase: phase, P: p, Panel: panel, StageElapsed: 4 * time.Second, Depth: anim.DepthTrue, Under: under, Reveal: true,
	})
	return dump(s)
}

// stageScene draws the sign-in stage: background dots and the login panel.
func stageScene(tk theme.Tokens, w, h int, lang i18n.Lang) []string {
	s := tcell.NewSimulationScreen("UTF-8")
	if err := s.Init(); err != nil {
		panic(err)
	}
	defer s.Fini()
	s.SetSize(w, h)
	views.Stage{T: tk}.Render(s, w, h, views.StageModel{Panel: views.LoginPanelRect(w, h), Elapsed: 4 * time.Second, Fade: 1})
	views.Dialog{T: tk}.RenderLogin(s, w, h, views.LoginModel{
		Username: "alice", Remembered: true, Focus: views.LoginFocusSubmit, ServerShown: "ws://127.0.0.1:8100/ws",
	}, lang)
	return dump(s)
}

// entranceScene draws one frame of the entrance at fraction f of its duration:
// the main interface underneath, the entrance layer over it.
func entranceScene(tk theme.Tokens, w, h int, f float64, lang i18n.Lang) []string {
	st := state()
	st.ActiveID = ""
	lines := mainSceneThen(st, tk, w, h, lang, func(s tcell.Screen) {
		phase, p := anim.EntranceAt(time.Duration(f * float64(anim.EntranceDuration)))
		views.EntranceOverlay{T: tk}.Render(s, w, h, views.EntranceModel{
			Phase: phase, P: p, Panel: views.LoginPanelRect(w, h), StageElapsed: 4 * time.Second, Depth: anim.DepthTrue,
		})
	})
	return lines
}

// dialogScene draws one dialog over an empty frame.
func dialogScene(tk theme.Tokens, w, h int, lang i18n.Lang, draw func(d views.Dialog, s tcell.Screen)) []string {
	s := tcell.NewSimulationScreen("UTF-8")
	if err := s.Init(); err != nil {
		panic(err)
	}
	defer s.Fini()
	s.SetSize(w, h)
	draw(views.Dialog{T: tk}, s)
	return dump(s)
}

// dump reads the screen back as text, treating wide runes as two cells.
func dump(s tcell.SimulationScreen) []string {
	s.Show()
	cells, width, height := s.GetContents()
	out := make([]string, 0, height)
	for y := 0; y < height; y++ {
		var b strings.Builder
		skip := 0
		for x := 0; x < width; x++ {
			if skip > 0 {
				skip--
				continue
			}
			rs := cells[y*width+x].Runes
			if len(rs) == 0 {
				b.WriteRune(' ')
				continue
			}
			b.WriteRune(rs[0])
			// Only genuinely wide runes occupy a second cell. Box-drawing characters
			// are narrow despite their high code points.
			if layout.RuneWidth(rs[0]) == 2 {
				skip = 1
			}
		}
		out = append(out, strings.TrimRight(b.String(), " "))
	}
	return out
}

func main() {
	ink := theme.NewTokens(theme.Ink, theme.ColorTrue, true)
	mono := theme.NewTokens(theme.Ink, theme.ColorMono, true)
	zh := i18n.ZH
	const w, h = 92, 28

	empty := client.NewAppState()
	empty.State = client.StateReady

	type scene struct {
		name, note string
		lines      func() []string
	}
	scenes := []scene{
		{"open", "list open, focus on the list (just after pressing ←)", func() []string {
			return mainScene(state(), ink, w, h, 0, client.FocusList, zh)
		}},
		{"folded", "list folded, conversation fills the row, focus in the composer", func() []string {
			return mainScene(state(), ink, w, h, 1, client.FocusInput, zh)
		}},
		{"mid", "40% through the fold: the name is in the air, the list is narrowing", func() []string {
			return mainScene(state(), ink, w, h, 0.4, client.FocusInput, zh)
		}},
		{"empty", "just after sign-in: list open, nothing opened, welcome page", func() []string {
			return mainScene(empty, ink, w, h, 0, client.FocusList, zh)
		}},
		{"narrow", "a 44-column window: one pane, bubbles without borders", func() []string {
			return mainScene(state(), ink, 44, 22, 0, client.FocusInput, zh)
		}},
		{"mono", "no colour: dashed borders for the other side, solid for mine", func() []string {
			return mainScene(state(), mono, w, h, 1, client.FocusInput, zh)
		}},
		{"logout", "sign-out confirmation: cancel focused, then sign out focused", func() []string {
			var out []string
			for _, f := range []views.LogoutChoice{views.LogoutCancel, views.LogoutConfirm} {
				out = append(out, mainSceneThen(state(), ink, w, h, zh, func(s tcell.Screen) {
					views.Dialog{T: ink}.RenderLogout(s, w, h, views.LogoutModel{Focus: f, Depth: anim.DepthTrue}, zh)
				})...)
			}
			return out
		}},
		{"exit", "the sign-out exit: the entrance backwards", func() []string {
			under := snapshotOf(state(), ink, w, h, zh)
			var out []string
			for _, f := range []float64{0.15, 0.40, 0.62, 0.80, 0.92, 1.0} {
				out = append(out, fmt.Sprintf("--- %.0f%% (%dms)", f*100, int(f*float64(anim.EntranceDuration/time.Millisecond))))
				out = append(out, exitScene(ink, w, h, f, under, zh)...)
			}
			return out
		}},
		{"stage", "the sign-in stage: the panel alone on drifting dots", func() []string {
			return stageScene(ink, w, h, zh)
		}},
		{"entrance", "the sign-in entrance: collapse, stretch, open, done", func() []string {
			var out []string
			for _, f := range []float64{0.12, 0.22, 0.35, 0.60, 0.85, 1.0} {
				out = append(out, fmt.Sprintf("--- %.0f%% (%dms)", f*100, int(f*float64(anim.EntranceDuration/time.Millisecond))))
				out = append(out, entranceScene(ink, w, h, f, zh)...)
			}
			return out
		}},
		{"login", "sign-in panel with an error between the password and the button", func() []string {
			return dialogScene(ink, 80, 20, zh, func(d views.Dialog, s tcell.Screen) {
				d.RenderLogin(s, 80, 20, views.LoginModel{
					Username: "alice", Focus: views.LoginFocusPassword,
					Err: "用户名或密码不对", ServerShown: "ws://127.0.0.1:8100/ws",
				}, zh)
			})
		}},
		{"remembered", "sign-in panel with a saved session: dots, no hint, no caret", func() []string {
			return dialogScene(ink, 80, 20, zh, func(d views.Dialog, s tcell.Screen) {
				d.RenderLogin(s, 80, 20, views.LoginModel{
					Username: "alice", Remembered: true, Focus: views.LoginFocusSubmit,
					ServerShown: "ws://127.0.0.1:8100/ws",
				}, zh)
			})
		}},
		{"register", "registration: four fields, one width", func() []string {
			return dialogScene(ink, 80, 24, zh, func(d views.Dialog, s tcell.Screen) {
				d.RenderRegister(s, 80, 24, views.RegisterModel{Username: "ab", Password: "x", Answer: "a much longer answer"}, zh)
			})
		}},
		{"recover", "password recovery, last step", func() []string {
			return dialogScene(ink, 80, 18, zh, func(d views.Dialog, s tcell.Screen) {
				d.RenderRecover(s, 80, 18, views.RecoverModel{Step: views.RecoverStepPassword, NewPass: "abc"}, zh)
			})
		}},
		{"about", "the About dialog", func() []string {
			return dialogScene(ink, 80, 16, zh, func(d views.Dialog, s tcell.Screen) {
				d.RenderAbout(s, 80, 16, "dev", zh)
			})
		}},
	}

	want := map[string]bool{}
	for _, a := range os.Args[1:] {
		want[a] = true
	}
	for _, sc := range scenes {
		if len(want) > 0 && !want[sc.name] {
			continue
		}
		fmt.Printf("\n=== %s — %s\n", sc.name, sc.note)
		fmt.Println(strings.Repeat("─", 60))
		for _, ln := range sc.lines() {
			fmt.Println(ln)
		}
	}
	_ = widget.Rect{}
	_ = time.Second
}

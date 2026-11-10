//go:build ignore

// preview renders a sample frame to stdout so the layout can be inspected
// without a terminal session. Build it with:
//
//	go run tools/preview.go
package main

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/client"
	"link-chat/pkg/i18n"
	"link-chat/pkg/protocol"
	"link-chat/pkg/tui/layout"
	"link-chat/pkg/tui/theme"
	"link-chat/pkg/tui/views"
	"link-chat/pkg/tui/widget"
)

func main() {
	const w, h = 88, 26
	s := tcell.NewSimulationScreen("UTF-8")
	if err := s.Init(); err != nil {
		panic(err)
	}
	defer s.Fini()
	s.SetSize(w, h)

	tk := theme.NewTokens(theme.Ink, theme.ColorTrue, true)
	lang := i18n.ZH

	now := int64(1_762_000_000_000)
	st := client.NewAppState()
	st.UserID, st.Username = "u_me", "alice"
	st.State = client.StateReady
	st.ThemeName = "ink"

	general := st.EnsureConversation("c1")
	general.Name = "general"
	general.Kind = protocol.KindChannel
	general.MemberCount = 4
	general.Topic = "产品与工程同步"
	general.LastMessageAt = now - 30_000
	general.Messages = []protocol.Message{
		{ID: "m1", Seq: 1, AuthorID: "u_b", AuthorName: "bob", Body: "新的快捷键都接好了，试试 Tab 切换面板", CreatedAt: now - 400_000},
		{ID: "m2", Seq: 2, AuthorID: "u_b", AuthorName: "bob", Body: "选中态改成了整行背景，比反色干净很多", CreatedAt: now - 380_000},
		{ID: "m3", Seq: 3, AuthorID: "u_c", AuthorName: "carol", Body: "我看了下折行，中文和 emoji 都没有错位了 👍", CreatedAt: now - 200_000},
		{ID: "m4", Seq: 4, AuthorID: "u_me", AuthorName: "alice", Body: "好，那我把消息合并的窗口定在 2 分钟，再长就看不出是同一个人了", CreatedAt: now - 30_000},
	}
	dm := st.EnsureConversation("d1")
	dm.Name = "carol"
	dm.Kind = protocol.KindDM
	dm.LastMessageAt = now - 5_000
	dm.Unread = 2
	dm.Messages = []protocol.Message{
		{ID: "d1", Seq: 1, AuthorID: "u_c", AuthorName: "carol", Body: "私聊这条路径也测一下", CreatedAt: now - 5_000},
	}
	random := st.EnsureConversation("c2")
	random.Name = "random"
	random.Kind = protocol.KindChannel
	random.LastMessageAt = now - 3_600_000
	random.Messages = []protocol.Message{
		{ID: "r1", Seq: 1, AuthorID: "u_b", AuthorName: "bob", Body: "周五团建有人吗", CreatedAt: now - 3_600_000},
	}
	design := st.EnsureConversation("c3")
	design.Name = "design"
	design.Kind = protocol.KindChannel
	design.LastMessageAt = now - 90_000_000

	st.ActiveID = "c1"
	st.Focus = client.FocusInput
	st.MarkRead("c1")
	st.RestoreInput("先按这个方向往下做")

	chrome := views.Chrome{T: tk}
	content := chrome.Frame(s, st, lang, w, h)

	list := views.List{T: tk}
	list.Render(s, views.SidebarRect(content), views.ListModel{
		Conversations: st.VisibleConversations(),
		Selected:      0,
		ActiveID:      st.ActiveID,
		Focus:         client.FocusList,
		Now:           now,
		Username:      st.Username,
	}, lang)
	views.DrawDivider(s, tk, content)

	main := views.MainRect(content)
	trRect, compRect := views.ComposerRect(main, 1)
	views.Transcript{T: tk}.Render(s, trRect, views.TranscriptModel{
		Conversation: st.Active(),
		UserID:       st.UserID,
		Now:          now,
	}, lang)
	views.Composer{T: tk}.Render(s, compRect, views.ComposerModel{
		Input:       st.Input,
		Cursor:      st.InputCursor,
		Focus:       client.FocusInput,
		Placeholder: i18n.ComposeHint.Get(lang),
		MaxBytes:    4000,
	}, lang)

	chrome.StatusBar(s, st, lang, w, h)
	s.Show()

	cells, width, height := s.GetContents()
	fmt.Println(strings.Repeat("─", w))
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
			// Only genuinely wide runes occupy a second cell. Box-drawing
			// characters are narrow despite their high code points.
			if layout.RuneWidth(rs[0]) == 2 {
				skip = 1
			}
		}
		fmt.Println(b.String())
	}
	fmt.Println(strings.Repeat("─", w))
	_ = layout.GutterX
	_ = widget.Rect{}
}

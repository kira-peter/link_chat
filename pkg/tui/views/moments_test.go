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

const pageW, pageH = 80, 30

func screenDump(s tcell.SimulationScreen, w, h int) string {
	var b strings.Builder
	for y := 0; y < h; y++ {
		b.WriteString(strings.TrimRight(rowText(s, y, w), " "))
		b.WriteByte('\n')
	}
	return b.String()
}

func pageRowWith(s tcell.SimulationScreen, w, h int, want string) int {
	for y := 0; y < h; y++ {
		if strings.Contains(rowText(s, y, w), want) {
			return y
		}
	}
	return -1
}

var (
	uAlice = protocol.User{ID: "u1", Username: "alice", Nickname: "小爱", Avatar: "🐱", Color: "rose"}
	uBob   = protocol.User{ID: "u2", Username: "bob", Avatar: "B", Color: "blue", Online: true}
	uCarol = protocol.User{ID: "u3", Username: "carol", Avatar: "C", Color: "green"}
)

func sampleMoments() []protocol.Moment {
	return []protocol.Moment{
		{ID: "m1", Author: uBob, Body: "今天天气不错\n出去走走", Visibility: protocol.VisibilityFriends, CreatedAt: 1000,
			Likes: []protocol.User{uAlice}, Liked: true,
			Comments: []protocol.Comment{
				{ID: "c1", Author: uCarol, Body: "好看"},
				{ID: "c2", Author: uBob, ReplyTo: &uCarol, Body: "谢谢"},
			}},
		{ID: "m2", Author: uAlice, Body: "只给自己看", Visibility: protocol.VisibilityPrivate, CreatedAt: 900},
	}
}

func renderMoments(t *testing.T, m MomentsModel) tcell.SimulationScreen {
	t.Helper()
	s := newScreen(t, pageW, pageH)
	MomentsPage{T: toks()}.Render(s, widget.Rect{W: pageW, H: pageH}, m, i18n.ZH)
	return s
}

func TestMomentsTimelineRender(t *testing.T) {
	m := MomentsModel{Loaded: true, Moments: sampleMoments(), Focused: true, Now: 2000, CommentSel: -1}
	m.Selected[TabTimeline] = 1
	s := renderMoments(t, m)
	dump := screenDump(s, pageW, pageH)
	for _, want := range []string{"发现", "动态", "通知", "+ 发动态", "B  bob", "今天天气不错", "出去走走",
		"♥ 小爱", "carol：好看", "bob 回复 carol：谢谢", "仅自己可见", "只给自己看", "没有更早的了", "l 赞"} {
		if !strings.Contains(dump, want) {
			t.Errorf("missing %q:\n%s", want, dump)
		}
	}
	// The selected moment's rows carry the selection fill; the compose row does not.
	header := pageRowWith(s, pageW, pageH, "B  bob")
	compose := pageRowWith(s, pageW, pageH, "+ 发动态")
	_, bgSel, _ := toks().Selected().Decompose()
	_, _, st, _ := s.GetContent(pageW/2, header)
	if _, bg, _ := st.Decompose(); bg != bgSel {
		t.Errorf("selected moment not highlighted")
	}
	_, _, st, _ = s.GetContent(pageW/2, compose)
	if _, bg, _ := st.Decompose(); bg == bgSel {
		t.Errorf("compose row highlighted")
	}
}

func TestMomentsCommentSelectionMarksIt(t *testing.T) {
	m := MomentsModel{Loaded: true, Moments: sampleMoments(), Focused: true, Now: 2000, CommentSel: 1}
	m.Selected[TabTimeline] = 1
	dump := screenDump(renderMoments(t, m), pageW, pageH)
	if !strings.Contains(dump, "› bob 回复 carol") || strings.Contains(dump, "› carol") {
		t.Fatalf("comment marker:\n%s", dump)
	}
}

func TestMomentsEmptyAndLoading(t *testing.T) {
	dump := screenDump(renderMoments(t, MomentsModel{Focused: true}), pageW, pageH)
	if !strings.Contains(dump, "加载中") {
		t.Fatalf("loading:\n%s", dump)
	}
	dump = screenDump(renderMoments(t, MomentsModel{Loaded: true, Focused: true}), pageW, pageH)
	if !strings.Contains(dump, "还没有动态") {
		t.Fatalf("empty:\n%s", dump)
	}
	dump = screenDump(renderMoments(t, MomentsModel{Unsupported: true}), pageW, pageH)
	if !strings.Contains(dump, "不支持朋友圈") {
		t.Fatalf("unsupported:\n%s", dump)
	}
}

func TestMomentsNoticesRender(t *testing.T) {
	m := MomentsModel{Tab: TabNotices, Focused: true, Now: 5000,
		Notifications: []protocol.Notification{
			{ID: 2, Kind: protocol.NotifyComment, Actor: uBob, Body: "好看", Excerpt: "今天天气", CreatedAt: 4000},
			{ID: 1, Kind: protocol.NotifyLike, Actor: uAlice, CreatedAt: 3000, Read: true},
		}}
	dump := screenDump(renderMoments(t, m), pageW, pageH)
	for _, want := range []string{"通知 1", "评论了你的动态", "「好看」", "今天天气", "赞了你的动态"} {
		if !strings.Contains(dump, want) {
			t.Errorf("missing %q:\n%s", want, dump)
		}
	}
	s := renderMoments(t, m)
	if a, b := colOf(s, pageRowWith(s, pageW, pageH, "评论了"), "评论了"), colOf(s, pageRowWith(s, pageW, pageH, "赞了"), "赞了"); a != b || a < 0 {
		t.Errorf("actions at %d and %d", a, b)
	}
	if r, n := NoticeTarget(m, 1); r != -1 || n != 1 {
		t.Errorf("block 1: %d %d", r, n)
	}
}

func TestFriendsPageRender(t *testing.T) {
	m := FriendsModel{Focused: true, Friends: []protocol.User{uAlice, uBob},
		RemovedBy: []protocol.User{{ID: "u9", Username: "dave"}}, Blocked: []protocol.User{uCarol},
		Requests: []protocol.FriendRequest{{ID: "r1", From: uCarol, Note: "我是 carol"}}}
	render := func(m FriendsModel) string {
		s := newScreen(t, pageW, pageH)
		FriendsPage{T: toks()}.Render(s, widget.Rect{W: pageW, H: pageH}, m, i18n.ZH)
		return screenDump(s, pageW, pageH)
	}
	dump := render(m)
	for _, want := range []string{"通讯录", "好友申请 1", "广场"} {
		if !strings.Contains(dump, want) {
			t.Errorf("contacts missing %q:\n%s", want, dump)
		}
	}
	// Columns are padded, so check each row holds its parts rather than one
	// exact spacing.
	for _, row := range [][]string{{"小爱", "@alice", "离线"}, {"bob", "在线"}, {"dave", "已将你删除"}, {"carol", "已拉黑"}} {
		found := false
		for _, ln := range strings.Split(dump, "\n") {
			ok := true
			for _, w := range row {
				ok = ok && strings.Contains(ln, w)
			}
			found = found || ok
		}
		if !found {
			t.Errorf("no row with %v:\n%s", row, dump)
		}
	}
	if r := ContactRowAt(m, 2); r.Removed != 0 {
		t.Errorf("row 2: %+v", r)
	}
	m.Tab = TabRequests
	if dump := render(m); !strings.Contains(dump, "我是 carol") {
		t.Errorf("requests:\n%s", dump)
	}
	m.Tab = TabPlaza
	m.Plaza = []protocol.User{{ID: "u7", Username: "erin", Gender: "female", Age: 25, Bio: "喜欢猫"}}
	if dump := render(m); !strings.Contains(dump, "erin") || !strings.Contains(dump, "25 岁") || !strings.Contains(dump, "喜欢猫") {
		t.Errorf("plaza:\n%s", dump)
	}
}

func TestMomentsInputs(t *testing.T) {
	m := MomentsModel{Loaded: true, Focused: true, Input: MomentsInput{Kind: InputPost, Text: []rune("hello"), Private: true}}
	dump := screenDump(renderMoments(t, m), pageW, pageH)
	if !strings.Contains(dump, "发动态 · 仅自己可见") || !strings.Contains(dump, "hello") || !strings.Contains(dump, "Tab 可见范围") {
		t.Fatalf("post input:\n%s", dump)
	}
	m.Input = MomentsInput{Kind: InputReply, ReplyName: "carol", Text: []rune("ok")}
	dump = screenDump(renderMoments(t, m), pageW, pageH)
	if !strings.Contains(dump, "回复 carol") {
		t.Fatalf("reply input:\n%s", dump)
	}
	m.Input, m.Confirm = MomentsInput{}, "删除这条动态？y 确认"
	dump = screenDump(renderMoments(t, m), pageW, pageH)
	if !strings.Contains(dump, "删除这条动态") {
		t.Fatalf("confirm:\n%s", dump)
	}
}

// The selection stays in view however far down it is.
func TestMomentsScrollShowsSelected(t *testing.T) {
	var ms []protocol.Moment
	for i := 0; i < 20; i++ {
		ms = append(ms, protocol.Moment{ID: string(rune('a' + i)), Author: uBob, Body: "第" + string(rune('A'+i)) + "条", CreatedAt: int64(1000 - i)})
	}
	m := MomentsModel{Loaded: true, Moments: ms, Focused: true, CommentSel: -1}
	m.Selected[TabTimeline] = 18
	mp := MomentsPage{T: toks()}
	top, h := mp.BlockSpan(m, MomentsContentWidth(pageW), i18n.ZH, 18)
	m.Scroll[TabTimeline] = top + h - MomentsBodyRows(pageH, m)
	dump := screenDump(renderMoments(t, m), pageW, pageH)
	// Block 0 is the compose row, so block 18 is the eighteenth moment.
	if !strings.Contains(dump, "第R条") {
		t.Fatalf("selected moment off screen:\n%s", dump)
	}
}

func TestProfilePage(t *testing.T) {
	p := &protocol.Profile{User: uAlice, Bio: "喜欢猫", CreatedAt: 1700000000000, IsFriend: true, Moments: sampleMoments()[1:]}
	s := newScreen(t, pageW, pageH)
	ProfilePage{T: toks()}.Render(s, widget.Rect{W: pageW, H: pageH}, ProfileModel{Profile: p, Focused: true, Now: 2000}, i18n.ZH)
	dump := screenDump(s, pageW, pageH)
	for _, want := range []string{"🐱", "@alice", "好友", "喜欢猫", "只给自己看", "发消息", "拉黑"} {
		if !strings.Contains(dump, want) {
			t.Errorf("missing %q:\n%s", want, dump)
		}
	}
	if strings.Contains(dump, "加好友") {
		t.Error("offers to add a friend")
	}

	stranger := &protocol.Profile{User: uCarol, Moments: nil}
	s = newScreen(t, pageW, pageH)
	ProfilePage{T: toks()}.Render(s, widget.Rect{W: pageW, H: pageH}, ProfileModel{Profile: stranger, Focused: true}, i18n.ZH)
	dump = screenDump(s, pageW, pageH)
	if !strings.Contains(dump, "不是好友") || !strings.Contains(dump, "加好友") || !strings.Contains(dump, "没有可见的动态") {
		t.Fatalf("stranger:\n%s", dump)
	}
}

func TestProfileEditPreviews(t *testing.T) {
	self := &protocol.Profile{User: uAlice, IsSelf: true}
	m := ProfileModel{Profile: self, Focused: true, Edit: ProfileEdit{Active: true, Field: EditColor,
		Nickname: []rune("新名字"), Bio: []rune("新签名"), Avatar: []rune("★"), Color: "amber"}}
	s := newScreen(t, pageW, pageH)
	ProfilePage{T: toks()}.Render(s, widget.Rect{W: pageW, H: pageH}, m, i18n.ZH)
	dump := screenDump(s, pageW, pageH)
	for _, want := range []string{"编辑资料", "新名字", "新签名", "★", "[琥珀]", "Enter 保存"} {
		if !strings.Contains(dump, want) {
			t.Errorf("missing %q:\n%s", want, dump)
		}
	}
}

// A direct message row starts with the peer's avatar; a one-cell avatar is
// padded so names line up (spec tui-chat-client / 头像显示).
func TestSidebarDMRowShowsAvatar(t *testing.T) {
	st := sidebarState(1, 0)
	var dm *client.Conversation
	for _, cv := range st.Conversations {
		dm = cv
	}
	s := renderSidebar(t, toks(), st, -1, func(m *ListModel) {
		m.Avatars = map[string]protocol.User{strings.ToLower(dm.Name): {Username: dm.Name, Avatar: "Z", Color: "violet"}}
	})
	y := rowOfText(s, dm.Name)
	if y < 0 || !strings.Contains(rowText(s, y, sideW), "· Z  "+dm.Name) {
		t.Fatalf("dm row: %q", rowText(s, y, sideW))
	}
	_, _, style, _ := s.GetContent(2, y)
	if fg, _, _ := style.Decompose(); fg != messageColor(toks(), "violet", tcell.ColorDefault) {
		t.Errorf("avatar not in its colour: %v", fg)
	}

	mono := theme.NewTokens(theme.Ink, theme.ColorMono, true)
	s = renderSidebar(t, mono, st, -1, func(m *ListModel) {
		m.Avatars = map[string]protocol.User{strings.ToLower(dm.Name): {Username: dm.Name, Avatar: "Z", Color: "violet"}}
	})
	if !strings.Contains(rowText(s, rowOfText(s, dm.Name), sideW), "Z  "+dm.Name) {
		t.Fatal("mono terminal lost the avatar glyph")
	}
}

func TestMomentsEntryBadge(t *testing.T) {
	s := renderSidebar(t, toks(), sidebarState(0, 0), -1, func(m *ListModel) { m.MomentsBadge = 3 })
	if y := rowOfText(s, "发现"); !strings.Contains(rowText(s, y, sideW), "3") {
		t.Fatalf("badge: %q", rowText(s, y, sideW))
	}
	s = renderSidebar(t, toks(), sidebarState(0, 0), -1, func(m *ListModel) { m.MomentsDot = true })
	if y := rowOfText(s, "发现"); !strings.Contains(rowText(s, y, sideW), "●") {
		t.Fatalf("dot: %q", rowText(s, y, sideW))
	}
}

func TestTranscriptNameCarriesAvatar(t *testing.T) {
	msgs := []protocol.Message{{ID: "1", AuthorID: "u2", AuthorName: "bob", AuthorAvatar: "B", AuthorColor: "blue", Body: "hi", CreatedAt: 1}}
	items := LayoutMessagesWith(msgs, 60, "me", 100, i18n.ZH, Layout{BubblePercent: 70, Clock24: true})
	found := false
	for _, it := range items {
		if it.Kind == ItemName {
			found = it.Avatar == "B" && it.AvatarColor == "blue" && it.Lines[0] == "bob"
		}
	}
	if !found {
		t.Fatalf("name item: %+v", items)
	}
}

// colOf is the display column where want starts on row y, or -1.
func colOf(s tcell.SimulationScreen, y int, want string) int {
	row := rowText(s, y, pageW)
	i := strings.Index(row, want)
	if i < 0 {
		return -1
	}
	return layout.StringWidth(row[:i])
}

// Every column of the plaza starts at the same place on every row, whatever
// the names' widths, and private values leave their cell blank.
func TestPlazaColumnsAlign(t *testing.T) {
	m := FriendsModel{Tab: TabPlaza, Focused: true, Plaza: []protocol.User{
		{ID: "1", Username: "erin", Nickname: "小爱同学", Gender: "female", Age: 25, Bio: "签名一"},
		{ID: "2", Username: "bo", Gender: "male", Bio: "签名二"},
		{ID: "3", Username: "christopher", Nickname: "Chris", Age: 103, Bio: "签名三"},
	}}
	s := newScreen(t, pageW, pageH)
	FriendsPage{T: toks()}.Render(s, widget.Rect{W: pageW, H: pageH}, m, i18n.ZH)
	var bios []int
	for y := 0; y < pageH; y++ {
		for _, b := range []string{"签名一", "签名二", "签名三"} {
			if c := colOf(s, y, b); c >= 0 {
				bios = append(bios, c)
			}
		}
	}
	if len(bios) != 3 || bios[0] != bios[1] || bios[1] != bios[2] {
		t.Fatalf("signatures start at %v:\n%s", bios, screenDump(s, pageW, pageH))
	}
	yErin, yChris := pageRowWith(s, pageW, pageH, "签名一"), pageRowWith(s, pageW, pageH, "签名三")
	if a, b := colOf(s, yErin, "@erin"), colOf(s, yChris, "@christopher"); a != b || a < 0 {
		t.Fatalf("@usernames at %d and %d", a, b)
	}
	if a, b := colOf(s, yErin, "25"), colOf(s, yChris, "103"); a != b || a < 0 {
		t.Fatalf("ages at %d and %d", a, b)
	}
}

func TestContactsColumnsAlign(t *testing.T) {
	m := FriendsModel{Focused: true,
		Friends:   []protocol.User{{ID: "1", Username: "alice", Nickname: "小爱", Online: true}, {ID: "2", Username: "bartholomew"}},
		RemovedBy: []protocol.User{{ID: "3", Username: "dave"}}}
	s := newScreen(t, pageW, pageH)
	FriendsPage{T: toks()}.Render(s, widget.Rect{W: pageW, H: pageH}, m, i18n.ZH)
	cols := []int{
		colOf(s, pageRowWith(s, pageW, pageH, "小爱"), "在线"),
		colOf(s, pageRowWith(s, pageW, pageH, "bartholomew"), "离线"),
		colOf(s, pageRowWith(s, pageW, pageH, "dave"), "已将你删除"),
	}
	if cols[0] < 0 || cols[0] != cols[1] || cols[1] != cols[2] {
		t.Fatalf("status columns %v:\n%s", cols, screenDump(s, pageW, pageH))
	}
}

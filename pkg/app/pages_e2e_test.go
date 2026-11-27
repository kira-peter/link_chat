package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/client"
	"link-chat/pkg/protocol"
	"link-chat/pkg/tui/views"
)

func TestCreatingAndDiscoveringChannelsEndToEnd(t *testing.T) {
	hs := startServer(t)
	url := "ws" + strings.TrimPrefix(hs.URL, "http") + "/ws"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	start := func(name string) *App {
		a := New(newTestScreen(t, 100, 30), Config{Server: url, Theme: "ink", Language: "zh"}, filepath.Join(appTempDir(t), name+".json"))
		go a.Run(ctx)
		registerAndWait(t, a, ctx, name)
		waitFor(t, 15*time.Second, name+"'s initial list", func() bool {
			a.mu.Lock()
			defer a.mu.Unlock()
			return a.ui.landed
		})
		return a
	}
	alice, bob := start("alice"), start("bob")
	create := func(name, topic string, private bool) string {
		alice.mu.Lock()
		alice.openNewPage(client.SectionChannel)
		typeText := func(text string) {
			for _, r := range text {
				alice.handleKey(ctx, tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
			}
		}
		typeText(name)
		alice.handleKey(ctx, specialKey(tcell.KeyTab))
		typeText(topic)
		alice.handleKey(ctx, specialKey(tcell.KeyTab))
		if private {
			typeText(" ")
		}
		alice.handleKey(ctx, specialKey(tcell.KeyEnter))
		alice.mu.Unlock()
		var id string
		waitFor(t, 15*time.Second, name+" created", func() bool {
			alice.mu.Lock()
			defer alice.mu.Unlock()
			cv := alice.st.Active()
			if cv == nil || cv.Name != name || cv.Topic != topic || (cv.Visibility == protocol.ChannelPrivate) != private {
				return false
			}
			id = cv.ID
			return true
		})
		return id
	}
	private := create("staff", "内部讨论", true)
	public := create("lobby", "欢迎来聊天", false)
	waitFor(t, 15*time.Second, "public channel pushed to Bob's already-open list", func() bool {
		bob.mu.Lock()
		defer bob.mu.Unlock()
		rows := bob.st.SidebarRows()
		i := client.IndexOfConversation(rows, public)
		return i >= 0 && rows[i].Section == client.SectionJoinableChannel
	})
	bob.mu.Lock()
	_, hasPrivate := bob.st.Conversations[private]
	publicTopic := bob.st.Conversations[public].Topic
	bob.mu.Unlock()
	if hasPrivate || publicTopic != "欢迎来聊天" {
		t.Fatalf("Bob's list contains private=%t, public topic=%q", hasPrivate, publicTopic)
	}
	openViaList(t, bob, ctx, public)
	bob.mu.Lock()
	row := bob.selectedSidebarRow()
	bob.mu.Unlock()
	if row.Section != client.SectionChannel || row.Conversation == nil || row.Conversation.ID != public {
		t.Fatalf("joined channel did not move to the channel section: %+v", row)
	}
}

// Against a real server: alice owns two channels; bob signs in and moves down the
// list past both. Neither is joined by being looked at — no "bob joined" appears
// for alice — and only the one he enters is.
func TestBrowsingTheListAgainstARealServer(t *testing.T) {
	hs := startServer(t)
	url := "ws" + strings.TrimPrefix(hs.URL, "http") + "/ws"

	a := New(newTestScreen(t, 100, 30), Config{Server: url, Theme: "ink", Language: "zh"}, filepath.Join(appTempDir(t), "a.json"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.Run(ctx)
	registerAndWait(t, a, ctx, "alice")
	for _, name := range []string{"alpha", "beta"} {
		a.mu.Lock()
		a.ui.NewPage = views.NewPageModel{Value: name}
		a.mu.Unlock()
		a.submitPrompt(ctx, true)
		n := name
		waitFor(t, 15*time.Second, n+" created", func() bool {
			a.mu.Lock()
			defer a.mu.Unlock()
			return a.st.Active() != nil && a.st.Active().Name == n
		})
	}
	ids := map[string]string{}
	a.mu.Lock()
	for id, cv := range a.st.Conversations {
		ids[cv.Name] = id
	}
	a.mu.Unlock()

	scrB := newTestScreen(t, 100, 30)
	b := New(scrB, Config{Server: url, Theme: "ink", Language: "zh"}, filepath.Join(appTempDir(t), "b.json"))
	ctxB, cancelB := context.WithCancel(context.Background())
	defer cancelB()
	go b.Run(ctxB)
	registerAndWait(t, b, ctxB, "bob")
	waitFor(t, 15*time.Second, "bob's list", func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		return len(b.st.Conversations) >= 2
	})
	renderSettled(t, b)

	// Bob walks down past both channels and on into the foot, pausing on each.
	b.mu.Lock()
	b.st.Focus = client.FocusList
	b.ui.Selected = client.FirstSelectable(b.st.SidebarRows())
	b.syncPage()
	b.mu.Unlock()
	// Bob has no direct messages, so the first row is a channel; it is on the
	// right from the start, and two ↓ take him past the other into the foot.
	b.mu.Lock()
	pages := []client.Page{b.page()}
	b.mu.Unlock()
	for i := 0; i < 2; i++ {
		time.Sleep(settleDelay + 50*time.Millisecond)
		b.mu.Lock()
		b.handleKey(ctxB, specialKey(tcell.KeyDown))
		pages = append(pages, b.page())
		b.mu.Unlock()
	}
	time.Sleep(settleDelay + 50*time.Millisecond)
	want := []client.Page{client.PageChannelPreview, client.PageChannelPreview, client.PageFriends}
	for i := range want {
		if pages[i] != want[i] {
			t.Fatalf("bob's pages walking down: %v, want %v", pages, want)
		}
	}

	// Alice has seen nobody join.
	time.Sleep(300 * time.Millisecond)
	a.mu.Lock()
	for _, name := range []string{"alpha", "beta"} {
		for _, m := range a.st.Conversations[ids[name]].Messages {
			if m.Body == "joined" && m.AuthorName == "bob" {
				a.mu.Unlock()
				t.Fatalf("looking at %s made bob join it", name)
			}
		}
	}
	a.mu.Unlock()

	// Bob enters beta: now he joins, and alice sees it.
	b.mu.Lock()
	b.ui.Selected = client.IndexOfConversation(b.st.SidebarRows(), ids["beta"])
	b.syncPage()
	b.handleKey(ctxB, specialKey(tcell.KeyRight))
	b.mu.Unlock()
	waitFor(t, 15*time.Second, "alice to see bob join beta", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		for _, m := range a.st.Conversations[ids["beta"]].Messages {
			if m.Body == "joined" && m.AuthorName == "bob" {
				return true
			}
		}
		return false
	})
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, m := range a.st.Conversations[ids["alpha"]].Messages {
		if m.Body == "joined" && m.AuthorName == "bob" {
			t.Fatal("entering beta also joined alpha")
		}
	}
	_ = protocol.Version
}

// Settings → chat → bubble width → 60% with the preview following, Enter, back to
// a conversation: its bubbles are now capped at 60%, and the config file says so.
func TestChangingBubbleWidthEndToEnd(t *testing.T) {
	hs := startServer(t)
	url := "ws" + strings.TrimPrefix(hs.URL, "http") + "/ws"
	cfgPath := filepath.Join(appTempDir(t), "w.json")
	scr := newTestScreen(t, 100, 34)
	a := New(scr, Config{Server: url, Theme: "ink", Language: "zh"}, cfgPath)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.Run(ctx)
	registerAndWait(t, a, ctx, "wanda")
	a.mu.Lock()
	a.ui.NewPage = views.NewPageModel{Value: "wide"}
	a.mu.Unlock()
	a.submitPrompt(ctx, true)
	waitFor(t, 15*time.Second, "channel", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.st.Active() != nil
	})
	a.mu.Lock()
	a.st.RestoreInput(strings.Repeat("这是一条很长的消息，用来看气泡的最大宽度。", 6))
	a.mu.Unlock()
	a.sendCurrent(ctx)
	waitFor(t, 15*time.Second, "message", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return len(a.st.Active().Messages) > 0
	})
	renderSettled(t, a)

	bubbleWidth := func() int {
		a.mu.Lock()
		defer a.mu.Unlock()
		a.render()
		w, h := scr.Size()
		// Only the transcript's rows: the composer's frame below it is a box too.
		content := views.ContentRect(w, h)
		tr, _ := views.ComposerRect(views.MainRectAt(content, a.foldNow(nowTime())), 1)
		best := 0
		for y := tr.Y; y < tr.Y+tr.H; y++ {
			first, last := -1, -1
			for x := 0; x < w; x++ {
				if r, _, _, _ := scr.GetContent(x, y); r == '┌' {
					first = x
				} else if r == '┐' && first >= 0 {
					last = x
				}
			}
			if first >= 0 && last > first && last-first+1 > best {
				best = last - first + 1
			}
		}
		return best
	}
	// Measured with the list folded both times, so the area is the same width.
	a.mu.Lock()
	a.moveFocus(client.FocusInput)
	a.settleFold()
	a.mu.Unlock()
	before := bubbleWidth()

	a.mu.Lock()
	a.handleKey(ctx, altKey('s'))
	for i, c := range views.Categories() {
		if c.Title.Get(a.lang) == "聊天" {
			a.ui.SettingsNav.Cat = i
		}
	}
	a.handleKey(ctx, specialKey(tcell.KeyRight)) // items
	a.handleKey(ctx, specialKey(tcell.KeyRight)) // wheel on 70%
	a.handleKey(ctx, specialKey(tcell.KeyUp))    // 60%
	lay, _, _ := a.previewLayout()
	// Committing takes the lock itself, as it does when the main loop delivers
	// the key, so it is pressed without holding it.
	a.mu.Unlock()
	a.handleKey(ctx, specialKey(tcell.KeyEnter))
	a.mu.Lock()
	a.handleKey(ctx, specialKey(tcell.KeyLeft)) // categories
	a.handleKey(ctx, specialKey(tcell.KeyLeft)) // the list
	a.ui.Selected = client.IndexOfConversation(a.st.SidebarRows(), a.st.ActiveID)
	a.syncPage()
	a.handleKey(ctx, specialKey(tcell.KeyRight)) // back into the conversation
	a.settleFold()
	a.mu.Unlock()

	if lay.BubblePercent != 60 {
		t.Fatalf("while choosing, the preview showed %d%%, want 60%%", lay.BubblePercent)
	}
	after := bubbleWidth()
	if after >= before {
		t.Fatalf("the bubble did not narrow: %d → %d", before, after)
	}
	w, _ := scr.Size()
	if main := w - 4; after > main*60/100 {
		t.Fatalf("the bubble is %d wide, over 60%% of %d", after, main)
	}
	saved, err := LoadConfig(cfgPath)
	if err != nil || saved.BubbleWidth != 60 {
		t.Fatalf("the config file has %+v (%v)", saved, err)
	}
}

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
	"link-chat/pkg/server"
	"link-chat/pkg/tui/views"
)

// signedInClient starts a client against url and registers user.
func signedInClient(t *testing.T, url, user string) (*App, context.Context) {
	t.Helper()
	scr := newTestScreen(t, 110, 34)
	a := New(scr, Config{Server: url, Theme: "ink", Language: "zh"}, filepath.Join(appTempDir(t), user+".json"))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go a.Run(ctx)
	waitFor(t, 3*time.Second, "login dialog", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.ui.Dialog == views.DialogLogin
	})
	registerAndWait(t, a, ctx, user)
	return a, ctx
}

func (a *App) keyLocked(ctx context.Context, ev *tcell.EventKey) {
	a.mu.Lock()
	a.handleKey(ctx, ev)
	a.mu.Unlock()
	a.markDirty()
}

func (a *App) typeLocked(ctx context.Context, s string) {
	for _, r := range s {
		a.keyLocked(ctx, tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
	}
}

func (a *App) specialLocked(ctx context.Context, k tcell.Key) {
	a.keyLocked(ctx, specialKey(k))
}

func (a *App) altLocked(ctx context.Context, r rune) {
	a.keyLocked(ctx, tcell.NewEventKey(tcell.KeyRune, r, tcell.ModAlt))
}

func (a *App) when(t *testing.T, what string, cond func(*App) bool) {
	t.Helper()
	waitFor(t, 5*time.Second, what, func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return cond(a)
	})
}

// The whole journey on a real server, through the keyboard: add a friend,
// accept, post, see it on the other side, like it, get the notification and
// the badge, comment so a stranger cannot see it, unfriend and lose the moment
// (spec tui-chat-client / 朋友圈页面, moments, friends).
func TestMomentsEndToEnd(t *testing.T) {
	// The test types far faster than a person; the per-connection request
	// budget is for abuse, not for this.
	hs := startServerWith(t, func(c *server.Config) { c.RequestsPerSecond = 1000 })
	url := "ws" + strings.TrimPrefix(hs.URL, "http") + "/ws"
	alice, actx := signedInClient(t, url, "alice")
	bob, bctx := signedInClient(t, url, "bob")
	carol, cctx := signedInClient(t, url, "carol")

	// addFriend types username into the plaza tab's first row and sends it.
	addFriend := func(x *App, ctx context.Context, username, note string) {
		x.mu.Lock()
		x.showEntryPage(client.EntryFriends)
		x.ui.Friends.Tab = views.TabPlaza
		x.ui.Friends.Selected[views.TabPlaza] = 0
		x.mu.Unlock()
		x.typeLocked(ctx, username)
		x.specialLocked(ctx, tcell.KeyEnter)
		x.typeLocked(ctx, note)
		x.specialLocked(ctx, tcell.KeyEnter)
		x.when(t, "request sent", func(a *App) bool { return a.ui.Friends.NoteFor == "" && !a.ui.Friends.Busy })
	}
	// acceptFirst accepts the first request on the requests tab.
	acceptFirst := func(x *App, ctx context.Context) {
		x.mu.Lock()
		x.showEntryPage(client.EntryFriends)
		x.ui.Friends.Tab = views.TabRequests
		x.ui.Friends.Selected[views.TabRequests] = 0
		x.mu.Unlock()
		x.typeLocked(ctx, "a")
	}

	addFriend(alice, actx, "bob", "我是 alice")

	// Bob sees the request at once, on his friends badge, and accepts it.
	bob.when(t, "request arrives live", func(a *App) bool {
		return a.ui.Moments.Unread.Requests == 1 && len(a.ui.Moments.Model.Requests) == 1 && a.ui.Moments.Model.Requests[0].Note == "我是 alice"
	})
	acceptFirst(bob, bctx)
	bob.when(t, "alice is a friend", func(a *App) bool { return len(a.ui.Moments.Model.Friends) == 1 })
	alice.when(t, "bob is a friend", func(a *App) bool { return len(a.ui.Moments.Model.Friends) == 1 })

	// Carol befriends alice too, but not bob.
	addFriend(carol, cctx, "alice", "")
	alice.when(t, "carol's request", func(a *App) bool { return a.ui.Moments.Unread.Requests == 1 })
	acceptFirst(alice, actx)
	alice.when(t, "two friends", func(a *App) bool { return len(a.ui.Moments.Model.Friends) == 2 })

	for _, x := range []struct {
		a   *App
		ctx context.Context
	}{{alice, actx}, {bob, bctx}, {carol, cctx}} {
		x.a.altLocked(x.ctx, 'p')
		x.a.when(t, "moments page", func(a *App) bool { return a.page() == client.PageMoments && a.ui.Moments.Model.Loaded })
	}

	// Alice posts from the timeline's compose row.
	alice.when(t, "on the compose row", func(a *App) bool {
		return a.ui.Moments.Model.Tab == views.TabTimeline && a.ui.Moments.Model.Selected[views.TabTimeline] == 0
	})
	alice.specialLocked(actx, tcell.KeyEnter)
	alice.typeLocked(actx, "第一条动态")
	alice.specialLocked(actx, tcell.KeyEnter)
	alice.when(t, "posted", func(a *App) bool { return len(a.ui.Moments.Model.Moments) == 1 })

	// Bob's timeline gets it pushed; he likes it.
	bob.when(t, "moment arrives", func(a *App) bool {
		return len(a.ui.Moments.Model.Moments) == 1 && a.ui.Moments.Model.Moments[0].Body == "第一条动态"
	})
	bob.specialLocked(bctx, tcell.KeyDown)
	bob.when(t, "on the moment", func(a *App) bool { return TimelineSelected(&a.ui.Moments.Model) == 0 })
	bob.typeLocked(bctx, "l")
	bob.when(t, "liked", func(a *App) bool { return a.ui.Moments.Model.Moments[0].Liked })

	// Alice gets a notification. She is on the timeline, so it counts.
	alice.when(t, "like notification", func(a *App) bool {
		return a.ui.Moments.Unread.Notifications == 1 && len(a.ui.Moments.Model.Moments[0].Likes) == 1
	})
	alice.mu.Lock()
	badge := alice.listModel(client.FocusList).MomentsBadge
	alice.mu.Unlock()
	if badge != 1 {
		t.Fatalf("alice's badge: %d", badge)
	}

	// Carol comments; bob (not carol's friend) must not see it, alice must.
	carol.when(t, "carol's timeline", func(a *App) bool {
		return a.ui.Moments.Model.Tab == views.TabTimeline && len(a.ui.Moments.Model.Moments) == 1
	})
	carol.specialLocked(cctx, tcell.KeyDown)
	carol.typeLocked(cctx, "c")
	carol.typeLocked(cctx, "carol 的评论")
	carol.specialLocked(cctx, tcell.KeyEnter)
	alice.when(t, "carol's comment", func(a *App) bool { return len(a.ui.Moments.Model.Moments[0].Comments) == 1 })
	time.Sleep(300 * time.Millisecond)
	bob.mu.Lock()
	bobSees := len(bob.ui.Moments.Model.Moments[0].Comments)
	bob.mu.Unlock()
	if bobSees != 0 {
		t.Fatal("bob sees a comment from someone who is not his friend")
	}
	// Carol, who is not bob's friend, cannot see that bob liked it either.
	carol.mu.Lock()
	carolLikes := len(carol.ui.Moments.Model.Moments[0].Likes)
	carol.mu.Unlock()
	if carolLikes != 0 {
		t.Fatal("carol sees bob's like")
	}

	// The screen shows it the way the user reads it.
	alice.mu.Lock()
	alice.render()
	text := screenText(alice.scr.(tcell.SimulationScreen))
	alice.mu.Unlock()
	for _, want := range []string{"第一条动态", "♥ bob", "carol：carol 的评论"} {
		if !strings.Contains(text, want) {
			t.Errorf("alice's screen lacks %q:\n%s", want, text)
		}
	}

	// Alice removes bob from the friends page: his timeline loses the moment
	// and his friends page shows that she removed him.
	alice.mu.Lock()
	alice.showEntryPage(client.EntryFriends)
	alice.ui.Friends.Tab = views.TabContacts
	for i, u := range alice.ui.Moments.Model.Friends {
		if u.Username == "bob" {
			alice.ui.Friends.Selected[views.TabContacts] = i
		}
	}
	alice.mu.Unlock()
	alice.typeLocked(actx, "d")
	alice.typeLocked(actx, "y")
	bob.when(t, "moment gone", func(a *App) bool {
		return len(a.ui.Moments.Model.Moments) == 0 && len(a.ui.Moments.Model.Friends) == 0 && len(a.ui.Moments.Model.RemovedBy) == 1
	})
}

// Alice sets her avatar; bob's direct message row and her message both carry it.
func TestAvatarReachesTheOtherSide(t *testing.T) {
	hs := startServerWith(t, func(c *server.Config) { c.RequestsPerSecond = 1000 })
	url := "ws" + strings.TrimPrefix(hs.URL, "http") + "/ws"
	alice, actx := signedInClient(t, url, "alice")
	bob, _ := signedInClient(t, url, "bob")

	// Alice edits her own profile: Alt+P, p, e.
	alice.altLocked(actx, 'p')
	alice.typeLocked(actx, "p")
	alice.when(t, "own profile", func(a *App) bool { p := a.ui.Moments.Profile.Profile; return p != nil && p.IsSelf })
	alice.typeLocked(actx, "e")
	alice.typeLocked(actx, "小爱")
	alice.specialLocked(actx, tcell.KeyDown)
	alice.specialLocked(actx, tcell.KeyDown)
	alice.typeLocked(actx, "★")
	alice.specialLocked(actx, tcell.KeyEnter)
	alice.when(t, "saved", func(a *App) bool {
		return !a.ui.Moments.Profile.Edit.Active && a.ui.Moments.Profile.Profile.User.Avatar == "★"
	})

	// A direct message needs a friendship: make them friends first.
	for _, c := range []struct {
		a    *App
		user string
	}{{alice, "bob"}, {bob, "alice"}} {
		c.a.socialCall(protocol.OpFriendRequest, protocol.FriendRequestReq{Username: c.user})
	}
	alice.mu.Lock()
	alice.ui.NewPage = views.NewPageModel{Value: "bob"}
	alice.mu.Unlock()
	alice.submitPrompt(actx, false)
	alice.when(t, "dm open", func(a *App) bool { return a.st.Active() != nil && a.st.Active().Kind == protocol.KindDM })
	alice.typeLocked(actx, "hi bob")
	alice.specialLocked(actx, tcell.KeyEnter)

	// Bob had never opened this direct message: it reaches him by refreshing
	// his list and opening it, the way the sidebar does.
	alice.when(t, "message sent", func(a *App) bool {
		cv := a.st.Active()
		return cv != nil && len(cv.Messages) == 1 && cv.Messages[0].ID != ""
	})
	bob.refreshConversations()
	var dmID string
	bob.when(t, "dm listed", func(a *App) bool {
		for _, cv := range a.st.Conversations {
			if cv.Kind == protocol.KindDM {
				dmID = cv.ID
				return true
			}
		}
		return false
	})
	bob.mu.Lock()
	bob.openConversation(dmID)
	bob.mu.Unlock()
	bob.when(t, "message arrived", func(a *App) bool {
		cv := a.st.Conversations[dmID]
		for _, m := range cv.Messages {
			if m.Body == "hi bob" {
				return m.AuthorAvatar == "★"
			}
		}
		return false
	})
	bob.refreshUsers()
	bob.when(t, "alice's avatar known", func(a *App) bool {
		for _, u := range a.st.Users {
			if u.Username == "alice" {
				return u.Avatar == "★" && u.Nickname == "小爱"
			}
		}
		return false
	})
	renderSettled(t, bob)
	bob.mu.Lock()
	defer bob.mu.Unlock()
	if text := screenText(bob.scr.(tcell.SimulationScreen)); !strings.Contains(text, "★  alice") {
		t.Fatalf("bob's sidebar lacks alice's avatar:\n%s", text)
	}
}

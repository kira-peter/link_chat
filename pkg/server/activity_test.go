package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"link-chat/pkg/protocol"
	"link-chat/pkg/storage"
)

func TestLiveActivityVisibility(t *testing.T) {
	hs, srv, root, _ := adminFixture(t)
	stats := newAdminClient(t, hs)
	chat := newAdminClient(t, hs)
	for _, account := range []struct {
		name string
		perm string
		api  *adminClient
	}{
		{"stats-user", PermStatsView, stats},
		{"chat-user", PermChatView, chat},
	} {
		id := makeUser(t, hs, account.name)
		if err := srv.st.SetUserPerms(id, []string{account.perm}); err != nil {
			t.Fatal(err)
		}
		if status, body := account.api.login(account.name); status != http.StatusOK {
			t.Fatalf("login %s: %d %v", account.name, status, body)
		}
	}
	const snapshot = "/admin/api/live/snapshot"
	for _, endpoint := range []string{snapshot, "/admin/api/live/stats"} {
		if status, _ := newAdminClient(t, hs).get(endpoint); status != http.StatusUnauthorized {
			t.Fatalf("anonymous %s: %d", endpoint, status)
		}
		if status, body := chat.get(endpoint); status != http.StatusForbidden || code(body) != protocol.CodeForbidden {
			t.Fatalf("chat-only %s: %d %v", endpoint, status, body)
		}
	}

	fullStream, statsStream := root.openEvents(), stats.openEvents()
	local := srv.SubscribeActivity(t.Context())
	alice := dial(t, hs)
	alice.register("alice", pw)
	const name = "private-label"
	const body = "message body for chat.view only"
	var channel protocol.Conversation
	alice.callOK(protocol.OpChannelCreate, protocol.ChannelCreateReq{Name: name}, &channel)
	alice.callOK(protocol.OpMessageSend, protocol.SendMessageReq{ConversationID: channel.ID, Body: body}, nil)

	full := awaitLiveStream(t, fullStream, "message")
	if full.Content != body || full.Channel == nil || full.Channel.Name != name {
		t.Fatalf("full activity: %+v", full)
	}
	redacted := awaitLiveStream(t, statsStream, "message")
	if redacted.Content != "" || redacted.Channel == nil || redacted.Channel.Name != "" ||
		redacted.Channel.ID != channel.ID || strings.Contains(redacted.Summary, name) {
		t.Fatalf("stats-only activity: %+v", redacted)
	}
	localMessage := awaitLocalActivity(t, local, "message")
	if localMessage.Actor.Name != "alice" || localMessage.Content != "" || localMessage.Channel == nil || localMessage.Channel.Name != name {
		t.Fatalf("local activity: %+v", localMessage)
	}

	for _, account := range []struct {
		api  *adminClient
		full bool
	}{{root, true}, {stats, false}} {
		status, raw := account.api.get(snapshot)
		if status != http.StatusOK {
			t.Fatalf("snapshot: %d %v", status, raw)
		}
		encoded, err := json.Marshal(raw)
		if err != nil {
			t.Fatal(err)
		}
		var snap LiveSnapshot
		if err := json.Unmarshal(encoded, &snap); err != nil {
			t.Fatal(err)
		}
		if len(snap.Activity) != 4 || snap.Sequence < full.Sequence ||
			snap.Activity[ActivityMessage].Total != 1 || snap.Stats.MessagesMinute != 1 {
			t.Fatalf("snapshot totals: %+v", snap)
		}
		if strings.Contains(string(encoded), body) || strings.Contains(string(encoded), name) != account.full {
			t.Fatalf("snapshot exposed the wrong fields (full=%v): %s", account.full, encoded)
		}
		if len(snap.Channels) != 1 || (len(snap.Channels[0].Members) != 0) != account.full {
			t.Fatalf("snapshot membership (full=%v): %+v", account.full, snap.Channels)
		}
	}
}

func TestLiveActivitiesFromChat(t *testing.T) {
	users := newSocial(t, "alice", "bob")
	activity := users.srv.SubscribeActivity(t.Context())
	users.befriend("alice", "bob")
	friend := awaitLocalActivity(t, activity, "friend.accepted")
	if friend.Category != ActivitySocial || friend.Actor.Name != "bob" || friend.Target == nil || friend.Target.Name != "alice" {
		t.Fatalf("friend activity: %+v", friend)
	}

	moment := users.post("alice", "private post body", protocol.VisibilityFriends)
	awaitLocalActivity(t, activity, "moment.posted")
	users.c["bob"].callOK(protocol.OpMomentLike, protocol.MomentLikeReq{MomentID: moment.ID, Like: true}, nil)
	like := awaitLocalActivity(t, activity, "moment.liked")
	if like.Category != ActivitySocial || like.Actor.Name != "bob" || like.Target == nil ||
		like.Target.Name != "alice" || strings.Contains(like.Summary, "private post body") {
		t.Fatalf("like activity: %+v", like)
	}

	var dm protocol.Conversation
	users.c["alice"].callOK(protocol.OpDMOpen, protocol.DMOpenReq{Username: "bob"}, &dm)
	users.c["alice"].callOK(protocol.OpMessageSend, protocol.SendMessageReq{ConversationID: dm.ID, Body: "hello"}, nil)
	message := awaitLocalActivity(t, activity, "message")
	if message.Category != ActivityMessage || message.Actor.Name != "alice" || message.Target == nil ||
		message.Target.Name != "bob" || message.Channel != nil || message.Summary != "alice → bob" || message.Content != "" {
		t.Fatalf("private message activity: %+v", message)
	}
}

func TestLiveActivityQueueKeepsLatestAndExactTotals(t *testing.T) {
	_, srv := testServer(t)
	local := srv.SubscribeActivity(t.Context())
	for i := range cap(local) + 4 {
		srv.activity(fmt.Sprintf("system.%d", i), ActivitySystem, "", "", storage.Conversation{}, "")
	}
	if first := <-local; first.Sequence != 5 {
		t.Fatalf("oldest queued sequence = %d, want 5", first.Sequence)
	}
	snap, err := srv.LiveSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	panel := snap.Activity[ActivitySystem]
	if panel.Total != uint64(cap(local)+4) || len(panel.Recent) != ActivityPreviewLimit {
		t.Fatalf("system total or recent window: %+v", panel)
	}
}

func TestLiveChannelFocusAndRelationships(t *testing.T) {
	hs, srv, root, _ := adminFixture(t)
	statsID := makeUser(t, hs, "observer")
	if err := srv.st.SetUserPerms(statsID, []string{PermStatsView}); err != nil {
		t.Fatal(err)
	}
	stats := newAdminClient(t, hs)
	if status, body := stats.login("observer"); status != http.StatusOK {
		t.Fatalf("stats login: %d %v", status, body)
	}
	alice, bob := dial(t, hs), dial(t, hs)
	aliceID := alice.register("alice", pw).User.ID
	bobID := bob.register("bob", pw).User.ID
	makeFriends(alice, bob, "alice", "bob")
	var alpha, beta protocol.Conversation
	alice.callOK(protocol.OpChannelCreate, protocol.ChannelCreateReq{Name: "alpha"}, &alpha)
	alice.callOK(protocol.OpChannelCreate, protocol.ChannelCreateReq{Name: "beta"}, &beta)
	bob.callOK(protocol.OpChannelJoin, protocol.ConversationRef{ConversationID: alpha.ID}, nil)
	allowed, limited := root.openEvents(), stats.openEvents()

	report := func(c *client, id *string) {
		c.callOK(protocol.OpPresencePing, protocol.PingReq{Nonce: 17, ViewedChannel: id}, nil)
	}
	expectFocus := func(channel string) {
		event := allowed.next("channel.focus", 5*time.Second)
		data := event["data"].(map[string]any)
		if data["user_id"] != aliceID || data["channel_id"] != channel {
			t.Fatalf("channel focus: %+v, want %s in %q", data, aliceID, channel)
		}
	}
	report(alice, &alpha.ID)
	expectFocus(alpha.ID)
	report(alice, nil)
	report(bob, &beta.ID)

	const path = "/admin/api/live/snapshot"
	getSnapshot := func(api *adminClient) LiveSnapshot {
		status, raw := api.get(path)
		if status != http.StatusOK {
			t.Fatalf("snapshot: %d %v", status, raw)
		}
		data, err := json.Marshal(raw)
		if err != nil {
			t.Fatal(err)
		}
		var snap LiveSnapshot
		if err := json.Unmarshal(data, &snap); err != nil {
			t.Fatal(err)
		}
		return snap
	}
	viewing := func(snap LiveSnapshot, uid string) string {
		for _, user := range snap.Users {
			if user.ID == uid {
				return user.ViewingChannel
			}
		}
		t.Fatalf("snapshot missing user %s", uid)
		return ""
	}
	full := getSnapshot(root)
	if viewing(full, aliceID) != alpha.ID || viewing(full, bobID) != "" {
		t.Fatal("omitting the field changed focus, or a nonmember focused a channel")
	}
	redacted := getSnapshot(stats)
	if redacted.ChatDetails || redacted.FocusSequence != 0 || viewing(redacted, aliceID) != "" {
		t.Fatalf("stats snapshot exposed channel focus: %+v", redacted)
	}
	for _, snap := range []LiveSnapshot{full, redacted} {
		index := slices.IndexFunc(snap.Channels, func(ch LiveChannel) bool { return ch.ID == alpha.ID })
		if index == -1 {
			t.Fatal("alpha missing from snapshot")
		}
		channel := snap.Channels[index]
		if channel.MemberCount != 2 || channel.OnlineCount != 2 ||
			(snap.ChatDetails && !slices.Contains(channel.Members, bobID)) ||
			(!snap.ChatDetails && len(channel.Members) != 0) {
			t.Fatalf("channel roster or counts: %+v", channel)
		}
	}
	friendsPath := "/admin/api/live/users/" + aliceID + "/friends"
	if status, result := root.get(friendsPath); status != http.StatusOK ||
		!slices.Equal(result["friends"].([]any), []any{bobID}) {
		t.Fatalf("friend links: %d %+v", status, result)
	}
	if status, result := stats.get(friendsPath); status != http.StatusForbidden || code(result) != protocol.CodeForbidden {
		t.Fatalf("restricted friend links: %d %+v", status, result)
	}

	report(alice, &beta.ID)
	expectFocus(beta.ID)
	outside := ""
	report(alice, &outside)
	expectFocus("")
	report(alice, &alpha.ID)
	expectFocus(alpha.ID)
	_ = alice.ws.CloseNow()
	expectFocus("")
	allowed.next("offline", 5*time.Second)

	// The offline event is queued after clearing focus. Check all the preceding
	// events on the restricted stream, including every focus transition above.
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case event := <-limited.evs:
			switch event["type"] {
			case "channel.focus":
				t.Fatalf("stats stream exposed channel focus: %+v", event)
			case "offline":
				snap := getSnapshot(stats)
				for _, channel := range snap.Channels {
					if channel.ID == alpha.ID && channel.MemberCount == 2 && channel.OnlineCount == 1 {
						return
					}
				}
				t.Fatalf("offline player did not update online count: %+v", snap.Channels)
			}
		case <-timeout.C:
			t.Fatal("missing offline event")
		}
	}
}

func awaitLiveStream(t *testing.T, stream *sseReader, kind string) Activity {
	t.Helper()
	for {
		payload, err := json.Marshal(stream.next("activity", 5*time.Second)["data"])
		if err != nil {
			t.Fatal(err)
		}
		var activity Activity
		if err := json.Unmarshal(payload, &activity); err != nil {
			t.Fatal(err)
		}
		if activity.Kind == kind {
			return activity
		}
	}
}

func awaitLocalActivity(t *testing.T, events <-chan Activity, kind string) Activity {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case activity, ok := <-events:
			if !ok {
				t.Fatalf("activity stream closed while waiting for %s", kind)
			}
			if activity.Kind == kind {
				return activity
			}
		case <-timer.C:
			t.Fatalf("missing activity %s", kind)
		}
	}
}

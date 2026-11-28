package server

import (
	"errors"
	"io"
	"net/http"
	"slices"
	"testing"
	"time"

	"link-chat/pkg/protocol"
	"link-chat/pkg/storage"
)

func TestSimulationRunsNormalChatAndCleansOneAccount(t *testing.T) {
	if len(simulationDefaultTexts) != 100 {
		t.Fatalf("default simulation corpus has %d lines", len(simulationDefaultTexts))
	}
	hs, srv := testServerWith(t, func(c *Config) { c.SimulationEnabled = true }, io.Discard)
	rootID := makeUser(t, hs, "root")
	if err := srv.st.SetUserSuper(rootID, true); err != nil {
		t.Fatal(err)
	}
	admin := newAdminClient(t, hs)
	if status, body := admin.login("root"); status != http.StatusOK || body["simulation"] != true {
		t.Fatalf("simulation availability: %d %+v", status, body)
	}
	config := simulationConfig{
		Prefix: "scene", Count: 3, Duration: 12, Friends: 2, OwnChannel: true, JoinMin: 2, JoinMax: 2,
		Actions: map[string]simulationRate{"dm": {1, 1}, "channel_message": {1, 1}, "focus": {1, 1}, "post": {1, 1}, "comment": {1, 1}, "like": {1, 1}},
	}
	if status, body := admin.post("/admin/api/simulation/start", config); status != http.StatusAccepted {
		t.Fatalf("start simulation: %d %+v", status, body)
	}
	if status, _ := admin.post("/admin/api/simulation/start", config); status != http.StatusConflict {
		t.Fatalf("concurrent start = %d, want 409", status)
	}
	deadline := time.Now().Add(8 * time.Second)
	for {
		srv.simulation.mu.Lock()
		got := srv.simulation.run.status.OK
		ready := got["dm"] > 0 && got["channel_message"] > 0 && got["focus"] > 0 &&
			got["post"] > 0 && got["comment"] > 0 && got["like"] > 0 && got["friendship"] == 3 && got["join_channel"] == 6
		srv.simulation.mu.Unlock()
		if ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("simulation did not exercise all configured operations")
		}
		time.Sleep(30 * time.Millisecond)
	}

	victim, err := srv.st.UserByUsernameKey("scene_1")
	if err != nil {
		t.Fatal(err)
	}
	owned, err := srv.st.ChannelByNameKey("scene_1-room")
	if err != nil {
		t.Fatal(err)
	}
	accounts, err := srv.st.SimulationUsers()
	if err != nil {
		t.Fatal(err)
	}
	var authors []string
	for _, user := range accounts {
		authors = append(authors, user.ID)
	}
	page, err := srv.st.MomentsBy(authors, victim.ID, storage.MomentCursor{}, 20)
	if err != nil || len(page.Moments) == 0 {
		t.Fatalf("could not read simulated posts: %v", err)
	}
	for _, moment := range page.Moments {
		if !slices.Contains(simulationDefaultTexts, moment.Body) {
			t.Fatalf("post missing from default corpus: %q", moment.Body)
		}
	}
	empty := storage.Conversation{ID: "c_empty", Kind: protocol.KindChannel, Name: "empty", CreatedBy: victim.ID}
	if err := srv.st.CreateConversation(empty); err != nil {
		t.Fatal(err)
	}
	stream := admin.openEvents()
	path := "/admin/api/users/" + victim.ID
	if status, _ := admin.do(http.MethodDelete, path, map[string]any{"username": "wrong"}); status != http.StatusBadRequest {
		t.Fatalf("wrong confirmation = %d", status)
	}
	if status, body := admin.do(http.MethodDelete, path, map[string]any{"username": victim.Username}); status != http.StatusOK {
		t.Fatalf("delete: %d %+v", status, body)
	}
	cutoff := time.Now().Add(3 * time.Second)
	sawDestroyedChannel := false
	for {
		event := stream.next("activity", time.Until(cutoff))["data"].(map[string]any)
		if event["kind"] == "channel.deleted" {
			if channelID := event["channel"].(map[string]any)["id"]; channelID != empty.ID {
				t.Fatalf("unexpected channel destruction: %v", channelID)
			}
			sawDestroyedChannel = true
		}
		if event["kind"] == "account.deleted" {
			if actor := event["actor"].(map[string]any); actor["id"] != victim.ID || actor["name"] != victim.Username {
				t.Fatalf("wrong deletion activity: %+v", event)
			}
			break
		}
	}
	if !sawDestroyedChannel {
		t.Fatal("empty channel destruction was not reported before account deletion")
	}
	event := stream.next("account.deleted", 2*time.Second)
	if event["data"].(map[string]any)["user_id"] != victim.ID {
		t.Fatalf("wrong account deleted: %+v", event)
	}
	if _, err := srv.st.UserByID(victim.ID); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("deleted account: %v", err)
	}
	channel, err := srv.st.ConversationByID(owned.ID)
	if err != nil || channel.CreatedBy == victim.ID || srv.st.IsMember(owned.ID, victim.ID) {
		t.Fatalf("channel not transferred: %+v, %v", channel, err)
	}
	snapshot, err := srv.LiveSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	for _, user := range snapshot.Users {
		if user.ID == victim.ID {
			t.Fatal("deleted account remains visible in live snapshot")
		}
	}
	if status, _ := admin.post("/admin/api/simulation/stop", struct{}{}); status != http.StatusOK {
		t.Fatalf("stop simulation: %d", status)
	}
	select {
	case <-srv.stopSimulation():
	case <-time.After(3 * time.Second):
		t.Fatal("simulation kept running after stop")
	}
	users, err := srv.st.SimulationUsers()
	if err != nil || len(users) != 2 {
		t.Fatalf("remaining simulation accounts: %+v, %v", users, err)
	}
	if status, body := admin.do(http.MethodDelete, "/admin/api/users/"+rootID, map[string]any{"username": "root"}); status != http.StatusForbidden || code(body) != protocol.CodeForbidden {
		t.Fatalf("super admin should be protected: %d %+v", status, body)
	}
}

func TestSimulationIsDisabledByDefault(t *testing.T) {
	_, srv, admin, _ := adminFixture(t)
	if status, _ := admin.get("/admin/api/simulation"); status != http.StatusNotFound {
		t.Fatalf("simulation API registered by default: %d", status)
	}
	srv.SetDashboardSimulation(true)
	if url, enabled := srv.DashboardSimulation(); url != "http://127.0.0.1:8100/admin/" || !enabled {
		t.Fatalf("dashboard setting: %q enabled=%t", url, enabled)
	}
	if status, body := admin.get("/admin/api/me"); status != http.StatusOK || body["simulation"] != true {
		t.Fatalf("simulation availability after enabling: %d %+v", status, body)
	}
	if status, _ := admin.get("/admin/api/simulation"); status != http.StatusOK {
		t.Fatalf("simulation did not open without restarting: %d", status)
	}
	if status, body := admin.post("/admin/api/simulation/start", simulationConfig{Prefix: "toggle", Count: 1, Duration: 3600}); status != http.StatusAccepted {
		t.Fatalf("start after enabling: %d %+v", status, body)
	}
	srv.SetDashboardSimulation(false)
	if status, _ := admin.get("/admin/api/simulation"); status != http.StatusNotFound {
		t.Fatalf("simulation did not close without restarting: %d", status)
	}
	select {
	case <-srv.simulation.run.done:
	case <-time.After(3 * time.Second):
		t.Fatal("disabling simulation did not stop the run")
	}
}

func TestSimulationCreatesOnePublicChannelPerGroup(t *testing.T) {
	hs, srv := testServerWith(t, func(c *Config) { c.SimulationEnabled = true }, io.Discard)
	rootID := makeUser(t, hs, "root")
	if err := srv.st.SetUserSuper(rootID, true); err != nil {
		t.Fatal(err)
	}
	admin := newAdminClient(t, hs)
	if status, body := admin.login("root"); status != http.StatusOK {
		t.Fatalf("login: %d %+v", status, body)
	}
	config := simulationConfig{Prefix: "group", Count: 7, Duration: 10, OwnChannel: true, UsersPerChannel: 3, JoinMin: 1, JoinMax: 1}
	if status, body := admin.post("/admin/api/simulation/start", config); status != http.StatusAccepted {
		t.Fatalf("start grouped simulation: %d %+v", status, body)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		srv.simulation.mu.Lock()
		status := srv.simulation.run.status
		created, joined := status.OK["create_channel"], status.OK["join_channel"]
		srv.simulation.mu.Unlock()
		if status.Phase == "running" {
			if created != 3 || joined != 7 || status.Failed != 0 {
				t.Fatalf("group setup created %d channels, joined %d times, with %d failures", created, joined, status.Failed)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("grouped simulation did not finish setup")
		}
		time.Sleep(20 * time.Millisecond)
	}
	for _, name := range []string{"group_1", "group_4", "group_7"} {
		user, err := srv.st.UserByUsernameKey(name)
		if err != nil {
			t.Fatal(err)
		}
		channel, err := srv.st.ChannelByNameKey(name + "-room")
		if err != nil || channel.CreatedBy != user.ID || channel.Visibility != protocol.ChannelPublic {
			t.Fatalf("public channel owned by %s: %+v, %v", name, channel, err)
		}
	}
}

func TestSimulationRemovesOnlyParticipatingFriends(t *testing.T) {
	hs, srv := testServerWith(t, func(c *Config) { c.SimulationEnabled = true }, io.Discard)
	rootID := makeUser(t, hs, "root")
	if err := srv.st.SetUserSuper(rootID, true); err != nil {
		t.Fatal(err)
	}
	outsider := makeUser(t, hs, "outsider")
	if _, err := srv.st.EnsureSimulationUsers([]storage.User{
		{ID: "a", Username: "unfriend_1"}, {ID: "b", Username: "unfriend_2"},
	}, "previous"); err != nil {
		t.Fatal(err)
	}
	for _, peer := range []string{"b", outsider} {
		if err := srv.st.AddFriendship("a", peer, storage.Now()); err != nil {
			t.Fatal(err)
		}
	}
	admin := newAdminClient(t, hs)
	if status, body := admin.login("root"); status != http.StatusOK {
		t.Fatalf("login: %d %+v", status, body)
	}
	stream := admin.openEvents()
	config := simulationConfig{Prefix: "unfriend", Count: 2, Duration: 5,
		Actions: map[string]simulationRate{"friend_remove": {1, 1}}}
	if status, body := admin.post("/admin/api/simulation/start", config); status != http.StatusAccepted {
		t.Fatalf("start removal simulation: %d %+v", status, body)
	}
	a := awaitLiveStream(t, stream, "friend.removed")
	if a.Target == nil || !((a.Actor.ID == "a" && a.Target.ID == "b") || (a.Actor.ID == "b" && a.Target.ID == "a")) {
		t.Fatalf("deletion outside simulation: %+v", a)
	}
	if present, err := srv.st.AreFriends("a", "b"); err != nil || present {
		t.Fatalf("simulated friendship still exists: %v", err)
	}
	if present, err := srv.st.AreFriends("a", outsider); err != nil || !present {
		t.Fatalf("external friendship changed: %v", err)
	}
}

func TestSimulationPresencePausesAndResumesActions(t *testing.T) {
	hs, srv := testServerWith(t, func(c *Config) { c.SimulationEnabled = true }, io.Discard)
	rootID := makeUser(t, hs, "root")
	if err := srv.st.SetUserSuper(rootID, true); err != nil {
		t.Fatal(err)
	}
	admin := newAdminClient(t, hs)
	if status, body := admin.login("root"); status != http.StatusOK {
		t.Fatalf("login: %d %+v", status, body)
	}
	stream := admin.openEvents()
	config := simulationConfig{Prefix: "presence-test", Count: 1, Duration: 8,
		Actions: map[string]simulationRate{"presence": {2, 2}, "channel_message": {1, 1}}, OwnChannel: true}
	if status, body := admin.post("/admin/api/simulation/start", config); status != http.StatusAccepted {
		t.Fatalf("start presence simulation: %d %+v", status, body)
	}
	online := awaitLiveStream(t, stream, "online")
	if online.Actor.Name != "presence-test_1" {
		t.Fatalf("unexpected online event: %+v", online)
	}
	if a := awaitLiveStream(t, stream, "offline"); a.Actor.ID != online.Actor.ID {
		t.Fatalf("unexpected offline event: %+v", a)
	}
	deadline := time.Now().Add(4 * time.Second)
	for {
		a := stream.next("activity", time.Until(deadline))["data"].(map[string]any)
		if a["kind"] == "message" {
			t.Fatal("offline simulator sent a message")
		}
		if a["kind"] == "online" {
			if a["actor"].(map[string]any)["id"] != online.Actor.ID {
				t.Fatalf("unexpected reconnect: %+v", a)
			}
			break
		}
	}
	if resumed := awaitLiveStream(t, stream, "message"); resumed.Actor.ID != online.Actor.ID {
		t.Fatalf("reconnected simulator did not resume sending: %+v", resumed)
	}
}

func TestSimulationMessageTimingUsesSubsecondPhases(t *testing.T) {
	spread := simulationRate{Min: 2, Max: 3}
	var fractional int
	for range 100 {
		delay := spread.delay()
		if delay < 2*time.Second || delay > 3*time.Second || delay%time.Millisecond != 0 {
			t.Fatalf("delay outside millisecond range: %v", delay)
		}
		if delay%time.Second != 0 {
			fractional++
		}
	}
	if fractional < 90 {
		t.Fatalf("only %d random delays used fractional seconds", fractional)
	}
	if fixed := (simulationRate{Min: 1, Max: 1}).delay(); fixed != time.Second {
		t.Fatalf("fixed recurrence changed: %v", fixed)
	}

	hs, srv := testServerWith(t, func(c *Config) { c.SimulationEnabled = true }, io.Discard)
	rootID := makeUser(t, hs, "root")
	if err := srv.st.SetUserSuper(rootID, true); err != nil {
		t.Fatal(err)
	}
	admin := newAdminClient(t, hs)
	if status, body := admin.login("root"); status != http.StatusOK {
		t.Fatalf("login: %d %+v", status, body)
	}
	stream := admin.openEvents()
	const users = 16
	config := simulationConfig{Prefix: "timing", Count: users, Duration: 5, OwnChannel: true,
		Actions: map[string]simulationRate{"channel_message": {1, 1}}}
	if status, body := admin.post("/admin/api/simulation/start", config); status != http.StatusAccepted {
		t.Fatalf("start timing simulation: %d %+v", status, body)
	}
	seen := make(map[string]bool)
	var first, last int64
	var example []string
	for len(seen) < users {
		event := awaitLiveStream(t, stream, "message")
		if seen[event.Actor.ID] {
			continue
		}
		seen[event.Actor.ID] = true
		if first == 0 {
			first = event.At
		}
		last = event.At
		if len(example) < 4 {
			example = append(example, time.UnixMilli(event.At).Format("15:04:05.000"))
		}
	}
	if last-first < 250 {
		t.Fatalf("first messages from %d users clustered into %dms: %v", users, last-first, example)
	}
	t.Logf("first messages from %d fixed-rate actors spread over %dms: %v", users, last-first, example)
}

func TestSimulationAtTenThousandUsers(t *testing.T) {
	if testing.Short() {
		t.Skip("large simulation")
	}
	hs, srv := testServerWith(t, func(c *Config) { c.SimulationEnabled = true }, io.Discard)
	rootID := makeUser(t, hs, "root")
	if err := srv.st.SetUserSuper(rootID, true); err != nil {
		t.Fatal(err)
	}
	admin := newAdminClient(t, hs)
	admin.hc.Timeout = 35 * time.Second
	if status, body := admin.login("root"); status != http.StatusOK {
		t.Fatalf("login: %d %+v", status, body)
	}
	config := simulationConfig{
		Prefix: "scale", Count: 10_000, Duration: 15, OwnChannel: true, Friends: 1, JoinMin: 1, JoinMax: 1,
		Actions: map[string]simulationRate{"channel_message": {1, 20}},
	}
	stream := admin.openEvents()
	firstMessage := make(chan int, 1)
	allRegistered := make(chan struct{})
	go func() {
		registered := 0
		signal := firstMessage
		for {
			select {
			case event := <-stream.evs:
				switch event["type"] {
				case "registered":
					registered++
					if registered == config.Count {
						close(allRegistered)
					}
				case "activity":
					activity, _ := event["data"].(map[string]any)
					if signal != nil && activity["kind"] == "message" {
						signal <- registered
						signal = nil
					}
				}
			case <-stream.done:
				return
			}
		}
	}()
	started := time.Now()
	if status, body := admin.post("/admin/api/simulation/start", config); status != http.StatusAccepted {
		t.Fatalf("start large simulation: %d %+v", status, body)
	}
	select {
	case count := <-firstMessage:
		if count <= 0 || count >= config.Count {
			t.Fatalf("first message arrived after %d registrations, expected overlap", count)
		}
		t.Logf("first message after %d/%d registrations, %v after start", count, config.Count, time.Since(started).Round(time.Millisecond))
	case <-stream.done:
		t.Fatal("live event stream closed during simulation setup")
	case <-time.After(18 * time.Second):
		t.Fatal("live event stream did not receive an early message")
	}
	deadline := time.Now().Add(85 * time.Second)
	for {
		srv.simulation.mu.Lock()
		st := srv.simulation.run.status
		channels, joins, friendships := st.OK["create_channel"], st.OK["join_channel"], st.OK["friendship"]
		srv.simulation.mu.Unlock()
		if st.Phase == "running" {
			if st.Count != 10_000 || channels != 10_000 || joins != 10_000 || friendships != 5_000 || st.Failed != 0 {
				t.Fatalf("large simulation has %d actors, %d channels, %d joins, %d friendships and %d failures (%s)",
					st.Count, channels, joins, friendships, st.Failed, st.LastError)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("large simulation stalled in %q after %d channels, %d joins and %d friendships (%s)", st.Phase, channels, joins, friendships, st.LastError)
		}
		time.Sleep(60 * time.Millisecond)
	}
	t.Logf("completed preparation in %v", time.Since(started).Round(time.Millisecond))
	select {
	case <-allRegistered:
	case <-stream.done:
		t.Fatal("live event stream closed during simulation setup")
	case <-time.After(5 * time.Second):
		t.Fatal("live event stream did not receive all registrations")
	}
	snap, err := srv.liveSnapshot(true, true)
	if err != nil || len(snap.Users) != 10_001 || len(snap.Channels) != 10_000 {
		t.Fatalf("live snapshot has %d users and %d channels, error %v", len(snap.Users), len(snap.Channels), err)
	}
	var ids []string
	for _, ch := range snap.Channels {
		ids = append(ids, ch.ID)
	}
	counts, err := srv.DashboardChannelCounts(ids)
	if err != nil || len(counts) != 10_000 {
		t.Fatalf("terminal channel counts: %d channels, %v", len(counts), err)
	}
	members := 0
	for _, ch := range counts {
		members += ch.OnlineCount
	}
	if members != 20_000 {
		t.Fatalf("terminal counted %d online memberships, want 20000", members)
	}
	if status, data := admin.get("/admin/api/simulation"); status != http.StatusOK || len(data["users"].([]any)) != 10_000 {
		t.Fatalf("large simulation listing status = %d", status)
	} else if status, unchanged := admin.get("/admin/api/simulation?users_revision=" + data["users_revision"].(string)); status != http.StatusOK || unchanged["users"] != nil {
		t.Fatalf("unchanged account list should be omitted on poll (status %d)", status)
	}
	if status, _ := admin.post("/admin/api/simulation/stop", struct{}{}); status != http.StatusOK {
		t.Fatalf("stop large simulation = %d", status)
	}
	select {
	case <-srv.stopSimulation():
	case <-time.After(12 * time.Second):
		t.Fatal("large simulation did not disconnect")
	}
}

func TestSimulationAndAccountDeletionRequireSuperAdmin(t *testing.T) {
	hs, srv := testServerWith(t, func(c *Config) { c.SimulationEnabled = true }, io.Discard)
	managerID := makeUser(t, hs, "manager")
	victimID := makeUser(t, hs, "victim")
	if err := srv.st.SetUserPerms(managerID, []string{PermAccountsManage}); err != nil {
		t.Fatal(err)
	}
	admin := newAdminClient(t, hs)
	if status, body := admin.login("manager"); status != http.StatusOK || body["simulation"] == true {
		t.Fatalf("simulation visibility: %d %+v", status, body)
	}
	if status, _ := admin.get("/admin/api/simulation"); status != http.StatusForbidden {
		t.Fatalf("manager simulation access = %d, want 403", status)
	}
	if status, _ := admin.post("/admin/api/simulation/start", simulationConfig{Prefix: "scene", Count: 1, Duration: 1}); status != http.StatusForbidden {
		t.Fatalf("manager start = %d, want 403", status)
	}
	if status, _ := admin.post("/admin/api/simulation/stop", struct{}{}); status != http.StatusForbidden {
		t.Fatalf("manager stop = %d, want 403", status)
	}
	if status, _ := admin.do(http.MethodDelete, "/admin/api/users/"+victimID, map[string]any{"username": "victim"}); status != http.StatusForbidden {
		t.Fatalf("manager delete = %d, want 403", status)
	}
	if _, err := srv.st.UserByID(victimID); err != nil {
		t.Fatalf("victim changed after denied deletion: %v", err)
	}
}

func TestSimulationRerunAndOrderedDeletion(t *testing.T) {
	hs, srv := testServerWith(t, func(c *Config) { c.SimulationEnabled = true }, io.Discard)
	rootID := makeUser(t, hs, "root")
	if err := srv.st.SetUserSuper(rootID, true); err != nil {
		t.Fatal(err)
	}
	ordinary := makeUser(t, hs, "demo_2")
	if _, err := srv.st.EnsureSimulationUsers([]storage.User{
		{ID: "original", Username: "DEMO_1"}, {ID: "other", Username: "other_1"}, {ID: "last", Username: "other_2"},
	}, "previous"); err != nil {
		t.Fatal(err)
	}
	admin := newAdminClient(t, hs)
	if status, body := admin.login("root"); status != http.StatusOK {
		t.Fatalf("login: %d %+v", status, body)
	}
	config := simulationConfig{Prefix: "demo", Count: 4, Duration: 20, Texts: []string{"Custom text"}, Actions: map[string]simulationRate{"post": {1, 1}}}
	stream := admin.openEvents()
	if status, body := admin.post("/admin/api/simulation/start", config); status != http.StatusAccepted {
		t.Fatalf("restart with conflicts: %d %+v", status, body)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		page, err := srv.st.MomentsBy([]string{"original"}, "original", storage.MomentCursor{}, 20)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Moments) != 0 {
			if page.Moments[0].Body != "Custom text" {
				t.Fatalf("custom text was not used: %q", page.Moments[0].Body)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("reused simulation account did not post")
		}
		time.Sleep(30 * time.Millisecond)
	}
	status, data := admin.get("/admin/api/simulation")
	if status != http.StatusOK {
		t.Fatalf("simulation status: %d %+v", status, data)
	}
	run := data["run"].(map[string]any)
	if run["created"] != float64(2) || run["reused"] != float64(1) || run["conflicts"] != float64(1) {
		t.Fatalf("unexpected conflict counts: %+v", run)
	}
	if status, _ := admin.do(http.MethodDelete, "/admin/api/users/original", map[string]bool{}); status != http.StatusBadRequest {
		t.Fatalf("user page deletion without confirmation = %d", status)
	}
	if status, _ := admin.do(http.MethodDelete, "/admin/api/simulation/users/"+ordinary, map[string]bool{}); status != http.StatusNotFound {
		t.Fatalf("simulation deletion of regular account = %d", status)
	}
	fourth, err := srv.st.UserByUsernameKey("demo_4")
	if err != nil {
		t.Fatal(err)
	}
	if status, _ := admin.do(http.MethodDelete, "/admin/api/simulation/users/"+fourth.ID, map[string]bool{}); status != http.StatusOK {
		t.Fatalf("simulation deletion without confirmation = %d", status)
	}
	third, err := srv.st.UserByUsernameKey("demo_3")
	if err != nil {
		t.Fatal(err)
	}
	if status, body := admin.post("/admin/api/simulation/deletions/start", map[string]any{
		"interval_ms": 125, "user_ids": []string{third.ID, ordinary, "original"},
	}); status != http.StatusAccepted {
		t.Fatalf("ordered deletion: %d %+v", status, body)
	}
	var previous float64
	for _, name := range []string{"DEMO_1", "demo_3"} {
		deadline := time.Now().Add(4 * time.Second)
		for {
			event := stream.next("activity", time.Until(deadline))["data"].(map[string]any)
			if event["kind"] != "account.deleted" || event["actor"].(map[string]any)["name"] == "demo_4" {
				continue
			}
			if actor := event["actor"].(map[string]any); actor["name"] != name {
				t.Fatalf("deletion order: got %+v, want %q", actor, name)
			}
			at := event["at"].(float64)
			if previous != 0 && at-previous < 125 {
				t.Fatalf("deletions were %vms apart", at-previous)
			}
			previous = at
			break
		}
	}
	for _, id := range []string{ordinary, "other"} {
		if _, err := srv.st.UserByID(id); err != nil {
			t.Fatalf("account outside the deletion scope changed: %v", err)
		}
	}
	srv.simulation.mu.Lock()
	firstDone := srv.simulation.deletion.done
	srv.simulation.mu.Unlock()
	select {
	case <-firstDone:
	case <-time.After(3 * time.Second):
		t.Fatal("ordered deletion did not finish")
	}
	if status, body := admin.post("/admin/api/simulation/deletions/start", map[string]any{
		"interval_seconds": 30, "user_ids": []string{"other", "last"},
	}); status != http.StatusAccepted {
		t.Fatalf("second ordered deletion: %d %+v", status, body)
	}
	deadline = time.Now().Add(4 * time.Second)
	for {
		event := stream.next("activity", time.Until(deadline))["data"].(map[string]any)
		if event["kind"] == "account.deleted" && event["actor"].(map[string]any)["id"] == "other" {
			break
		}
	}
	if status, _ := admin.post("/admin/api/simulation/deletions/stop", struct{}{}); status != http.StatusOK {
		t.Fatalf("stop deletion = %d", status)
	}
	srv.simulation.mu.Lock()
	secondDone := srv.simulation.deletion.done
	srv.simulation.mu.Unlock()
	select {
	case <-secondDone:
	case <-time.After(3 * time.Second):
		t.Fatal("canceled deletion did not stop")
	}
	if _, err := srv.st.UserByID("last"); err != nil {
		t.Fatalf("queued account was removed after stopping deletion: %v", err)
	}
}

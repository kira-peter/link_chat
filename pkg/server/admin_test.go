package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"link-chat/pkg/protocol"
	"link-chat/pkg/storage"
	"link-chat/pkg/version"
)

// adminClient is a browser stand-in for the console: it keeps the cookie and
// sends the CSRF header on writes.
type adminClient struct {
	t    *testing.T
	base string
	hc   *http.Client
}

func newAdminClient(t *testing.T, hs *httptest.Server) *adminClient {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	return &adminClient{t: t, base: hs.URL, hc: &http.Client{Jar: jar, Timeout: 10 * time.Second}}
}

func (a *adminClient) do(method, path string, body any, headers ...string) (int, map[string]any) {
	a.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, a.base+path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet {
		req.Header.Set(adminCSRFHeader, adminCSRFValue)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		if headers[i+1] == "" {
			req.Header.Del(headers[i])
		} else {
			req.Header.Set(headers[i], headers[i+1])
		}
	}
	res, err := a.hc.Do(req)
	if err != nil {
		a.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	var out map[string]any
	b, _ := io.ReadAll(res.Body)
	_ = json.Unmarshal(b, &out)
	return res.StatusCode, out
}

func (a *adminClient) get(path string) (int, map[string]any) {
	return a.do(http.MethodGet, path, nil)
}

func (a *adminClient) post(path string, body any) (int, map[string]any) {
	return a.do(http.MethodPost, path, body)
}

func (a *adminClient) login(user string) (int, map[string]any) {
	return a.post("/admin/api/login", adminLoginReq{Username: user, Password: pw})
}

func code(m map[string]any) string { s, _ := m["code"].(string); return s }

// makeUser registers a chat account and returns its ID.
func makeUser(t *testing.T, hs *httptest.Server, name string) string {
	t.Helper()
	return dial(t, hs).register(name, pw).User.ID
}

// adminFixture is a server with a super administrator "root" signed in.
func adminFixture(t *testing.T) (*httptest.Server, *Server, *adminClient, string) {
	t.Helper()
	hs, srv := testServer(t)
	rootID := makeUser(t, hs, "root")
	srv.st.SetUserSuper(rootID, true)
	a := newAdminClient(t, hs)
	if st, body := a.login("root"); st != http.StatusOK {
		t.Fatalf("root login: %d %v", st, body)
	}
	return hs, srv, a, rootID
}

// ---------- 3.2 authentication ----------

func TestAdminLogin(t *testing.T) {
	hs, srv := testServer(t)
	plainID := makeUser(t, hs, "plain")
	opsID := makeUser(t, hs, "ops")
	srv.st.SetUserPerms(opsID, []string{PermStatsView})
	_ = plainID

	a := newAdminClient(t, hs)
	if st, b := a.login("plain"); st != http.StatusForbidden || code(b) != protocol.CodeForbidden {
		t.Fatalf("a plain user signed in: %d %v", st, b)
	}
	if st, b := a.post("/admin/api/login", adminLoginReq{Username: "ops", Password: "nope nope"}); st != http.StatusUnauthorized || code(b) != protocol.CodeInvalidCredentials {
		t.Fatalf("wrong password: %d %v", st, b)
	}
	if st, _ := a.get("/admin/api/me"); st != http.StatusUnauthorized {
		t.Fatalf("me before login: %d", st)
	}
	st, me := a.login("ops")
	if st != http.StatusOK || me["username"] != "ops" {
		t.Fatalf("ops login: %d %v", st, me)
	}

	// The cookie is HttpOnly, SameSite=Strict, scoped to /admin/.
	req, _ := http.NewRequest(http.MethodPost, hs.URL+"/admin/api/login", strings.NewReader(`{"username":"ops","password":"`+pw+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(adminCSRFHeader, adminCSRFValue)
	res, _ := http.DefaultClient.Do(req)
	res.Body.Close()
	ck := res.Header.Get("Set-Cookie")
	for _, want := range []string{"HttpOnly", "SameSite=Strict", "Path=/admin/"} {
		if !strings.Contains(ck, want) {
			t.Errorf("cookie lacks %s: %s", want, ck)
		}
	}

	// Logout makes the token worthless.
	if st, _ := a.post("/admin/api/logout", nil); st != http.StatusOK {
		t.Fatalf("logout: %d", st)
	}
	if st, _ := a.get("/admin/api/me"); st != http.StatusUnauthorized {
		t.Fatalf("me after logout: %d", st)
	}
}

func TestAdminLoginSharesRateLimit(t *testing.T) {
	hs, _ := testServer(t)
	makeUser(t, hs, "ops")
	a := newAdminClient(t, hs)
	got429 := false
	for i := 0; i < 12; i++ {
		st, _ := a.post("/admin/api/login", adminLoginReq{Username: "ops", Password: "wrong wrong"})
		if st == http.StatusTooManyRequests {
			got429 = true
			break
		}
	}
	if !got429 {
		t.Fatal("repeated wrong passwords were never limited")
	}
	// Fresh connections from the same host do not reset the budget.
	b := newAdminClient(t, hs)
	b.hc.Transport = &http.Transport{DisableKeepAlives: true}
	if st, _ := b.post("/admin/api/login", adminLoginReq{Username: "ops", Password: pw}); st != http.StatusTooManyRequests {
		t.Fatalf("a new connection escaped the limit: %d", st)
	}
}

func TestAdminTokenExpires(t *testing.T) {
	_, srv, a, rootID := adminFixture(t)
	toks, _ := srv.st.ListTokensForUser(rootID)
	for _, tk := range toks {
		if tk.Purpose == storage.PurposeAdmin {
			tk.ExpiresAt = storage.Now() - 1
			srv.st.SaveToken(tk)
		}
	}
	if st, b := a.get("/admin/api/me"); st != http.StatusUnauthorized || code(b) != protocol.CodeTokenExpired {
		t.Fatalf("expired: %d %v", st, b)
	}
}

func TestAdminCSRFAndContentType(t *testing.T) {
	hs, _, a, _ := adminFixture(t)
	id := makeUser(t, hs, "victim")
	if st, _ := a.do(http.MethodPost, "/admin/api/users/"+id+"/kick", nil, adminCSRFHeader, ""); st != http.StatusForbidden {
		t.Fatalf("POST without the header: %d", st)
	}
	// A form post, as a cross-site page would send.
	req, _ := http.NewRequest(http.MethodPost, hs.URL+"/admin/api/users/"+id+"/ban", strings.NewReader("reason=x&minutes=0"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set(adminCSRFHeader, adminCSRFValue)
	for _, c := range a.hc.Jar.Cookies(mustURL(hs.URL + "/admin/")) {
		req.AddCookie(c)
	}
	res, _ := http.DefaultClient.Do(req)
	res.Body.Close()
	if res.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("form post: %d", res.StatusCode)
	}
}

func mustURL(s string) *url.URL { u, _ := url.Parse(s); return u }

// The console and chat do not accept each other's tokens, and signing in to
// the console does not displace a chat session.
func TestAdminAndChatSessionsAreSeparate(t *testing.T) {
	hs, srv := testServer(t)
	c := dial(t, hs)
	res := c.register("root", pw)
	srv.st.SetUserSuper(res.User.ID, true)

	a := newAdminClient(t, hs)
	if st, _ := a.login("root"); st != http.StatusOK {
		t.Fatal("login")
	}
	// The chat connection is still alive: a request on it succeeds.
	c.callOK(protocol.OpUserList, nil, nil)
	for _, f := range c.drainEvents() {
		if strings.Contains(string(f.Payload), protocol.EvSessionReplaced) {
			t.Fatal("console sign-in displaced the chat session")
		}
	}

	// A chat token presented as the console cookie is refused.
	jar, _ := cookiejar.New(nil)
	jar.SetCookies(mustURL(hs.URL+"/admin/"), []*http.Cookie{{Name: adminCookie, Value: res.Token, Path: "/admin/"}})
	other := &adminClient{t: t, base: hs.URL, hc: &http.Client{Jar: jar}}
	if st, _ := other.get("/admin/api/me"); st != http.StatusUnauthorized {
		t.Fatalf("chat token accepted by console: %d", st)
	}

	// A chat login later does not end the console session either.
	c2 := dial(t, hs)
	c2.hello()
	c2.callOK(protocol.OpAuthLogin, protocol.LoginReq{Username: "root", Password: pw}, nil)
	if st, _ := a.get("/admin/api/me"); st != http.StatusOK {
		t.Fatalf("chat login ended the console session: %d", st)
	}
}

// Permission changes and bans apply on the next request.
func TestAdminPermissionsAreLive(t *testing.T) {
	hs, srv, root, _ := adminFixture(t)
	bobID := makeUser(t, hs, "bob")
	srv.st.SetUserPerms(bobID, []string{PermChatView, PermStatsView})
	bob := newAdminClient(t, hs)
	bob.login("bob")
	if st, _ := bob.get("/admin/api/conversations"); st != http.StatusOK {
		t.Fatalf("with chat.view: %d", st)
	}
	if st, _ := root.post("/admin/api/users/"+bobID+"/perms", permsReq{Perms: []string{PermStatsView}}); st != http.StatusOK {
		t.Fatalf("narrow: %d", st)
	}
	if st, _ := bob.get("/admin/api/conversations"); st != http.StatusForbidden {
		t.Fatalf("after losing chat.view: %d", st)
	}
	root.post("/admin/api/users/"+bobID+"/perms", permsReq{Perms: nil})
	if st, _ := bob.get("/admin/api/me"); st != http.StatusForbidden {
		t.Fatalf("after losing everything: %d", st)
	}
	srv.st.SetUserPerms(bobID, []string{PermStatsView})
	root.post("/admin/api/users/"+bobID+"/ban", sanctionReq{Reason: "x"})
	// The ban revoked bob's console token along with his chat sessions.
	if st, _ := bob.get("/admin/api/stats"); st != http.StatusUnauthorized {
		t.Fatalf("after ban: %d", st)
	}
	if st, b := bob.login("bob"); st != http.StatusForbidden || code(b) != protocol.CodeAccountBanned {
		t.Fatalf("signing in again while banned: %d %v", st, b)
	}
}

// ---------- 3.3 every endpoint: allowed with its permission, 403 without ----------

func TestAdminEndpointPermissions(t *testing.T) {
	hs, srv, root, _ := adminFixture(t)
	targetID := makeUser(t, hs, "target")
	var cv protocol.Conversation
	owner := dial(t, hs)
	owner.register("owner", pw)
	owner.callOK(protocol.OpChannelCreate, protocol.ChannelCreateReq{Name: "general"}, &cv)

	type ep struct {
		method, path string
		body         any
		perm         string
	}
	eps := []ep{
		{"GET", "/admin/api/stats", nil, PermStatsView},
		{"GET", "/admin/api/users", nil, PermUsersModerate},
		{"GET", "/admin/api/users/" + targetID, nil, PermUsersModerate},
		{"POST", "/admin/api/users/" + targetID + "/kick", struct{}{}, PermUsersModerate},
		{"POST", "/admin/api/users/" + targetID + "/mute", sanctionReq{Reason: "r", Minutes: 5}, PermUsersModerate},
		{"POST", "/admin/api/users/" + targetID + "/perms", permsReq{}, PermAccountsManage},
		{"GET", "/admin/api/accounts", nil, PermAccountsManage},
		{"GET", "/admin/api/audit", nil, PermAccountsManage},
		{"GET", "/admin/api/conversations", nil, PermChatView},
		{"GET", "/admin/api/conversations/" + cv.ID + "/messages", nil, PermChatView},
		{"GET", "/admin/api/search?q=x", nil, PermChatView},
	}
	for i, perm := range AllPerms {
		name := fmt.Sprintf("only%d", i)
		id := makeUser(t, hs, name)
		srv.st.SetUserPerms(id, []string{perm})
		c := newAdminClient(t, hs)
		c.login(name)
		for _, e := range eps {
			st, b := c.do(e.method, e.path, e.body)
			allowed := e.perm == perm
			if allowed && st == http.StatusForbidden {
				t.Errorf("%s with %s: forbidden %v", e.path, perm, b)
			}
			if !allowed && st != http.StatusForbidden {
				t.Errorf("%s with only %s: %d, want 403", e.path, perm, st)
			}
		}
	}
	// And the super administrator gets through everywhere.
	for _, e := range eps {
		if st, b := root.do(e.method, e.path, e.body); st == http.StatusForbidden {
			t.Errorf("root on %s: %v", e.path, b)
		}
	}
}

func TestAdminModerationEndpoints(t *testing.T) {
	hs, srv, root, rootID := adminFixture(t)
	carol := dial(t, hs)
	cres := carol.register("carol", pw)

	if st, b := root.post("/admin/api/users/"+cres.User.ID+"/ban", sanctionReq{Reason: " "}); st != http.StatusBadRequest {
		t.Fatalf("empty reason: %d %v", st, b)
	}
	if st, _ := root.post("/admin/api/users/"+rootID+"/ban", sanctionReq{Reason: "x"}); st != http.StatusForbidden {
		t.Fatalf("ban self/super: %d", st)
	}
	// A second super administrator cannot be touched either.
	other := makeUser(t, hs, "root2")
	srv.st.SetUserSuper(other, true)
	for _, path := range []string{"/ban", "/mute", "/kick", "/reset-password"} {
		body := any(sanctionReq{Reason: "x"})
		if path == "/reset-password" {
			body = passwordReq{Password: "something long"}
		}
		if st, _ := root.post("/admin/api/users/"+other+path, body); st != http.StatusForbidden {
			t.Errorf("%s on a super: %d", path, st)
		}
	}

	st, sc := root.post("/admin/api/users/"+cres.User.ID+"/mute", sanctionReq{Reason: "noise", Minutes: 60})
	if st != http.StatusOK || sc["active"] != true || sc["created_by_name"] != "root" {
		t.Fatalf("mute: %d %v", st, sc)
	}
	carol.waitEvent(protocol.EvAccountMuted, 5*time.Second)
	_, detail := root.get("/admin/api/users/" + cres.User.ID)
	if detail["mute"] == nil || len(detail["sanctions"].([]any)) != 1 {
		t.Fatalf("detail: %v", detail)
	}
	if st, _ := root.post("/admin/api/users/"+cres.User.ID+"/revoke", revokeReq{SanctionID: "nope"}); st != http.StatusNotFound {
		t.Fatalf("revoke unknown: %d", st)
	}
	if st, _ := root.post("/admin/api/users/"+cres.User.ID+"/revoke", revokeReq{SanctionID: sc["id"].(string)}); st != http.StatusOK {
		t.Fatalf("revoke: %d", st)
	}
	carol.waitEvent(protocol.EvAccountUnmuted, 5*time.Second)

	if st, b := root.post("/admin/api/users/"+cres.User.ID+"/reset-password", passwordReq{Password: "short"}); st != http.StatusBadRequest || code(b) != protocol.CodeWeakPassword {
		t.Fatalf("weak reset: %d %v", st, b)
	}
}

func TestAdminCreateAccountAndGrant(t *testing.T) {
	hs, srv, root, _ := adminFixture(t)
	st, b := root.post("/admin/api/accounts", createAccountReq{Username: "ops", Password: pw, Perms: []string{PermStatsView}})
	if st != http.StatusOK || b["can_recover"] != false {
		t.Fatalf("create: %d %v", st, b)
	}
	if st, b := root.post("/admin/api/accounts", createAccountReq{Username: "ops", Password: pw}); st != http.StatusConflict || code(b) != protocol.CodeUsernameTaken {
		t.Fatalf("duplicate: %d %v", st, b)
	}
	if st, b := root.post("/admin/api/accounts", createAccountReq{Username: "x", Password: pw}); code(b) != protocol.CodeInvalidUsername {
		t.Fatalf("bad name: %d %v", st, b)
	}
	if st, b := root.post("/admin/api/accounts", createAccountReq{Username: "okname", Password: "short"}); code(b) != protocol.CodeWeakPassword {
		t.Fatalf("weak: %d %v", st, b)
	}

	// The new account signs in to chat and sees only the overview in the console.
	c := dial(t, hs)
	c.hello()
	c.callOK(protocol.OpAuthLogin, protocol.LoginReq{Username: "ops", Password: pw}, nil)
	ops := newAdminClient(t, hs)
	if _, me := ops.login("ops"); fmt.Sprint(me["perms"]) != "[stats.view]" {
		t.Fatalf("ops perms: %v", me["perms"])
	}
	// No security question, so self-service recovery is refused.
	c.callErr(protocol.OpRecoverQuestion, protocol.RecoverQuestionReq{Username: "ops"}, protocol.CodeCannotRecover)

	// A manager without chat.view cannot hand it out, nor accounts.manage.
	mgrID := makeUser(t, hs, "mgr")
	srv.st.SetUserPerms(mgrID, []string{PermAccountsManage, PermStatsView})
	mgr := newAdminClient(t, hs)
	mgr.login("mgr")
	u, _ := srv.st.UserByUsernameKey("ops")
	if st, _ := mgr.post("/admin/api/users/"+u.ID+"/perms", permsReq{Perms: []string{PermChatView}}); st != http.StatusForbidden {
		t.Fatalf("grant unheld: %d", st)
	}
	if st, _ := mgr.post("/admin/api/users/"+u.ID+"/perms", permsReq{Perms: []string{PermAccountsManage}}); st != http.StatusForbidden {
		t.Fatalf("grant manage: %d", st)
	}
	if st, _ := mgr.post("/admin/api/users/"+mgrID+"/perms", permsReq{Perms: []string{PermStatsView}}); st != http.StatusForbidden {
		t.Fatalf("self: %d", st)
	}
	if st, _ := mgr.post("/admin/api/accounts", createAccountReq{Username: "sneaky", Password: pw, Perms: []string{PermChatView}}); st != http.StatusForbidden {
		t.Fatalf("create with unheld: %d", st)
	}
	if _, err := srv.st.UserByUsernameKey("sneaky"); err == nil {
		t.Fatal("refused account was created anyway")
	}
}

// Viewing conversations, including other people's private chats, and every
// search are audited.
func TestAdminChatViewingIsAudited(t *testing.T) {
	hs, srv, root, rootID := adminFixture(t)
	alice := dial(t, hs)
	alice.register("alice", pw)
	makeUser(t, hs, "bob")
	var dm protocol.Conversation
	alice.callOK(protocol.OpDMOpen, protocol.DMOpenReq{Username: "bob"}, &dm)
	alice.callOK(protocol.OpMessageSend, protocol.SendMessageReq{ConversationID: dm.ID, Body: "secret plans"}, nil)

	_, list := root.get("/admin/api/conversations")
	convs := list["conversations"].([]any)
	if len(convs) != 1 || convs[0].(map[string]any)["label"] != "alice ↔ bob" && convs[0].(map[string]any)["label"] != "bob ↔ alice" {
		t.Fatalf("conversations: %v", convs)
	}
	st, page := root.get("/admin/api/conversations/" + dm.ID + "/messages")
	msgs := page["messages"].([]any)
	if st != http.StatusOK || len(msgs) != 1 || msgs[0].(map[string]any)["body"] != "secret plans" {
		t.Fatalf("messages: %d %v", st, page)
	}
	_, sr := root.get("/admin/api/search?q=PLANS&author=alice")
	hits := sr["messages"].([]any)
	if len(hits) != 1 || sr["next"] != nil {
		t.Fatalf("search: %v", sr)
	}

	entries, _ := srv.st.ListAudit(storage.AuditFilter{ActorID: rootID, Limit: 50})
	var viewed, searched bool
	for _, e := range entries {
		if e.Action == "view_conversation" && e.Target == dm.ID && e.Detail["kind"] == protocol.KindDM {
			viewed = true
		}
		if e.Action == "search" && e.Detail["q"] == "PLANS" && e.Detail["author"] == "alice" {
			searched = true
		}
	}
	if !viewed || !searched {
		t.Fatalf("audit: viewed=%v searched=%v %+v", viewed, searched, entries)
	}
	_, ab := root.get("/admin/api/audit?actor_name=root&action=search")
	if len(ab["entries"].([]any)) != 1 {
		t.Fatalf("audit filter: %v", ab)
	}
}

// ---------- 3.4 stats ----------

func TestAdminStats(t *testing.T) {
	hs, _, root, _ := adminFixture(t)
	c := dial(t, hs)
	c.helloAs("link-chat-tui/1.0.0")
	var res protocol.AuthRes
	c.callOK(protocol.OpAuthRegister, protocol.RegisterReq{Username: "v1user", Password: pw, SecurityQuestion: "q", SecurityAnswer: "a"}, &res)
	c2 := dial(t, hs)
	c2.helloAs("link-chat-tui/2.0.0")
	c2.callOK(protocol.OpAuthRegister, protocol.RegisterReq{Username: "v2user", Password: pw, SecurityQuestion: "q", SecurityAnswer: "a"}, nil)
	old := dial(t, hs)
	old.helloAs("link-chat-tui")
	old.callOK(protocol.OpAuthRegister, protocol.RegisterReq{Username: "olduser", Password: pw, SecurityQuestion: "q", SecurityAnswer: "a"}, nil)

	var cv protocol.Conversation
	c.callOK(protocol.OpChannelCreate, protocol.ChannelCreateReq{Name: "general"}, &cv)
	for i := 0; i < 3; i++ {
		c.callOK(protocol.OpMessageSend, protocol.SendMessageReq{ConversationID: cv.ID, Body: "hi"}, nil)
	}

	st, s := root.get("/admin/api/stats")
	if st != http.StatusOK {
		t.Fatalf("stats: %d", st)
	}
	if s["server_version"] != version.String() || s["messages_since_start"] != float64(3) || s["messages_24h"] != float64(3) {
		t.Fatalf("stats: %v", s)
	}
	if s["counts"].(map[string]any)["messages"] != float64(3) {
		t.Fatalf("counts: %v", s["counts"])
	}
	var dist []string
	for _, v := range s["versions"].([]any) {
		m := v.(map[string]any)
		dist = append(dist, fmt.Sprintf("%v=%v", m["version"], m["count"]))
	}
	// The test client in adminFixture registered root without a version.
	if strings.Join(dist, ",") != "2.0.0=1,1.0.0=1,unknown=2" {
		t.Fatalf("version distribution: %v", dist)
	}
	byName := map[string]string{}
	for _, o := range s["online"].([]any) {
		m := o.(map[string]any)
		byName[m["username"].(string)] = m["client_version"].(string)
	}
	if byName["v1user"] != "1.0.0" || byName["olduser"] != "unknown" {
		t.Fatalf("online versions: %v", byName)
	}

	_, u := root.get("/admin/api/users/" + res.User.ID)
	sess := u["sessions"].([]any)
	if len(sess) != 1 || sess[0].(map[string]any)["client_version"] != "1.0.0" {
		t.Fatalf("session version: %v", sess)
	}
	if fmt.Sprint(u["client_versions"]) != "[1.0.0]" {
		t.Fatalf("user online versions: %v", u["client_versions"])
	}
}

func TestMsgStatsWindow(t *testing.T) {
	var m msgStats
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	m.record(base)
	m.record(base.Add(5 * time.Hour))
	m.record(base.Add(23 * time.Hour))
	if n := m.last24h(base.Add(23 * time.Hour)); n != 3 {
		t.Fatalf("within a day: %d", n)
	}
	if n := m.last24h(base.Add(25 * time.Hour)); n != 2 {
		t.Fatalf("after the first hour lapsed: %d", n)
	}
	m.record(base.Add(48 * time.Hour)) // reuses bucket 0, which must reset
	if n := m.last24h(base.Add(48 * time.Hour)); n != 1 {
		t.Fatalf("stale bucket reused: %d", n)
	}
	if m.total.Load() != 4 {
		t.Fatalf("total: %d", m.total.Load())
	}
}

// ---------- 3.5 log ring ----------

func TestLogRing(t *testing.T) {
	r := NewLogRing()
	h := NewLogHandler(r, nil, false)
	_ = h
	for i := 1; i <= 600; i++ {
		fmt.Fprintf(r, "line %d\n", i)
	}
	lines := r.Lines()
	if len(lines) != LogRingSize || lines[0] != "line 101" || lines[len(lines)-1] != "line 600" {
		t.Fatalf("ring: %d %q %q", len(lines), lines[0], lines[len(lines)-1])
	}
}

func TestTeeHandlerMatchesTerminal(t *testing.T) {
	var term bytes.Buffer
	ring := NewLogRing()
	lg := newTeeLogger(&term, ring)
	lg.Info("login ok", "user", "alice", "addr", "1.2.3.4:5")
	lg.Debug("hidden")
	lines := ring.Lines()
	if len(lines) != 1 || lines[0] != strings.TrimRight(term.String(), "\n") {
		t.Fatalf("ring %q vs terminal %q", lines, term.String())
	}
	if strings.Contains(lines[0], "\x1b[") {
		t.Fatal("colour codes in the ring")
	}
}

// ---------- 3.6 switch ----------

func TestAdminDisabled(t *testing.T) {
	hs, _ := testServerWith(t, func(c *Config) { off := false; c.AdminConsole = &off }, io.Discard)
	for _, p := range []string{"/admin/", "/admin/api/me", "/admin/api/stats"} {
		res, err := http.Get(hs.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusNotFound {
			t.Errorf("%s with the console off: %d", p, res.StatusCode)
		}
	}
	// Chat still works.
	dial(t, hs).register("alice", pw)
}

func TestAdminEnabledConfig(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Validate()
	if !cfg.AdminEnabled {
		t.Fatal("console off by default")
	}
	t.Setenv("LINKCHAT_ADMIN_ENABLED", "false")
	c := DefaultConfig()
	applyEnv(&c)
	c.Validate()
	if c.AdminEnabled {
		t.Fatal("env override ignored")
	}
}

func TestAdminPageServed(t *testing.T) {
	hs, _ := testServer(t)
	res, err := http.Get(hs.URL + "/admin/")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(b), "<!doctype html>") {
		t.Fatalf("page: %d %s", res.StatusCode, b)
	}
	if !strings.Contains(res.Header.Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Fatal("no CSP on the page")
	}
	for _, asset := range []string{"/admin/app.js", "/admin/style.css"} {
		res, err := http.Get(hs.URL + asset)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Errorf("%s: %d", asset, res.StatusCode)
		}
	}
}

// ---------- 4.x event stream ----------

// sseReader reads events from an open stream.
type sseReader struct {
	t    *testing.T
	res  *http.Response
	evs  chan map[string]any
	done chan struct{}
}

func (a *adminClient) openEvents() *sseReader {
	a.t.Helper()
	req, _ := http.NewRequest(http.MethodGet, a.base+"/admin/api/events", nil)
	hc := &http.Client{Jar: a.hc.Jar} // no timeout: it is a long-lived stream
	res, err := hc.Do(req)
	if err != nil || res.StatusCode != http.StatusOK {
		a.t.Fatalf("events: %v %v", err, res)
	}
	r := &sseReader{t: a.t, res: res, evs: make(chan map[string]any, 64), done: make(chan struct{})}
	go func() {
		defer close(r.done)
		sc := bufio.NewScanner(res.Body)
		for sc.Scan() {
			line := sc.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			var ev map[string]any
			if json.Unmarshal([]byte(line[6:]), &ev) == nil {
				r.evs <- ev
			}
		}
	}()
	a.t.Cleanup(func() { res.Body.Close() })
	return r
}

// next waits for an event of the given type.
func (r *sseReader) next(typ string, d time.Duration) map[string]any {
	r.t.Helper()
	deadline := time.After(d)
	for {
		select {
		case ev := <-r.evs:
			if ev["type"] == typ {
				return ev
			}
		case <-r.done:
			r.t.Fatalf("stream ended while waiting for %s", typ)
		case <-deadline:
			r.t.Fatalf("no %s event", typ)
		}
	}
}

func (r *sseReader) waitEnded(d time.Duration) {
	r.t.Helper()
	select {
	case <-r.done:
	case <-time.After(d):
		r.t.Fatal("stream did not end")
	}
}

func TestAdminEventStream(t *testing.T) {
	hs, srv, root, _ := adminFixture(t)
	statsID := makeUser(t, hs, "watcher")
	srv.st.SetUserPerms(statsID, []string{PermStatsView})
	watcher := newAdminClient(t, hs)
	watcher.login("watcher")

	full := root.openEvents()
	limited := watcher.openEvents()

	alice := dial(t, hs)
	alice.register("alice", pw)
	full.next("online", 5*time.Second)
	limited.next("online", 5*time.Second)

	var cv protocol.Conversation
	alice.callOK(protocol.OpChannelCreate, protocol.ChannelCreateReq{Name: "general"}, &cv)
	alice.callOK(protocol.OpMessageSend, protocol.SendMessageReq{ConversationID: cv.ID, Body: "hello world"}, nil)

	ev := full.next("message", 5*time.Second)
	data := ev["data"].(map[string]any)
	if data["message"].(map[string]any)["body"] != "hello world" || data["conversation_label"] != "#general" {
		t.Fatalf("full event: %v", ev)
	}
	ev = limited.next("message", 5*time.Second)
	if b, _ := json.Marshal(ev); strings.Contains(string(b), "hello world") || strings.Contains(string(b), cv.ID) {
		t.Fatalf("stats-only stream saw the message: %s", b)
	}

	// Losing every permission ends the stream.
	root.post("/admin/api/users/"+statsID+"/perms", permsReq{Perms: nil})
	limited.waitEnded(5 * time.Second)
}

// A stream that stops reading is dropped; chat delivery is not held up.
func TestSlowAdminStreamIsDropped(t *testing.T) {
	hs, srv, _, rootID := adminFixture(t)
	sub := &adminSub{uid: rootID, ch: make(chan []byte, 1), perms: effectivePerms(storage.User{Super: true}), done: make(chan struct{})}
	srv.hub.addAdminSub(sub)
	defer srv.hub.removeAdminSub(sub)

	alice := dial(t, hs)
	alice.register("alice", pw)
	bob := dial(t, hs)
	bob.register("bob", pw)
	var cv protocol.Conversation
	alice.callOK(protocol.OpChannelCreate, protocol.ChannelCreateReq{Name: "general"}, &cv)
	bob.callOK(protocol.OpChannelJoin, protocol.ConversationRef{ConversationID: cv.ID}, nil)
	for i := 0; i < 5; i++ {
		alice.callOK(protocol.OpMessageSend, protocol.SendMessageReq{ConversationID: cv.ID, Body: "m"}, nil)
	}
	select {
	case <-sub.done:
	case <-time.After(5 * time.Second):
		t.Fatal("slow stream was not dropped")
	}
	got := 0
	for _, f := range bob.drainEvents() {
		if strings.Contains(string(f.Payload), `"name":"message"`) {
			got++
		}
	}
	if got != 5 {
		t.Fatalf("chat delivery affected: bob got %d of 5", got)
	}
}

// Shutdown ends open streams within the grace period.
func TestShutdownEndsAdminStreams(t *testing.T) {
	_, srv, root, _ := adminFixture(t)
	r := root.openEvents()
	var once sync.Once
	done := make(chan struct{})
	go func() {
		once.Do(func() { _ = srv.Shutdown(); close(done) })
	}()
	r.waitEnded(5 * time.Second)
	select {
	case <-done:
	case <-time.After(srv.cfg.ShutdownGrace + 2*time.Second):
		t.Fatal("shutdown waited out its grace period on an admin stream")
	}
}

// The heartbeat re-checks access, so an expired session ends its stream even
// with nothing else happening.
func TestAdminStreamHeartbeatRechecks(t *testing.T) {
	old := adminStreamHeartbeat
	adminStreamHeartbeat = 50 * time.Millisecond
	defer func() { adminStreamHeartbeat = old }()
	_, srv, root, rootID := adminFixture(t)
	r := root.openEvents()
	srv.st.DeleteTokensForUserPurpose(rootID, storage.PurposeAdmin)
	r.waitEnded(5 * time.Second)
}

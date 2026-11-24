package storage

import (
	"fmt"
	"strings"
	"testing"
)

func openT(t *testing.T, dir string) Storage {
	t.Helper()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func reopen(t *testing.T, st Storage, dir string) Storage {
	t.Helper()
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	return openT(t, dir)
}

// 1.1: an account written before the console has no permissions; permissions
// and the super flag survive a restart.
func TestUserPermsPersist(t *testing.T) {
	dir := storeDir(t)
	st := openT(t, dir)
	// A row as written before the console existed: no perms, no super flag.
	if _, err := sqlStoreOf(t, st).db.Exec(`INSERT INTO users (id, username, username_key, password_hash, created_at) VALUES ('u1', 'alice', 'alice', 'x', 1)`); err != nil {
		t.Fatal(err)
	}
	u, err := st.UserByID("u1")
	if err != nil || len(u.Perms) != 0 || u.Super {
		t.Fatalf("legacy user: %+v %v", u, err)
	}

	if err := st.SetUserPerms("u1", []string{"stats.view", "chat.view"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserSuper("u1", true); err != nil {
		t.Fatal(err)
	}
	// An account created without a security question is accepted.
	if err := st.CreateUser(User{ID: "u2", Username: "ops", PasswordHash: "h", CreatedAt: 2}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserPerms("missing", nil); err != ErrNotFound {
		t.Fatalf("unknown user: %v", err)
	}

	st = reopen(t, st, dir)
	defer st.Close()
	u, _ = st.UserByID("u1")
	if !u.Super || strings.Join(u.Perms, ",") != "chat.view,stats.view" {
		t.Fatalf("after reopen: %+v", u)
	}
	ops, _ := st.UserByID("u2")
	if ops.CanRecover() {
		t.Fatal("an account without a question claims it can recover")
	}

	if err := st.SetUserPerms("u1", nil); err != nil {
		t.Fatal(err)
	}
	u, _ = st.UserByID("u1")
	if u.Perms != nil {
		t.Fatalf("cleared perms: %v", u.Perms)
	}
}

// 1.2: sanctions are active, lapse, can be permanent, revoked or superseded,
// and read back the same after a restart.
func TestSanctions(t *testing.T) {
	dir := storeDir(t)
	st := openT(t, dir)
	const hour = int64(3600_000)
	t0 := int64(1_000_000)

	ban, err := st.AddSanction(Sanction{UserID: "u1", Kind: SanctionBan, Reason: "spam", CreatedBy: "a", CreatedAt: t0, ExpiresAt: t0 + hour})
	if err != nil || ban.ID == "" {
		t.Fatalf("add: %+v %v", ban, err)
	}
	if _, ok, _ := st.ActiveSanction("u1", SanctionBan, t0+hour/2); !ok {
		t.Fatal("ban not active inside its window")
	}
	if _, ok, _ := st.ActiveSanction("u1", SanctionBan, t0+hour); ok {
		t.Fatal("ban still active at its expiry")
	}
	if _, ok, _ := st.ActiveSanction("u1", SanctionMute, t0+1); ok {
		t.Fatal("a ban reads as a mute")
	}
	if _, ok, _ := st.ActiveSanction("u2", SanctionBan, t0+1); ok {
		t.Fatal("another user is banned")
	}

	// Permanent.
	perm, _ := st.AddSanction(Sanction{UserID: "u2", Kind: SanctionBan, Reason: "r", CreatedBy: "a", CreatedAt: t0})
	if _, ok, _ := st.ActiveSanction("u2", SanctionBan, t0+1000*hour); !ok {
		t.Fatal("permanent ban lapsed")
	}

	// Revoke.
	rev, err := st.RevokeSanction(perm.ID, "admin2", t0+5)
	if err != nil || rev.RevokedBy != "admin2" || rev.RevokedAt != t0+5 {
		t.Fatalf("revoke: %+v %v", rev, err)
	}
	if _, ok, _ := st.ActiveSanction("u2", SanctionBan, t0+6); ok {
		t.Fatal("revoked ban still active")
	}
	if _, err := st.RevokeSanction(perm.ID, "admin2", t0+7); err != ErrConflict {
		t.Fatalf("revoking twice: %v", err)
	}
	if _, err := st.RevokeSanction("nope", "x", t0); err != ErrNotFound {
		t.Fatalf("revoking unknown: %v", err)
	}

	// Supersede: a 7-day mute replaces a 1-day mute still in force.
	m1, _ := st.AddSanction(Sanction{UserID: "u3", Kind: SanctionMute, Reason: "a", CreatedBy: "x", CreatedAt: t0, ExpiresAt: t0 + 24*hour})
	m7, _ := st.AddSanction(Sanction{UserID: "u3", Kind: SanctionMute, Reason: "b", CreatedBy: "x", CreatedAt: t0 + 1, ExpiresAt: t0 + 7*24*hour})
	active, ok, _ := st.ActiveSanction("u3", SanctionMute, t0+2*24*hour)
	if !ok || active.ID != m7.ID {
		t.Fatalf("active after supersede: %+v %v", active, ok)
	}
	hist, _ := st.SanctionsForUser("u3")
	if len(hist) != 2 || hist[0].ID != m7.ID || hist[1].ID != m1.ID || hist[1].SupersededBy != m7.ID {
		t.Fatalf("history: %+v", hist)
	}
	// A lapsed sanction is not superseded, only left to expire.
	st.AddSanction(Sanction{UserID: "u1", Kind: SanctionBan, Reason: "again", CreatedBy: "a", CreatedAt: t0 + 2*hour})
	h1, _ := st.SanctionsForUser("u1")
	if h1[1].SupersededBy != "" {
		t.Fatalf("an expired sanction was marked superseded: %+v", h1[1])
	}

	st = reopen(t, st, dir)
	defer st.Close()
	again, _ := st.SanctionsForUser("u3")
	if len(again) != 2 || again[1].SupersededBy != m7.ID {
		t.Fatalf("after reopen: %+v", again)
	}
	if _, ok, _ := st.ActiveSanction("u2", SanctionBan, t0+6); ok {
		t.Fatal("revocation lost on reopen")
	}
}

// 1.3: audit is appended, filtered, paged, and readable after a restart.
func TestAudit(t *testing.T) {
	dir := storeDir(t)
	st := openT(t, dir)
	for i := 1; i <= 25; i++ {
		actor := "a"
		if i%2 == 0 {
			actor = "b"
		}
		action := "view"
		if i%5 == 0 {
			action = "ban"
		}
		if err := st.AppendAudit(AuditEntry{At: int64(i), ActorID: actor, Action: action, Target: fmt.Sprint(i)}); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := st.ListAudit(AuditFilter{Limit: 3})
	if len(got) != 3 || got[0].At != 25 || got[2].At != 23 {
		t.Fatalf("newest page: %+v", got)
	}
	bans, _ := st.ListAudit(AuditFilter{Action: "ban", Limit: 100})
	if len(bans) != 5 || bans[4].At != 5 {
		t.Fatalf("filter by action across memory and disk: %+v", bans)
	}
	byB, _ := st.ListAudit(AuditFilter{ActorID: "b", From: 4, To: 9, Limit: 100})
	if len(byB) != 3 || byB[0].At != 8 || byB[2].At != 4 {
		t.Fatalf("filter by actor and range: %+v", byB)
	}
	// Paging with Before walks older entries.
	deep, _ := st.ListAudit(AuditFilter{Before: 8, Limit: 5})
	if len(deep) != 5 || deep[0].At != 7 || deep[4].At != 3 {
		t.Fatalf("deep page: %+v", deep)
	}

	st = reopen(t, st, dir)
	defer st.Close()
	all, _ := st.ListAudit(AuditFilter{Limit: 500})
	if len(all) != 25 || all[0].At != 25 || all[24].At != 1 {
		t.Fatalf("after reopen: %d entries", len(all))
	}
}

// 1.4: counts match what was written; ListConversations includes DMs.
func TestCounts(t *testing.T) {
	st := openT(t, storeDir(t))
	defer st.Close()
	for _, u := range []string{"u1", "u2", "u3"} {
		st.CreateUser(User{ID: u, Username: u, PasswordHash: "h"})
	}
	st.CreateConversation(Conversation{ID: "c_a", Kind: "channel", Name: "a"})
	st.CreateConversation(Conversation{ID: "c_b", Kind: "channel", Name: "b"})
	dm, _ := st.DMBetween("u1", "u2")
	for i := 0; i < 4; i++ {
		st.AppendMessage(Message{ID: fmt.Sprint("m", i), ConversationID: "c_a", AuthorID: "u1", Body: "x"})
	}
	st.AppendMessage(Message{ID: "d1", ConversationID: dm.ID, AuthorID: "u1", Body: "hi"})

	c, err := st.Counts()
	if err != nil || c != (Counts{Users: 3, Channels: 2, DMs: 1, Messages: 5}) {
		t.Fatalf("counts: %+v %v", c, err)
	}
	convs, _ := st.ListConversations()
	if len(convs) != 3 {
		t.Fatalf("ListConversations omits DMs: %d", len(convs))
	}
}

// 1.5: search filters combine; the cursor resumes without gaps or repeats;
// the scan cap ends a call early.
func TestSearchMessages(t *testing.T) {
	st := openT(t, storeDir(t))
	defer st.Close()
	st.CreateConversation(Conversation{ID: "c_a", Kind: "channel", Name: "a"})
	st.CreateConversation(Conversation{ID: "c_b", Kind: "channel", Name: "b"})
	add := func(id, conv, author, body string, at int64) Message {
		m, err := st.AppendMessage(Message{ID: id, ConversationID: conv, AuthorID: author, Body: body, CreatedAt: at})
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	add("1", "c_a", "alice", "准备发布 v2", 100)
	add("2", "c_a", "bob", "hello", 200)
	add("3", "c_a", "alice", "Release notes", 300)
	gone := add("4", "c_b", "alice", "发布 delayed", 400)
	add("5", "c_b", "bob", "发布 done", 500)
	gone.Deleted, gone.Body = true, ""
	st.UpdateMessage(gone)

	ids := func(ms []Message) string {
		var s []string
		for _, m := range ms {
			s = append(s, m.ID)
		}
		return strings.Join(s, ",")
	}
	r, _ := st.SearchMessages(SearchFilter{Keyword: "发布"}, SearchCursor{})
	if ids(r.Messages) != "5,1" || r.Next != nil {
		t.Fatalf("keyword: %s %+v", ids(r.Messages), r.Next)
	}
	r, _ = st.SearchMessages(SearchFilter{Keyword: "release"}, SearchCursor{})
	if ids(r.Messages) != "3" {
		t.Fatalf("case-insensitive: %s", ids(r.Messages))
	}
	r, _ = st.SearchMessages(SearchFilter{AuthorID: "alice", From: 150}, SearchCursor{})
	if ids(r.Messages) != "3" {
		t.Fatalf("author and time: %s", ids(r.Messages))
	}
	r, _ = st.SearchMessages(SearchFilter{ConversationID: "c_b"}, SearchCursor{})
	if ids(r.Messages) != "5" {
		t.Fatalf("conversation: %s", ids(r.Messages))
	}

	// Paging two at a time covers everything once, newest first across
	// conversations.
	var seen []string
	cur := SearchCursor{}
	for i := 0; i < 10; i++ {
		r, _ := st.SearchMessages(SearchFilter{Limit: 2}, cur)
		seen = append(seen, ids(r.Messages))
		if r.Next == nil {
			break
		}
		cur = *r.Next
	}
	if got := strings.Join(seen, "|"); got != "5,3|2,1" {
		t.Fatalf("paging: %s", got)
	}
}

// Percent and underscore in a keyword are literal characters, not wildcards.
func TestSearchKeywordWildcardsAreLiteral(t *testing.T) {
	st := openT(t, storeDir(t))
	defer st.Close()
	for i, body := range []string{"100% done", "1000 done", "a_b", "axb", `back\slash`} {
		st.AppendMessage(Message{ID: fmt.Sprint("w", i), ConversationID: "c", AuthorID: "u", Body: body, CreatedAt: int64(i + 1)})
	}
	for kw, want := range map[string]string{"0%": "w0", "a_b": "w2", `k\s`: "w4", "_": "w2"} {
		r, err := st.SearchMessages(SearchFilter{Keyword: kw}, SearchCursor{})
		if err != nil || len(r.Messages) != 1 || r.Messages[0].ID != want {
			t.Errorf("keyword %q: %+v %v, want %s", kw, r.Messages, err, want)
		}
	}
}

// Paging through more rows than one batch visits each match exactly once,
// including rows that share a timestamp.
func TestSearchPagingLarge(t *testing.T) {
	st := openT(t, storeDir(t))
	defer st.Close()
	const n = 25000
	sqlStoreOf(t, st).bulkMessages(t, "c_a", n)
	// Two rows with one timestamp, so the id tiebreak is exercised.
	st.AppendMessage(Message{ID: "tie-a", ConversationID: "c_b", AuthorID: "u", Body: "filler tie", CreatedAt: 500})
	st.AppendMessage(Message{ID: "tie-b", ConversationID: "c_b", AuthorID: "u", Body: "filler tie", CreatedAt: 500})

	seen := map[string]bool{}
	cur := SearchCursor{}
	var lastAt int64 = 1 << 62
	for batches := 0; ; batches++ {
		r, err := st.SearchMessages(SearchFilter{Keyword: "filler"}, cur)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range r.Messages {
			if seen[m.ID] {
				t.Fatalf("repeated %s", m.ID)
			}
			if m.CreatedAt > lastAt {
				t.Fatalf("out of order at %s", m.ID)
			}
			seen[m.ID], lastAt = true, m.CreatedAt
		}
		if r.Next == nil {
			break
		}
		cur = *r.Next
	}
	if len(seen) != n-1+2 {
		t.Fatalf("visited %d, want %d", len(seen), n+1)
	}
}

// The full-text index follows edits and soft deletes.
func TestSearchFollowsEdits(t *testing.T) {
	st := openT(t, storeDir(t))
	defer st.Close()
	m, _ := st.AppendMessage(Message{ID: "e1", ConversationID: "c", AuthorID: "u", Body: "original wording"})
	m.Body, m.EditedAt = "replacement text", 9
	if err := st.UpdateMessage(m); err != nil {
		t.Fatal(err)
	}
	if r, _ := st.SearchMessages(SearchFilter{Keyword: "original"}, SearchCursor{}); len(r.Messages) != 0 {
		t.Fatalf("old wording still found: %+v", r.Messages)
	}
	if r, _ := st.SearchMessages(SearchFilter{Keyword: "replacement"}, SearchCursor{}); len(r.Messages) != 1 {
		t.Fatalf("new wording not found: %+v", r.Messages)
	}
	m.Body, m.Deleted = "", true
	st.UpdateMessage(m)
	if r, _ := st.SearchMessages(SearchFilter{Keyword: "replacement"}, SearchCursor{}); len(r.Messages) != 0 {
		t.Fatalf("deleted message found: %+v", r.Messages)
	}
	got, _ := st.MessageByID("e1")
	if !got.Deleted || got.Seq != 1 {
		t.Fatalf("soft delete lost the row: %+v", got)
	}
}

// 1.6: revoking chat sessions leaves admin and reset tokens alone, and the
// purpose-scoped delete removes only its own kind.
func TestTokenPurposes(t *testing.T) {
	st := openT(t, storeDir(t))
	defer st.Close()
	for _, tok := range []Token{
		{Hash: "s", UserID: "u1", Purpose: PurposeSession},
		{Hash: "legacy", UserID: "u1"},
		{Hash: "a", UserID: "u1", Purpose: PurposeAdmin},
		{Hash: "r", UserID: "u1", Purpose: PurposeReset},
		{Hash: "other", UserID: "u2", Purpose: PurposeAdmin},
	} {
		st.SaveToken(tok)
	}
	st.DeleteSessionTokensForUser("u1")
	for hash, want := range map[string]bool{"s": false, "legacy": false, "a": true, "r": true, "other": true} {
		_, err := st.TokenByHash(hash)
		if (err == nil) != want {
			t.Errorf("after session revoke, %s present=%v", hash, err == nil)
		}
	}
	st.DeleteTokensForUserPurpose("u1", PurposeAdmin)
	if _, err := st.TokenByHash("a"); err == nil {
		t.Error("admin token survived")
	}
	if _, err := st.TokenByHash("other"); err != nil {
		t.Error("another user's admin token was removed")
	}
	if _, err := st.TokenByHash("r"); err != nil {
		t.Error("reset token was removed")
	}
}

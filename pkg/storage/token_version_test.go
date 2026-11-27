package storage

import "testing"

// The client version recorded on a session survives a restart, and tokens
// written before the field existed read back with it empty.
func TestTokenClientVersionPersists(t *testing.T) {
	dir := storeDir(t)
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, tok := range []Token{
		{Hash: "h-new", UserID: "u1", CreatedAt: 2, ExpiresAt: Now() + 60_000, Purpose: PurposeSession, ClientVersion: "1.2.3"},
		{Hash: "h-old", UserID: "u1", CreatedAt: 1, ExpiresAt: Now() + 60_000, Purpose: PurposeSession},
	} {
		if err := st.SaveToken(tok); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	again, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	for hash, want := range map[string]string{"h-new": "1.2.3", "h-old": ""} {
		tok, err := again.TokenByHash(hash)
		if err != nil || tok.ClientVersion != want {
			t.Fatalf("%s: got %+v %v; want client version %q", hash, tok, err, want)
		}
	}
}

func TestReplaceChatSessionPreservesOldTokenWhenInsertFails(t *testing.T) {
	st, _ := newTestStore(t)
	old := Token{Hash: "old", UserID: "alice", Purpose: PurposeSession}
	admin := Token{Hash: "admin", UserID: "alice", Purpose: PurposeAdmin}
	for _, token := range []Token{old, admin} {
		if err := st.SaveToken(token); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.ReplaceChatSession(admin); err == nil {
		t.Fatal("expected duplicate token to fail")
	}
	for _, token := range []Token{old, admin} {
		if got, err := st.TokenByHash(token.Hash); err != nil || got.Purpose != token.Purpose {
			t.Fatalf("token %s after replacement failure: %+v, %v", token.Hash, got, err)
		}
	}
}

package server

import (
	"errors"
	"testing"

	"link-chat/pkg/storage"
)

// answers returns a prompt that replies with the given passwords in turn.
func answers(pws ...string) PasswordPrompt {
	return func(string) (string, error) {
		if len(pws) == 0 {
			return "", errors.New("no more input")
		}
		p := pws[0]
		pws = pws[1:]
		return p, nil
	}
}

func cliStore(t *testing.T) storage.Storage {
	t.Helper()
	st, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestCreateAdminNew(t *testing.T) {
	st := cliStore(t)
	res, err := CreateAdmin(st, "root", answers(pw, pw))
	if err != nil || !res.Created {
		t.Fatalf("%+v %v", res, err)
	}
	u, _ := st.UserByUsernameKey("root")
	if !u.Super || u.CanRecover() {
		t.Fatalf("user: %+v", u)
	}
	if ok, _, _ := verifyPassword(pw, u.PasswordHash); !ok {
		t.Fatal("password not set")
	}
	entries, _ := st.ListAudit(storage.AuditFilter{})
	if len(entries) != 1 || entries[0].ActorID != "cli" || entries[0].Action != "create_account" {
		t.Fatalf("audit: %+v", entries)
	}
}

func TestCreateAdminPromotesWithoutChangingPassword(t *testing.T) {
	st := cliStore(t)
	hash, _ := hashPassword(pw, defaultArgonParams)
	st.CreateUser(storage.User{ID: "u1", Username: "alice", PasswordHash: hash})
	res, err := CreateAdmin(st, "Alice", answers()) // no prompt expected
	if err != nil || res.Created {
		t.Fatalf("%+v %v", res, err)
	}
	u, _ := st.UserByID("u1")
	if !u.Super || u.PasswordHash != hash {
		t.Fatalf("user: %+v", u)
	}
	entries, _ := st.ListAudit(storage.AuditFilter{})
	if len(entries) != 1 || entries[0].Action != "promote_admin" {
		t.Fatalf("audit: %+v", entries)
	}
}

func TestCreateAdminRejectsBadInput(t *testing.T) {
	st := cliStore(t)
	if _, err := CreateAdmin(st, "root", answers(pw, pw+"x")); !errors.Is(err, ErrPasswordMismatch) {
		t.Fatalf("mismatch: %v", err)
	}
	if _, err := CreateAdmin(st, "root", answers("short", "short")); err == nil {
		t.Fatal("weak password accepted")
	}
	if _, err := CreateAdmin(st, "x", answers(pw, pw)); err == nil {
		t.Fatal("bad username accepted")
	}
	if _, err := st.UserByUsernameKey("root"); err == nil {
		t.Fatal("a refused run created the account")
	}
}

func TestResetPasswordCLI(t *testing.T) {
	st := cliStore(t)
	CreateAdmin(st, "root", answers(pw, pw))
	u, _ := st.UserByUsernameKey("root")
	st.SaveToken(storage.Token{Hash: "s", UserID: u.ID, Purpose: storage.PurposeSession, ExpiresAt: storage.Now() + 1e6})
	st.SaveToken(storage.Token{Hash: "a", UserID: u.ID, Purpose: storage.PurposeAdmin, ExpiresAt: storage.Now() + 1e6})
	st.SaveToken(storage.Token{Hash: "r", UserID: u.ID, Purpose: storage.PurposeReset, ExpiresAt: storage.Now() + 1e6})

	if err := ResetPasswordCLI(st, "nobody", answers(pw, pw)); err == nil {
		t.Fatal("unknown account accepted")
	}
	if err := ResetPasswordCLI(st, "root", answers("new secret pw", "new secret pw")); err != nil {
		t.Fatal(err)
	}
	u, _ = st.UserByID(u.ID)
	if ok, _, _ := verifyPassword("new secret pw", u.PasswordHash); !ok {
		t.Fatal("password not changed")
	}
	if toks, _ := st.ListTokensForUser(u.ID); len(toks) != 0 {
		t.Fatalf("tokens survive: %+v", toks)
	}
}

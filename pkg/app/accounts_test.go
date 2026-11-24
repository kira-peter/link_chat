package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/client"
	"link-chat/pkg/tui/views"
)

// ---------- 9.1 one token per account ----------

func TestTokensAreKeptPerAccount(t *testing.T) {
	b := newMemBackend()
	store := tokenStore{backend: b, instance: "default"}
	if err := store.forUser("alice").save("tok-a"); err != nil {
		t.Fatal(err)
	}
	if err := store.forUser("bob").save("tok-b"); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.forUser("alice").load(); got != "tok-a" {
		t.Fatalf("saving bob's token changed alice's: %q", got)
	}
	// A dead token is cleared for its own account only.
	if err := store.forUser("alice").clear(); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.forUser("bob").load(); got != "tok-b" {
		t.Fatalf("clearing alice's token touched bob's: %q", got)
	}
}

// The file fallback keeps per-account tokens as files, not directories.
func TestFileBackendStoresPerAccountKeysAsFiles(t *testing.T) {
	dir := appTempDir(t)
	f := fileBackend{dir: dir}
	if err := f.set("default/alice", "x"); err != nil {
		t.Fatal(err)
	}
	if got, err := f.get("default/alice"); err != nil || got != "x" {
		t.Fatalf("read back %q, %v", got, err)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.IsDir() {
			t.Fatalf("a per-account key made a directory %q", e.Name())
		}
	}
}

// A token saved by an older version, under the instance's single key, moves to
// the account named in the config, once.
func TestALegacyTokenMovesToItsAccount(t *testing.T) {
	path := filepath.Join(appTempDir(t), "legacy.json")
	b := credentials(path).(*inMemoryBackend)
	_ = b.set("default", "old-token")
	cfg := Config{Server: "ws://127.0.0.1:1/ws", Instance: "default", Username: "carol"}
	cfg.SetRemember(true)
	a := New(newTestScreen(t, 100, 30), cfg, path)
	if a.st.Token != "old-token" || !a.ui.Login.Remembered {
		t.Fatalf("the old token was not adopted: token %q remembered %v", a.st.Token, a.ui.Login.Remembered)
	}
	if b.values["default/carol"] != "old-token" {
		t.Fatal("the token is not under carol's key")
	}
	if _, ok := b.values["default"]; ok {
		t.Fatal("the old single key is still there to be inherited by the next account")
	}
}

// ---------- 9.2 recent accounts ----------

func TestRecentAccountsAreMostRecentFirstWithoutRepeats(t *testing.T) {
	var c Config
	for _, n := range []string{"alice", "bob", "alice", "carol"} {
		c.rememberAccount(n)
	}
	if got := strings.Join(c.Accounts, ","); got != "carol,alice,bob" {
		t.Fatalf("recent accounts %q, want carol,alice,bob", got)
	}
	for i := 0; i < 20; i++ {
		c.rememberAccount(string(rune('a'+i)) + "user")
	}
	if len(c.Accounts) != maxAccounts {
		t.Fatalf("%d accounts kept, want at most %d", len(c.Accounts), maxAccounts)
	}
}

func TestRecentAccountsHoldNoSecrets(t *testing.T) {
	p := filepath.Join(appTempDir(t), "c.json")
	c := Config{Server: "ws://x/ws", Accounts: []string{"alice", "bob"}, Token: "secret"}
	if err := SaveConfig(p, c); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(p)
	if strings.Contains(string(body), "secret") {
		t.Fatal("the config file holds a token")
	}
	var back Config
	_ = json.Unmarshal(body, &back)
	if strings.Join(back.Accounts, ",") != "alice,bob" {
		t.Fatalf("accounts did not round-trip: %v", back.Accounts)
	}
}

// ---------- 9.6 the account list ----------

// accountsApp is on the stage with the account list open for three accounts,
// two of them remembered.
func accountsApp(t *testing.T) (*App, *inMemoryBackend) {
	t.Helper()
	path := filepath.Join(appTempDir(t), "acc.json")
	b := credentials(path).(*inMemoryBackend)
	_ = b.set("default/alice", "tok-a")
	_ = b.set("default/carol", "tok-c")
	cfg := Config{Server: "ws://127.0.0.1:1/ws", Instance: "default", Username: "alice",
		Accounts: []string{"alice", "bob", "carol"}}
	cfg.SetRemember(true)
	a := New(newTestScreen(t, 100, 30), cfg, path)
	freezeClock(t, time.Unix(70000, 0))
	a.openAccounts("alice")
	return a, b
}

func TestAccountListShowsEveryAccountAndAnotherOne(t *testing.T) {
	a, _ := accountsApp(t)
	a.render()
	scr := a.scr.(tcell.SimulationScreen)
	text := screenText(scr)
	for _, w := range []string{"选择账号", "alice", "bob", "carol", "使用其他账号", "已记住", "需要密码"} {
		if !strings.Contains(text, w) {
			t.Fatalf("the account list has no %q:\n%s", w, text)
		}
	}
	if hasChromeRules(scr) {
		t.Fatal("the account list is not on the stage: the main interface shows")
	}
	// bob has no saved sign-in, so his row says a password is needed.
	for _, ln := range strings.Split(text, "\n") {
		if strings.Contains(ln, "bob") && !strings.Contains(ln, "需要密码") {
			t.Fatalf("bob's row does not say a password is needed: %q", ln)
		}
	}
}

// The focused account wears the focus colour and the others do not; ↓ moves it,
// and the ends do not wrap.
func TestAccountListFocusMovesAndStops(t *testing.T) {
	a, _ := accountsApp(t)
	a.st.MotionOff = true
	frameFG := func(name string) tcell.Color {
		a.render()
		scr := a.scr.(tcell.SimulationScreen)
		x, y, ok := findCell(scr, name)
		if !ok {
			t.Fatalf("%q not drawn", name)
		}
		for cx := x; cx >= 0; cx-- {
			if r, _, _, _ := scr.GetContent(cx, y-1); r == '┌' {
				return cellFG(scr, cx, y-1)
			}
		}
		t.Fatalf("no frame for %q", name)
		return 0
	}
	if frameFG("alice") != a.tokens.Accent || frameFG("bob") == a.tokens.Accent {
		t.Fatal("the account the list opened on is not the lit one")
	}
	a.handleKey(nil, specialKey(tcell.KeyDown))
	if frameFG("bob") != a.tokens.Accent || frameFG("alice") == a.tokens.Accent {
		t.Fatal("↓ did not move the light to bob")
	}
	a.handleKey(nil, specialKey(tcell.KeyUp))
	a.handleKey(nil, specialKey(tcell.KeyUp))
	if a.ui.Accounts.Cursor != 3 {
		t.Fatalf("↑ past the top moved to %d, want 3 (use another account, wrapping round)", a.ui.Accounts.Cursor)
	}
	a.handleKey(nil, specialKey(tcell.KeyDown))
	if a.ui.Accounts.Cursor != 0 {
		t.Fatalf("↓ past the end left the cursor at %d, want 0 (wrapping round)", a.ui.Accounts.Cursor)
	}
}

// The focused button's band needs frames while the list is up.
func TestAccountListBandAsksForFrames(t *testing.T) {
	a, _ := accountsApp(t)
	if d, ok := a.frameDelay(nowTime()); !ok || d != frameInterval {
		t.Fatalf("with the account list up the frame delay is %v (%v)", d, ok)
	}
}

// ---------- 9.7 choosing from the list ----------

func TestChoosingAnAccountWithoutASavedSignInAsksForItsPassword(t *testing.T) {
	a, _ := accountsApp(t)
	a.handleKey(context.Background(), specialKey(tcell.KeyDown)) // bob
	a.handleKey(context.Background(), specialKey(tcell.KeyEnter))
	m := a.ui.Login
	if a.ui.Dialog != views.DialogLogin || m.Username != "bob" || m.Remembered || m.Focus != views.LoginFocusPassword {
		t.Fatalf("choosing bob: dialog %v login %+v", a.ui.Dialog, m)
	}
}

func TestUseAnotherAccountOpensABlankPanel(t *testing.T) {
	a, _ := accountsApp(t)
	for i := 0; i < 3; i++ {
		a.handleKey(context.Background(), specialKey(tcell.KeyDown))
	}
	a.handleKey(context.Background(), specialKey(tcell.KeyEnter))
	m := a.ui.Login
	if a.ui.Dialog != views.DialogLogin || m.Username != "" || m.Focus != views.LoginFocusUsername {
		t.Fatalf("use another account: dialog %v login %+v", a.ui.Dialog, m)
	}
}

// Esc from the list goes to the sign-in panel for the account it started on.
func TestEscFromTheAccountListGoesToItsSignIn(t *testing.T) {
	a, _ := accountsApp(t)
	a.handleKey(context.Background(), specialKey(tcell.KeyEscape))
	m := a.ui.Login
	if a.ui.Dialog != views.DialogLogin || m.Username != "alice" || !m.Remembered || m.Focus != views.LoginFocusSubmit {
		t.Fatalf("Esc: dialog %v login %+v", a.ui.Dialog, m)
	}
}

// "Switch account" on the "me" page goes straight to the account list — no
// confirmation — and the exit animation lands on the list's shape.
func TestSwitchingAccountsGoesStraightToTheList(t *testing.T) {
	a, _ := pagesApp(t)
	a.cfg.Accounts = []string{"alice", "bob"}
	a.st.Username = "alice"
	a.showEntryPage(client.EntryMe)
	a.ui.Me.Selected = 0
	a.handleKey(nil, specialKey(tcell.KeyUp)) // wraps to sign out, at the bottom
	a.handleKey(nil, specialKey(tcell.KeyUp)) // switch account, just above sign out
	if views.MeItems[a.ui.Me.Selected] != views.MeSwitchAccount {
		t.Fatal("the cursor is not on switch account")
	}
	a.handleKey(nil, specialKey(tcell.KeyEnter))
	if a.ui.Dialog == views.DialogLogout {
		t.Fatal("switching asked for confirmation")
	}
	if a.ui.Dialog != views.DialogAccounts || a.ui.Accounts.Cursor != 0 {
		t.Fatalf("after switching: dialog %v cursor %d", a.ui.Dialog, a.ui.Accounts.Cursor)
	}
	if a.anim.exit == nil {
		t.Fatal("no exit animation played on switching")
	}
	w, h := a.scr.Size()
	if a.stagePanel(w, h) != views.AccountsPanelRect(w, h, 2) {
		t.Fatal("the exit lands on the sign-in panel's shape, not the account list's")
	}
	if a.st.Focus != client.FocusList || len(a.st.Conversations) != 0 {
		t.Fatal("the old session's state was not cleared")
	}
}

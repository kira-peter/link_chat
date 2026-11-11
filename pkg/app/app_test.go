package app

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/client"
	"link-chat/pkg/server"
	"link-chat/pkg/storage"
	"link-chat/pkg/tui/layout"
	"link-chat/pkg/tui/views"
)

// startServer runs a real chat server on a loopback port for end-to-end tests.
func startServer(t *testing.T) *httptest.Server {
	t.Helper()
	cfg := server.DefaultConfig()
	cfg.DataDir = t.TempDir()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	st, err := storage.OpenJSON(cfg.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("close storage: %v", err)
		}
	})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := server.New(cfg, st, logger)
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		// Close client sockets before storage so no handler writes after Close.
		hs.CloseClientConnections()
		hs.Close()
	})
	return hs
}

func newTestScreen(t *testing.T, w, h int) tcell.SimulationScreen {
	t.Helper()
	s := tcell.NewSimulationScreen("UTF-8")
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	s.SetSize(w, h)
	t.Cleanup(s.Fini)
	return s
}

// screenText flattens the simulated screen into one string for assertions.
func screenText(s tcell.SimulationScreen) string {
	s.Show()
	cells, width, height := s.GetContents()
	var b strings.Builder
	skip := 0
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			if skip > 0 {
				skip--
				continue
			}
			rs := cells[y*width+x].Runes
			if len(rs) == 0 {
				b.WriteRune(' ')
				continue
			}
			b.WriteRune(rs[0])
			if layout.RuneWidth(rs[0]) == 2 {
				skip = 1
			}
		}
		b.WriteRune('\n')
	}
	return b.String()
}

// waitFor polls a condition, which is how these tests wait for network round
// trips without sleeping a fixed amount.
func waitFor(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// The whole point of the client: sign in, see conversations, send a message, and
// see it come back. This drives the real App against a real server.
func TestAppLoginCreateChannelAndSendMessage(t *testing.T) {
	hs := startServer(t)
	scr := newTestScreen(t, 100, 30)

	a := New(scr, Config{
		Server:   "ws" + strings.TrimPrefix(hs.URL, "http") + "/ws",
		Theme:    "ink",
		Language: "zh",
	}, filepath.Join(t.TempDir(), "client.json"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.Run(ctx)

	// The login dialog must be up because there is no token.
	waitFor(t, 3*time.Second, "login dialog", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.ui.Dialog == views.DialogLogin
	})

	// Drive the login form as a user would: fill fields, submit.
	a.mu.Lock()
	a.ui.Register = views.RegisterModel{
		Username: "alice", Password: "correct horse battery",
		Question: "Test question?", Answer: "test answer",
	}
	a.mu.Unlock()
	a.submitRegister(ctx)

	waitFor(t, 15*time.Second, "authenticated", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.st.State == client.StateReady
	})

	if a.st.Token == "" {
		t.Fatal("no token persisted after login")
	}
	if a.st.Username != "alice" {
		t.Fatalf("wrong username: %q", a.st.Username)
	}

	// Create a channel through the prompt dialog path.
	a.mu.Lock()
	a.ui.Dialog = views.DialogChannel
	a.ui.Prompt = views.PromptModel{Title: "new", Label: "name", Value: "general"}
	a.mu.Unlock()
	a.submitPrompt(ctx, true)

	waitFor(t, 15*time.Second, "channel created and opened", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.st.ActiveID != "" && a.st.Active() != nil
	})

	cv := a.st.Active()
	if cv.Name != "general" {
		t.Fatalf("wrong channel: %q", cv.Name)
	}

	// Type and send.
	a.mu.Lock()
	a.st.RestoreInput("hello from the test")
	a.mu.Unlock()
	a.sendCurrent(ctx)

	waitFor(t, 15*time.Second, "message stored", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		c := a.st.Active()
		if c == nil {
			return false
		}
		for _, m := range c.Messages {
			if m.Body == "hello from the test" {
				return true
			}
		}
		return false
	})

	// The composer must be empty again after a successful send.
	if got := a.st.InputText(); got != "" {
		t.Fatalf("composer not cleared: %q", got)
	}

	// And the message must actually be on screen. Draw a frame now rather than
	// trusting the loop to have drawn one since the message landed.
	a.mu.Lock()
	a.render()
	a.mu.Unlock()
	screen := screenText(scr)
	if !strings.Contains(screen, "hello from the test") {
		t.Fatalf("message not rendered:\n%s", screen)
	}
	if !strings.Contains(screen, "general") {
		t.Fatalf("channel name not rendered:\n%s", screen)
	}
}

// A second client must see the first client's message arrive live.
func TestTwoClientsExchangeMessages(t *testing.T) {
	hs := startServer(t)
	url := "ws" + strings.TrimPrefix(hs.URL, "http") + "/ws"

	scrA := newTestScreen(t, 100, 30)
	a := New(scrA, Config{Server: url, Theme: "ink", Language: "zh"}, filepath.Join(t.TempDir(), "a.json"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.Run(ctx)

	a.mu.Lock()
	a.ui.Register = views.RegisterModel{
		Username: "alice", Password: "correct horse battery",
		Question: "Test question?", Answer: "test answer",
	}
	a.mu.Unlock()
	a.submitRegister(ctx)
	waitFor(t, 15*time.Second, "alice ready", func() bool { return a.st.State == client.StateReady })

	a.mu.Lock()
	a.ui.Prompt = views.PromptModel{Value: "lobby"}
	a.mu.Unlock()
	a.submitPrompt(ctx, true)
	waitFor(t, 15*time.Second, "lobby created", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.st.Active() != nil && a.st.Active().Name == "lobby"
	})

	scrB := newTestScreen(t, 100, 30)
	b := New(scrB, Config{Server: url, Theme: "ink", Language: "zh"}, filepath.Join(t.TempDir(), "b.json"))
	ctxB, cancelB := context.WithCancel(context.Background())
	defer cancelB()
	go b.Run(ctxB)

	b.mu.Lock()
	b.ui.Register = views.RegisterModel{
		Username: "bob", Password: "correct horse battery",
		Question: "Test question?", Answer: "test answer",
	}
	b.mu.Unlock()
	b.submitRegister(ctxB)
	waitFor(t, 15*time.Second, "bob ready", func() bool { return b.st.State == client.StateReady })

	// Bob joins alice's channel the way a user does: pick it in the list, Enter.
	var lobbyID string
	a.mu.Lock()
	lobbyID = a.st.ActiveID
	a.mu.Unlock()

	openViaList(t, b, ctxB, lobbyID)

	// Wait for bob to actually be joined, not merely to have the conversation
	// open: joining is an async round trip, and sending before it lands would
	// race the subscription.
	waitFor(t, 15*time.Second, "bob subscribed", func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		if b.st.ActiveID != lobbyID {
			return false
		}
		for _, m := range b.st.Active().Messages {
			if m.Body == "joined" {
				return true
			}
		}
		return false
	})

	// Alice speaks; bob must receive it.
	a.mu.Lock()
	a.st.RestoreInput("hi bob")
	a.mu.Unlock()
	a.sendCurrent(ctx)

	waitFor(t, 15*time.Second, "bob received the message", func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		c := b.st.Active()
		if c == nil {
			return false
		}
		for _, m := range c.Messages {
			if m.Body == "hi bob" {
				return true
			}
		}
		return false
	})

	b.mu.Lock()
	b.render()
	b.mu.Unlock()
	if screen := screenText(scrB); !strings.Contains(screen, "hi bob") {
		t.Fatalf("bob's screen does not show the message:\n%s", screen)
	}
}

// A stored token must let the next launch skip the password.
func TestResumeWithStoredToken(t *testing.T) {
	hs := startServer(t)
	url := "ws" + strings.TrimPrefix(hs.URL, "http") + "/ws"
	cfgPath := filepath.Join(t.TempDir(), "client.json")

	scr1 := newTestScreen(t, 100, 30)
	a := New(scr1, Config{Server: url, Theme: "ink", Language: "zh"}, cfgPath)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.Run(ctx)

	a.mu.Lock()
	a.ui.Register = views.RegisterModel{
		Username: "carol", Password: "correct horse battery",
		Question: "Test question?", Answer: "test answer",
	}
	a.mu.Unlock()
	a.submitRegister(ctx)
	waitFor(t, 15*time.Second, "first session ready", func() bool { return a.st.State == client.StateReady })

	// Reload from disk the way a restart would. The token is in the credential
	// store, not the config file, so "was it saved" means "is it in the store".
	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if cfg.Token != "" {
		t.Fatalf("token leaked into the config file: %q", cfg.Token)
	}
	stored, err := tokenStore{backend: credentials(cfgPath), instance: cfg.Instance}.load()
	if err != nil || stored == "" {
		t.Fatalf("token was not saved to the credential store: %v", err)
	}

	scr2 := newTestScreen(t, 100, 30)
	b := New(scr2, cfg, cfgPath)
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	go b.Run(ctx2)

	// Startup must only show the panel, remembered and ready for one Enter. It
	// must not sign in by itself: that would displace the account's session
	// elsewhere without the user choosing to.
	b.mu.Lock()
	if b.ui.Dialog != views.DialogLogin || !b.ui.Login.Remembered ||
		b.ui.Login.Username != "carol" || b.ui.Login.Focus != views.LoginFocusSubmit {
		t.Fatalf("startup panel not in remembered state: dialog=%v login=%+v", b.ui.Dialog, b.ui.Login)
	}
	b.mu.Unlock()
	time.Sleep(500 * time.Millisecond)
	b.mu.Lock()
	if b.st.State == client.StateReady {
		t.Fatal("client signed in at startup without Enter")
	}
	b.mu.Unlock()

	// One Enter on the button signs in with the saved session, no password.
	b.mu.Lock()
	b.handleKey(ctx2, specialKey(tcell.KeyEnter))
	b.mu.Unlock()

	waitFor(t, 20*time.Second, "resumed after Enter", func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		return b.st.State == client.StateReady && b.st.Username == "carol"
	})
}

// registerAndQuit registers a user in a throwaway client so a token is left in
// the credential store, then returns the config a restart would load.
func registerAndQuit(t *testing.T, url, cfgPath, user string) Config {
	t.Helper()
	scr := newTestScreen(t, 100, 30)
	a := New(scr, Config{Server: url, Theme: "ink", Language: "zh"}, cfgPath)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.Run(ctx)
	a.mu.Lock()
	a.ui.Register = views.RegisterModel{
		Username: user, Password: "correct horse battery",
		Question: "Test question?", Answer: "test answer",
	}
	a.mu.Unlock()
	a.submitRegister(ctx)
	waitFor(t, 15*time.Second, user+" ready", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.st.State == client.StateReady
	})
	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	return cfg
}

// A dead saved token must fall back to a password login: the token is cleared,
// the reason shown, and the caret moved to the password row.
func TestStaleTokenFallsBackToPassword(t *testing.T) {
	hs := startServer(t)
	url := "ws" + strings.TrimPrefix(hs.URL, "http") + "/ws"
	cfgPath := filepath.Join(t.TempDir(), "client.json")
	cfg := registerAndQuit(t, url, cfgPath, "dave")

	store := tokenStore{backend: credentials(cfgPath), instance: cfg.Instance}
	if err := store.save("not-a-real-token"); err != nil {
		t.Fatalf("poison token: %v", err)
	}

	b := New(newTestScreen(t, 100, 30), cfg, cfgPath)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)

	b.mu.Lock()
	remembered := b.ui.Login.Remembered
	b.mu.Unlock()
	if !remembered {
		t.Fatal("panel should offer the saved session before it is tried")
	}
	b.submitLogin(ctx)

	waitFor(t, 20*time.Second, "fallback to password", func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		return !b.ui.Login.Busy && b.ui.Login.Err != ""
	})

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.ui.Login.Remembered || b.ui.Login.Focus != views.LoginFocusPassword {
		t.Fatalf("not in password mode: %+v", b.ui.Login)
	}
	if b.st.Token != "" {
		t.Fatal("dead token was kept")
	}
	if b.st.State == client.StateReady {
		t.Fatal("client marked ready with a dead token")
	}
	if got, _ := store.load(); got != "" {
		t.Fatalf("dead token still in the credential store: %q", got)
	}
}

// Opening a second client must not sign it in, so it cannot displace a session
// that is live elsewhere.
func TestStartupDoesNotDisplaceLiveSession(t *testing.T) {
	hs := startServer(t)
	url := "ws" + strings.TrimPrefix(hs.URL, "http") + "/ws"
	cfgPath := filepath.Join(t.TempDir(), "client.json")

	scr1 := newTestScreen(t, 100, 30)
	a := New(scr1, Config{Server: url, Theme: "ink", Language: "zh"}, cfgPath)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.Run(ctx)
	a.mu.Lock()
	a.ui.Register = views.RegisterModel{
		Username: "erin", Password: "correct horse battery",
		Question: "Test question?", Answer: "test answer",
	}
	a.mu.Unlock()
	a.submitRegister(ctx)
	waitFor(t, 15*time.Second, "first client ready", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.st.State == client.StateReady
	})
	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}

	// Start a second client on the same saved session and never press Enter.
	b := New(newTestScreen(t, 100, 30), cfg, cfgPath)
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	go b.Run(ctx2)
	time.Sleep(2 * time.Second)

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.st.State != client.StateReady {
		t.Fatalf("starting a second client displaced the first (state=%v)", a.st.State)
	}
}

// A wrong password must leave the dialog open with an explanation, and keep the
// username so the user only retypes the secret.
func TestWrongPasswordKeepsDialogWithError(t *testing.T) {
	hs := startServer(t)
	url := "ws" + strings.TrimPrefix(hs.URL, "http") + "/ws"

	scr := newTestScreen(t, 100, 30)
	a := New(scr, Config{Server: url, Theme: "ink", Language: "zh"}, filepath.Join(t.TempDir(), "c.json"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.Run(ctx)

	a.mu.Lock()
	a.ui.Login.Username = "nobody"
	a.ui.Login.Password = "wrong wrong wrong"
	a.ui.Login.Focus = views.LoginFocusPassword
	a.mu.Unlock()
	a.submitLogin(ctx)

	waitFor(t, 20*time.Second, "login error shown", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.ui.Login.Err != ""
	})

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.ui.Dialog != views.DialogLogin {
		t.Fatal("dialog closed on failure")
	}
	if a.ui.Login.Username != "nobody" {
		t.Fatalf("username cleared: %q", a.ui.Login.Username)
	}
	if a.st.State == client.StateReady {
		t.Fatal("client marked ready after a failed login")
	}
}

// The full recovery journey a user actually performs: register, forget the
// password, answer the question, set a new one, sign in.
func TestPasswordRecoveryEndToEnd(t *testing.T) {
	hs := startServer(t)
	url := "ws" + strings.TrimPrefix(hs.URL, "http") + "/ws"

	scr := newTestScreen(t, 100, 30)
	a := New(scr, Config{Server: url, Theme: "ink", Language: "zh"}, filepath.Join(t.TempDir(), "r.json"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.Run(ctx)

	a.mu.Lock()
	a.ui.Register = views.RegisterModel{
		Username: "dave", Password: "forgotten password",
		Question: "First pet?", Answer: "Blue Harbor",
	}
	a.mu.Unlock()
	a.submitRegister(ctx)
	waitFor(t, 15*time.Second, "registered", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.st.State == client.StateReady
	})

	// Step 1: ask for the question.
	a.mu.Lock()
	a.openRecover()
	a.ui.Recover.Username = "dave"
	a.mu.Unlock()
	a.recoverAskQuestion(ctx)

	waitFor(t, 15*time.Second, "question shown", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.ui.Recover.Step == views.RecoverStepAnswer && a.ui.Recover.Question != ""
	})
	if got := a.ui.Recover.Question; got != "First pet?" {
		t.Fatalf("wrong question: %q", got)
	}

	// Step 2: answer it.
	a.mu.Lock()
	a.ui.Recover.Answer = "blue harbor"
	a.mu.Unlock()
	a.recoverVerify(ctx)

	waitFor(t, 15*time.Second, "reset token issued", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.ui.Recover.Step == views.RecoverStepPassword && a.ui.Recover.ResetToken != ""
	})

	// Step 3: mismatched confirmation must not submit.
	a.mu.Lock()
	a.ui.Recover.NewPass = "brand new password"
	a.ui.Recover.Confirm = "different"
	a.mu.Unlock()
	a.recoverReset(ctx)
	a.mu.Lock()
	mismatch := a.ui.Recover.Err
	stillStep3 := a.ui.Recover.Step == views.RecoverStepPassword
	a.mu.Unlock()
	if mismatch == "" || !stillStep3 {
		t.Fatal("mismatched confirmation was not caught locally")
	}

	// Step 3 for real.
	a.mu.Lock()
	a.ui.Recover.Confirm = "brand new password"
	a.mu.Unlock()
	a.recoverReset(ctx)

	waitFor(t, 15*time.Second, "back at login with the new password set", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.ui.Dialog == views.DialogLogin && a.ui.Login.Username == "dave"
	})

	// And the new password actually works.
	a.mu.Lock()
	a.ui.Login.Password = "brand new password"
	a.ui.Login.Focus = views.LoginFocusSubmit
	a.mu.Unlock()
	a.submitLogin(ctx)

	waitFor(t, 20*time.Second, "signed in with the new password", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.st.State == client.StateReady
	})
}

// Two instances on one machine must be able to hold two accounts.
func TestTwoInstancesHaveSeparateCredentials(t *testing.T) {
	dir := t.TempDir()
	p1, err := InstanceConfigPath("alice")
	if err != nil {
		t.Fatal(err)
	}
	p2, err := InstanceConfigPath("bob")
	if err != nil {
		t.Fatal(err)
	}
	if p1 == p2 {
		t.Fatalf("instances share a credentials file: %s", p1)
	}

	// Each instance keeps its credential under its own name, so the two never
	// collide. Tokens are no longer written to the config file at all.
	if err := SaveConfig(p1, rememberConfig("ws://a/ws", "alice")); err != nil {
		t.Fatal(err)
	}
	if err := SaveConfig(p2, rememberConfig("ws://b/ws", "bob")); err != nil {
		t.Fatal(err)
	}
	c1, err := LoadConfig(p1)
	if err != nil {
		t.Fatal(err)
	}
	c2, err := LoadConfig(p2)
	if err != nil {
		t.Fatal(err)
	}
	if c1.Server != "ws://a/ws" || c2.Server != "ws://b/ws" {
		t.Fatalf("configs crossed: %q / %q", c1.Server, c2.Server)
	}

	// Store a token per instance and read each back independently.
	backend := credentials(p1)
	s1 := tokenStore{backend: backend, instance: "alice"}
	s2 := tokenStore{backend: backend, instance: "bob"}
	if err := s1.save("token-a"); err != nil {
		t.Fatal(err)
	}
	if err := s2.save("token-b"); err != nil {
		t.Fatal(err)
	}
	got1, err := s1.load()
	if err != nil || got1 != "token-a" {
		t.Fatalf("instance alice read %q, %v", got1, err)
	}
	got2, err := s2.load()
	if err != nil || got2 != "token-b" {
		t.Fatalf("instance bob read %q, %v", got2, err)
	}
	_ = dir
}

func TestInstanceConfigPathDefaults(t *testing.T) {
	p, err := InstanceConfigPath("")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(p, DefaultInstance+".json") {
		t.Fatalf("default instance path = %q", p)
	}
	// Path traversal must not be possible through the instance name.
	for _, bad := range []string{"../evil", "a/b", "a\b", ""} {
		if _, err := InstanceConfigPath(bad); err == nil && bad != "" {
			t.Fatalf("accepted unsafe instance name %q", bad)
		}
	}
}

// Being signed in elsewhere must stop reconnection and show the notice, not
// silently reconnect forever (spec login-session / 被顶掉一端的后续行为).
func TestSessionReplacedStopsReconnectAndNotifies(t *testing.T) {
	hs := startServer(t)
	url := "ws" + strings.TrimPrefix(hs.URL, "http") + "/ws"
	cfgPath := filepath.Join(t.TempDir(), "c.json")

	scrA := newTestScreen(t, 100, 30)
	a := New(scrA, Config{Server: url, Theme: "ink", Language: "zh"}, cfgPath)
	a.SetDevice("first")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.Run(ctx)

	a.mu.Lock()
	a.ui.Register = views.RegisterModel{
		Username: "erin", Password: "correct horse battery",
		Question: "Q?", Answer: "a",
	}
	a.mu.Unlock()
	a.submitRegister(ctx)
	waitFor(t, 15*time.Second, "first session ready", func() bool { return a.st.State == client.StateReady })

	// A second client signs in as the same account.
	scrB := newTestScreen(t, 100, 30)
	b := New(scrB, Config{Server: url, Theme: "ink", Language: "zh"}, filepath.Join(t.TempDir(), "d.json"))
	b.SetDevice("second")
	ctxB, cancelB := context.WithCancel(context.Background())
	defer cancelB()
	go b.Run(ctxB)

	b.mu.Lock()
	b.ui.Register = views.RegisterModel{
		Username: "erin", Password: "correct horse battery",
		Question: "Q?", Answer: "a",
	}
	b.mu.Unlock()
	// Registering the same name fails; sign in instead.
	b.mu.Lock()
	b.ui.Login = views.LoginModel{Username: "erin", Password: "correct horse battery", Focus: views.LoginFocusSubmit}
	b.ui.Register = views.RegisterModel{}
	b.mu.Unlock()
	b.submitLogin(ctxB)
	waitFor(t, 20*time.Second, "second session ready", func() bool { return b.st.State == client.StateReady })

	// The first client must learn it was displaced, and must not be reconnecting.
	waitFor(t, 20*time.Second, "displacement notice", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.ui.Dialog == views.DialogSessionReplaced
	})

	a.mu.Lock()
	if a.st.State != client.StateAuthFailed {
		t.Errorf("state = %v, want auth failed", a.st.State)
	}
	if a.st.RetryIn != 0 {
		t.Error("client is counting down to a reconnect despite being displaced")
	}
	if a.st.Token != "" {
		t.Error("token was not cleared")
	}
	if a.ui.SessionReplaced.Location == "" {
		t.Error("notice has no location")
	}
	if a.ui.SessionReplaced.Device != "second" {
		t.Errorf("notice should name the new client, got %q", a.ui.SessionReplaced.Device)
	}
	a.mu.Unlock()

	// The cleared token must be persisted so a restart does not resume it.
	saved, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Token != "" {
		t.Fatalf("token persisted after displacement: %q", saved.Token)
	}
}

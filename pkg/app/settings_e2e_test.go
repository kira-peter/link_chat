package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"link-chat/pkg/protocol"
	"link-chat/pkg/tui/views"
)

// runApp starts a client on url with cfg and stops it when the test ends.
func runApp(t *testing.T, url string, cfg Config, file string) (*App, context.Context) {
	t.Helper()
	cfg.Server = url
	if cfg.Theme == "" {
		cfg.Theme, cfg.Language = "ink", "zh"
	}
	cfg.SetRemember(true)
	a := New(newTestScreen(t, 100, 30), cfg, filepath.Join(appTempDir(t), file))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = a.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done; time.Sleep(50 * time.Millisecond) })
	return a, ctx
}

// waitSettings waits until the app's settings in effect satisfy cond.
func waitSettings(t *testing.T, a *App, what string, cond func(Config) bool) {
	t.Helper()
	waitFor(t, 10*time.Second, what, func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return cond(a.cfg)
	})
}

// Against a real server: what A changes, B — another machine, signing in to the
// same account afterwards — has once signed in.
func TestSettingsFollowTheAccountToAnotherMachine(t *testing.T) {
	hs := startServer(t)
	url := "ws" + strings.TrimPrefix(hs.URL, "http") + "/ws"

	a, actx := runApp(t, url, Config{}, "a.json")
	registerAndWait(t, a, actx, "quinn")
	// As the key handler does: on the main loop, without the lock.
	a.toggleLanguage() // zh -> en, uploaded
	a.uploadSettings(a.applySetting(views.SetMsgBorder, "hide"))
	waitFor(t, 10*time.Second, "both changes saved on the server", func() bool {
		f, err := a.settingsCall(protocol.OpSettingsGet, nil)
		var res protocol.SettingsGetRes
		return err == nil && decodeData(f, &res) == nil &&
			res.Settings["language"] == "en" && res.Settings["msg_border"] == "hide"
	})

	// B is a fresh machine: nothing cached, defaults in effect until signed in.
	b, bctx := runApp(t, url, Config{}, "b.json")
	b.mu.Lock()
	b.ui.Login = views.LoginModel{Username: "quinn", Password: "correct horse battery", Focus: views.LoginFocusSubmit}
	b.mu.Unlock()
	b.submitLogin(bctx)
	waitSettings(t, b, "B to take quinn's settings", func(c Config) bool {
		return c.Language == "en" && c.Layout().NoBorder
	})
	b.mu.Lock()
	lang := b.lang
	b.mu.Unlock()
	if lang != "en" {
		t.Fatalf("B's interface is in %s, want en", lang)
	}
}

// An existing user's settings, made before they travelled with the account, are
// what the server keeps after the first sign-in, and the screen does not change.
func TestFirstSignInKeepsAnOldUsersSettings(t *testing.T) {
	hs := startServer(t)
	url := "ws" + strings.TrimPrefix(hs.URL, "http") + "/ws"
	a, ctx := runApp(t, url, Config{Theme: "paper", Language: "zh", BubbleWidth: 80}, "old.json")
	registerAndWait(t, a, ctx, "rowan")
	waitFor(t, 10*time.Second, "the first upload", func() bool {
		f, err := a.settingsCall(protocol.OpSettingsGet, nil)
		var res protocol.SettingsGetRes
		return err == nil && decodeData(f, &res) == nil && res.Exists &&
			res.Settings["theme"] == "paper" && res.Settings["bubble_width"] == "80"
	})
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.st.ThemeName != "paper" || a.cfg.Layout().BubblePercent != 80 {
		t.Fatalf("the first sign-in changed the screen: theme %q width %d", a.st.ThemeName, a.cfg.Layout().BubblePercent)
	}
}

// A new client on a server without settings sync: settings stay on this machine
// and no failure is reported.
func TestOlderServerKeepsSettingsLocalWithoutComplaint(t *testing.T) {
	hs := startServer(t)
	url := "ws" + strings.TrimPrefix(hs.URL, "http") + "/ws"
	a, ctx := runApp(t, url, Config{}, "o.json")
	// The server answers everything else for real; the settings requests get the
	// answer a server from before them gives any unknown request.
	a.settingsCallFn = func(op string, data any) (protocol.Frame, error) {
		return protocol.NewErrorCodeFrame("x", protocol.CodeInvalidArgument)
	}
	registerAndWait(t, a, ctx, "sage")
	waitFor(t, 10*time.Second, "the older server to be recognised", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.settingsUnsupported
	})
	a.toggleLanguage()
	time.Sleep(200 * time.Millisecond)
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.lang != "en" || a.cfg.AccountSettings["sage"]["language"] != "en" {
		t.Fatal("the change did not take effect on this machine")
	}
	if a.st.StatusErr {
		t.Fatalf("a failure was reported: %q", a.st.StatusMsg)
	}
}

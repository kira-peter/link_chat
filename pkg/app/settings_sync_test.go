package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/protocol"
	"link-chat/pkg/tui/views"
)

// fakeSettings stands in for the server's settings requests.
type fakeSettings struct {
	mu     sync.Mutex
	stored protocol.Settings // nil: never saved
	fail   bool              // every call fails
	old    bool              // an older server: settings.get is unknown
	puts   []protocol.Settings
	gets   int
}

func (f *fakeSettings) call(op string, data any) (protocol.Frame, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return protocol.Frame{}, errNoTransport
	}
	switch op {
	case protocol.OpSettingsGet:
		f.gets++
		if f.old {
			return protocol.NewErrorCodeFrame("x", protocol.CodeInvalidArgument)
		}
		if f.stored == nil {
			return protocol.NewRes("x", protocol.SettingsGetRes{Settings: protocol.Settings{}})
		}
		return protocol.NewRes("x", protocol.SettingsGetRes{Settings: f.stored.Normalize(), Exists: true})
	case protocol.OpSettingsPut:
		req := data.(protocol.SettingsPutReq)
		f.puts = append(f.puts, req.Settings)
		if f.stored == nil {
			f.stored = protocol.Settings{}
		}
		for k, v := range req.Settings {
			f.stored[k] = v
		}
		return protocol.NewRes("x", protocol.SettingsRes{Settings: f.stored.Normalize()})
	}
	return protocol.NewErrorCodeFrame("x", protocol.CodeInvalidArgument)
}

func (f *fakeSettings) putCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.puts)
}

// waitUntil polls cond for up to two seconds.
func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// syncApp is a signed-in app as alice, talking to f.
func syncApp(t *testing.T, f *fakeSettings) *App {
	t.Helper()
	a, _ := newKeyTestApp(t)
	a.st.Username = "alice"
	a.settingsCallFn = f.call
	return a
}

// ---------- 3.1 config and migration ----------

// A config from before settings travelled with the account keeps looking the
// same: its settings become the most recent account's cache.
func TestOldConfigSettingsMigrateToTheAccountsCache(t *testing.T) {
	p := filepath.Join(appTempDir(t), "old.json")
	old := `{"server":"ws://x/ws","theme":"paper","language":"en","motion_off":true,
		"bubble_width":80,"density":"compact","accounts":["alice","bob"]}`
	if err := os.WriteFile(p, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Theme != "paper" || cfg.Language != "en" || !cfg.MotionOff || cfg.Layout().BubblePercent != 80 || !cfg.Layout().Compact {
		t.Fatalf("the settings in effect changed on upgrade: %+v", cfg)
	}
	c := cfg.AccountSettings["alice"]
	if c["theme"] != "paper" || c["bubble_width"] != "80" || c["motion"] != "off" {
		t.Fatalf("alice's cache is %v", c)
	}
	if _, ok := cfg.AccountSettings["bob"]; ok {
		t.Fatal("the settings were also given to an account they did not belong to")
	}

	// Saved and loaded again: the same, and the cache holds no token.
	cfg.Token = "secret"
	if err := SaveConfig(p, cfg); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if bytes.Contains(b, []byte("secret")) {
		t.Fatal("a token was written to the config file")
	}
	again, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if again.AccountSettings["alice"]["theme"] != "paper" || again.Theme != "paper" {
		t.Fatalf("round trip lost the cache: %+v", again.AccountSettings)
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// ---------- 3.2 one path for applying settings ----------

func TestApplyingSettingsTakesEffectAtOnce(t *testing.T) {
	a := syncApp(t, &fakeSettings{})
	s := a.cfg.accountSettings()
	s["theme"], s["language"], s["motion"], s["msg_border"] = "paper", "en", "off", "hide"
	a.applyAccountSettings(s)

	if a.st.ThemeName != "paper" || a.tokens.Name != "paper" {
		t.Fatalf("theme %q tokens %q", a.st.ThemeName, a.tokens.Name)
	}
	if string(a.lang) != "en" || !a.st.MotionOff || !a.cfg.Layout().NoBorder {
		t.Fatalf("lang %s motionOff %v layout %+v", a.lang, a.st.MotionOff, a.cfg.Layout())
	}
	if a.cfg.AccountSettings["alice"]["theme"] != "paper" {
		t.Fatal("the account's cache was not updated")
	}
}

// ---------- 3.3 the cache before and between sign-ins ----------

// Starting the client shows the stage in the last account's settings.
func TestStartupUsesTheLastAccountsCache(t *testing.T) {
	p := filepath.Join(appTempDir(t), "c.json")
	cfg := freshConfig()
	cfg.Accounts = []string{"alice"}
	cfg.AccountSettings = map[string]protocol.Settings{"alice": {"theme": "paper"}}
	if err := SaveConfig(p, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	a := New(newTestScreen(t, 100, 30), loaded, p)
	if a.st.ThemeName != "paper" || a.tokens.Name != "paper" {
		t.Fatalf("the stage starts in %q", a.st.ThemeName)
	}
}

// Switching to bob shows bob's settings before anything is downloaded.
func TestSwitchingAccountUsesThatAccountsCache(t *testing.T) {
	a := syncApp(t, &fakeSettings{})
	a.cfg.Accounts = []string{"alice", "bob"}
	a.cfg.AccountSettings = map[string]protocol.Settings{
		"alice": {"language": "en"},
		"bob":   {"language": "zh", "theme": "paper"},
	}
	a.openLoginFor("alice")
	if string(a.lang) != "en" {
		t.Fatalf("alice's cache gave %s", a.lang)
	}
	a.openLoginFor("bob")
	if string(a.lang) != "zh" || a.st.ThemeName != "paper" {
		t.Fatalf("bob's cache gave %s / %s", a.lang, a.st.ThemeName)
	}
	// An account with no cache gets the defaults, not the previous account's.
	a.openLoginFor("carol")
	if a.st.ThemeName != "ink" || string(a.lang) != "zh" {
		t.Fatalf("carol, with no cache, got %s / %s", a.st.ThemeName, a.lang)
	}
}

// ---------- 4.1 after sign-in ----------

func TestSignInDownloadsAndAppliesTheServersSettings(t *testing.T) {
	f := &fakeSettings{stored: protocol.Settings{"theme": "paper", "time_format": "12h"}}
	a := syncApp(t, f)
	a.syncSettingsAfterSignIn()
	if a.st.ThemeName != "paper" || a.cfg.Layout().Clock24 {
		t.Fatalf("after download: theme %q clock24 %v", a.st.ThemeName, a.cfg.Layout().Clock24)
	}
	if a.cfg.AccountSettings["alice"]["theme"] != "paper" {
		t.Fatal("the download did not reach the cache")
	}
	if f.putCount() != 0 {
		t.Fatal("settings the server already had were uploaded")
	}
}

func TestFirstSignInUploadsThisMachinesSettings(t *testing.T) {
	f := &fakeSettings{}
	a := syncApp(t, f)
	a.cfg.BubbleWidth = 80
	a.syncSettingsAfterSignIn()
	if a.cfg.Layout().BubblePercent != 80 {
		t.Fatal("the first sign-in changed the settings in effect")
	}
	if f.putCount() != 1 || f.puts[0]["bubble_width"] != "80" || len(f.puts[0]) != len(protocol.SettingSpecs) {
		t.Fatalf("uploaded %v, want this machine's complete settings once", f.puts)
	}
}

func TestDownloadFailureKeepsTheCacheQuietly(t *testing.T) {
	a := syncApp(t, &fakeSettings{fail: true})
	a.cfg.Theme = "paper"
	a.st.ThemeName = "paper"
	a.syncSettingsAfterSignIn()
	if a.st.ThemeName != "paper" || a.st.StatusMsg != "" {
		t.Fatalf("a failed download changed the theme to %q or said %q", a.st.ThemeName, a.st.StatusMsg)
	}
}

func TestOlderServerMeansSettingsStayLocal(t *testing.T) {
	f := &fakeSettings{old: true}
	a := syncApp(t, f)
	a.syncSettingsAfterSignIn()
	if !a.settingsUnsupported {
		t.Fatal("an unknown settings.get was not taken as an older server")
	}
	a.toggleLanguage()
	time.Sleep(50 * time.Millisecond)
	if f.putCount() != 0 || a.st.StatusMsg != "" {
		t.Fatalf("an older server was sent %d saves, status %q", f.putCount(), a.st.StatusMsg)
	}
}

// ---------- 4.2 changes from the settings page ----------

// commitSetting walks the settings page to item id and commits the candidate
// steps below the current one.
func commitSetting(t *testing.T, a *App, id views.SettingID, steps int) {
	t.Helper()
	for ci, c := range views.Categories() {
		for ii, it := range c.Items {
			if it.ID != id {
				continue
			}
			a.ui.SettingsNav = views.SettingsNav{Cat: ci, Level: views.LevelItem, Item: ii}
			a.handleSettingsPageKey(specialKey(tcell.KeyRight))
			for i := 0; i < steps; i++ {
				a.handleSettingsPageKey(specialKey(tcell.KeyDown))
			}
			a.handleSettingsPageKey(specialKey(tcell.KeyEnter))
			return
		}
	}
	t.Fatalf("setting %v is not on the page", id)
}

func TestCommittingUploadsTheChange(t *testing.T) {
	f := &fakeSettings{}
	a := syncApp(t, f)
	commitSetting(t, a, views.SetTimeFormat, 1)
	waitUntil(t, "the upload", func() bool { return f.putCount() == 1 })
	if f.puts[0]["time_format"] != "12h" || len(f.puts[0]) != 1 {
		t.Fatalf("uploaded %v, want just the time format", f.puts[0])
	}
}

func TestBrowsingAndCancellingUploadNothing(t *testing.T) {
	f := &fakeSettings{}
	a := syncApp(t, f)
	cats := views.Categories()
	for ci, c := range cats {
		for ii, it := range c.Items {
			if it.ID == views.SetTheme {
				a.ui.SettingsNav = views.SettingsNav{Cat: ci, Level: views.LevelItem, Item: ii}
			}
		}
	}
	a.handleSettingsPageKey(specialKey(tcell.KeyRight))
	a.handleSettingsPageKey(specialKey(tcell.KeyDown))
	a.handleSettingsPageKey(specialKey(tcell.KeyEscape))
	time.Sleep(50 * time.Millisecond)
	if f.putCount() != 0 {
		t.Fatalf("browsing and cancelling uploaded %v", f.puts)
	}
}

func TestRememberMeIsNeverUploaded(t *testing.T) {
	f := &fakeSettings{}
	a := syncApp(t, f)
	commitSetting(t, a, views.SetRemember, 1)
	time.Sleep(50 * time.Millisecond)
	if f.putCount() != 0 {
		t.Fatalf("remember-me was uploaded: %v", f.puts)
	}
}

func TestUploadFailureKeepsTheChangeAndSaysSo(t *testing.T) {
	a := syncApp(t, &fakeSettings{fail: true})
	commitSetting(t, a, views.SetTimeFormat, 1)
	if a.cfg.Layout().Clock24 {
		t.Fatal("the change was lost when the upload failed")
	}
	waitUntil(t, "the failure notice", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.st.StatusMsg != "" && a.st.StatusErr
	})
}

// ---------- 4.3 updates pushed from another connection ----------

func TestPushedSettingsApplyAtOnce(t *testing.T) {
	a := syncApp(t, &fakeSettings{})
	ev := mustJSON(t, protocol.SettingsRes{Settings: protocol.Settings{"msg_border": "hide"}.Normalize()})
	a.onEvent(protocol.EvSettingsUpdated, ev)
	if !a.cfg.Layout().NoBorder || a.cfg.AccountSettings["alice"]["msg_border"] != "hide" {
		t.Fatal("a pushed update was not applied and cached")
	}
}

func TestPushedSettingsWaitForTheChoiceModeToEnd(t *testing.T) {
	a := syncApp(t, &fakeSettings{})
	for ci, c := range views.Categories() {
		for ii, it := range c.Items {
			if it.ID == views.SetTheme {
				a.ui.SettingsNav = views.SettingsNav{Cat: ci, Level: views.LevelItem, Item: ii}
			}
		}
	}
	a.handleSettingsPageKey(specialKey(tcell.KeyRight)) // choosing the theme

	ev := mustJSON(t, protocol.SettingsRes{Settings: protocol.Settings{"language": "en"}.Normalize()})
	a.onEvent(protocol.EvSettingsUpdated, ev)
	if string(a.lang) == "en" {
		t.Fatal("the update was applied in the middle of the choice mode")
	}
	a.handleSettingsPageKey(specialKey(tcell.KeyEscape))
	if string(a.lang) != "en" {
		t.Fatal("the deferred update was not applied when the choice mode ended")
	}
}

func TestUnknownEventsAreIgnored(t *testing.T) {
	a := syncApp(t, &fakeSettings{})
	a.onEvent("something.new", []byte(`{"x":1}`))
	if a.ui.Dialog != views.DialogNone {
		t.Fatal("an unknown event changed the screen")
	}
}

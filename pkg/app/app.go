// Package app wires the transport, the state and the views into a running
// terminal program.
//
// It owns the one loop that reads keyboard input and network events, so there is
// exactly one place where state changes, and exactly one place that triggers a
// repaint (design.md decision 8).
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/client"
	"link-chat/pkg/i18n"
	"link-chat/pkg/protocol"
	"link-chat/pkg/tui/anim"
	"link-chat/pkg/tui/layout"
	"link-chat/pkg/tui/theme"
	"link-chat/pkg/tui/views"
	"link-chat/pkg/tui/widget"
)

// Config is the client's persisted settings.
//
// The session token is deliberately absent: it is kept in the OS credential
// store, not in this file (see credential.go). Token remains only to migrate a
// file written by an older version, and is cleared as soon as it is adopted.
type Config struct {
	Server    string `json:"server"`
	Token     string `json:"token,omitempty"`
	Username  string `json:"username"`
	UserID    string `json:"user_id"`
	Theme     string `json:"theme"`
	Language  string `json:"language"`
	MotionOff bool   `json:"motion_off"`

	// Instance names the credential entry this config belongs to.
	Instance string `json:"instance,omitempty"`
	// RememberSession controls whether the token is stored at all. Off means the
	// password is required on every launch (spec account-auth / 登录凭据存储).
	//
	// It is a pointer so that "absent" and "explicitly false" can be told apart: a
	// config written before this option existed must keep auto sign-in rather than
	// silently logging everyone out on upgrade.
	RememberSession *bool `json:"remember_session,omitempty"`

	// Chat settings (spec tui-settings / 设置项及其取值). Absent means the default,
	// which is how the client looked before they existed, so an old config needs
	// no migration. A value outside the choices is treated as absent.
	BubbleWidth int    `json:"bubble_width,omitempty"` // 60, 70 or 80 (percent)
	Density     string `json:"density,omitempty"`      // "comfortable" or "compact"
	TimeFormat  string `json:"time_format,omitempty"`  // "24h" or "12h"
	// Message style. Absent means shown and the theme's colours.
	MsgBorder   string `json:"msg_border,omitempty"`   // "show" or "hide"
	MsgTime     string `json:"msg_time,omitempty"`     // "show" or "hide"
	SelfText    string `json:"self_text,omitempty"`    // a views.MessageColors name
	OtherText   string `json:"other_text,omitempty"`   // a views.MessageColors name
	SelfBorder  string `json:"self_border,omitempty"`  // a views.MessageColors name
	OtherBorder string `json:"other_border,omitempty"` // a views.MessageColors name
	// Plaza is "on" when the account lists itself for anyone to add.
	Plaza string `json:"plaza,omitempty"`
	// WheelKey opens the quick wheel; one of protocol.WheelKeys.
	WheelKey string `json:"wheel_key,omitempty"`

	// Accounts are the usernames signed in on this instance, most recent first,
	// for the account list (spec tui-account-switch / 最近账号的记录). Names only:
	// each one's token, if remembered, is in the credential store.
	Accounts []string `json:"accounts,omitempty"`

	// AccountSettings caches each account's settings by username, so the sign-in
	// stage and a switch of account show that account's settings before they are
	// downloaded (spec user-settings-sync / 本机缓存). See account_settings.go.
	AccountSettings map[string]protocol.Settings `json:"account_settings,omitempty"`
}

// maxAccounts is how many recent accounts are kept.
const maxAccounts = 10

// rememberAccount moves name to the front of the recent accounts.
func (c *Config) rememberAccount(name string) {
	if name == "" {
		return
	}
	out := []string{name}
	for _, n := range c.Accounts {
		if n != name && len(out) < maxAccounts {
			out = append(out, n)
		}
	}
	c.Accounts = out
}

// The choices for the chat settings, and their defaults.
var (
	BubbleWidths = []int{60, 70, 80}
	Densities    = []string{"comfortable", "compact"}
	TimeFormats  = []string{"24h", "12h"}
)

const (
	defaultBubbleWidth = 70
	defaultDensity     = "comfortable"
	defaultTimeFormat  = "24h"
)

// Layout is the message layout the chat settings describe. Each setting outside
// its choices falls back to its default on its own, so one bad value does not
// cost the user the others.
func (c Config) Layout() views.Layout {
	lay := views.Layout{BubblePercent: defaultBubbleWidth, Clock24: true}
	for _, w := range BubbleWidths {
		if c.BubbleWidth == w {
			lay.BubblePercent = w
		}
	}
	lay.Compact = c.Density == "compact"
	lay.Clock24 = c.TimeFormat != "12h"
	lay.NoBorder = c.MsgBorder == "hide"
	lay.NoTime = c.MsgTime == "hide"
	color := func(v string) string {
		if views.ValidMessageColor(v) && v != "default" {
			return v
		}
		return ""
	}
	lay.SelfText, lay.OtherText = color(c.SelfText), color(c.OtherText)
	lay.SelfBorder, lay.OtherBorder = color(c.SelfBorder), color(c.OtherBorder)
	return lay
}

// App is the running client.
type App struct {
	// closeTransport closes the connection when the session is left. Nil means
	// the transport's own Close; tests put a slow one here to show leaving does
	// not wait for it.
	closeTransport func()
	// onLoad, when set, is told each time a conversation's history is asked for
	// (after joining, for a channel). Tests use it to see what the network side
	// would have been asked to do.
	onLoad func(id string)
	// runCtx is the context Run was given. Background work that outlives the
	// call that started it, such as a reconnect dial, hangs off it, so quitting
	// cancels it and nothing else does.
	runCtx context.Context
	// connStatus marks the status message as being about the connection that
	// was lost, so signing in again on a new one clears it.
	connStatus bool
	cfg        Config
	cfgPath    string
	st         *client.AppState
	tr         *client.Transport
	scr        tcell.Screen
	tokens     theme.Tokens
	lang       i18n.Lang

	// device is this instance's name, reported at login so whoever gets
	// displaced can tell which client took over.
	device string

	// creds is where the session token lives. Never the config file.
	creds tokenStore

	// anim is the animation state: the sliding focus marker and the hint sweep.
	anim animation

	// onFrame, if set, is called after every frame is drawn. It exists so a test
	// can measure the real render rate instead of inferring it.
	onFrame func()

	ui UIState
	// statusUntil is when a passing status notice goes; zero keeps it.
	statusUntil time.Time
	// previewing is set while a wheel submenu puts a candidate in effect, so
	// it is not saved.
	previewing bool

	// settingsUnsupported is set when the server does not know the settings
	// requests: settings then stay on this machine and nothing is reported.
	settingsUnsupported bool
	// pendingSettings are settings from the server that arrived during the
	// choice mode, applied when it ends.
	pendingSettings protocol.Settings
	// settingsCallFn replaces the network side of the settings requests in tests.
	settingsCallFn func(op string, data any) (protocol.Frame, error)
	// socialCallFn does the same for friends, profiles and moments.
	socialCallFn func(op string, data any) (protocol.Frame, error)

	mu   sync.Mutex
	sync bool // set when a background goroutine changed state

	// version is bumped on every state change so the renderer knows to repaint.
	dirty chan struct{}
}

// DefaultInstance is the instance name used when the user does not pass -as.
const DefaultInstance = "default"

// InstanceConfigPath returns the credentials file for a named instance.
//
// Keeping one file per instance is what lets two clients on the same machine be
// two different accounts. Sharing one file meant the second window silently
// resumed the first window's session (design.md decision 1).
func InstanceConfigPath(name string) (string, error) {
	if name == "" {
		name = DefaultInstance
	}
	if !validInstanceName(name) {
		return "", fmt.Errorf("invalid instance name %q: use letters, digits, _ - .", name)
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "link-chat-" + name + ".json", nil
	}
	return filepath.Join(base, "link-chat", "instances", name+".json"), nil
}

func validInstanceName(name string) bool {
	if len(name) > 64 {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '_' || r == '-' || r == '.':
		default:
			return false
		}
	}
	return name != ""
}

// legacyConfigPath is where credentials lived before instances existed.
func legacyConfigPath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "link-chat", "config.json"), nil
}

// SetDevice records this client's instance name for the login handshake.
func (a *App) SetDevice(name string) {
	if name == "" {
		name = DefaultInstance
	}
	a.device = name
}

// UIState holds view-only state that does not belong in AppState.
type UIState struct {
	Dialog   views.DialogKind
	Login    views.LoginModel
	Register views.RegisterModel
	Recover  views.RecoverModel
	// NewPage is the field of the "new direct message" / "new channel" page. One
	// draft for both: moving between the two buttons starts the field afresh.
	NewPage views.NewPageModel
	Logout  views.LogoutModel
	// Accounts is the account list on the sign-in stage.
	Accounts views.AccountsModel
	// Page is what the right-hand side shows (see pages.go).
	Page client.Page
	// SettingsNav is where the user is in the settings page; it is kept when the
	// page is left, so coming back returns to the same category.
	SettingsNav views.SettingsNav
	// HelpScroll is how far the help page is scrolled.
	HelpScroll int
	// Moments is the moments and profile pages (see moments.go).
	Moments momentsState
	// Friends and DMPick are the friends page and the new-DM picker
	// (see friends.go).
	Friends friendsState
	DMPick  dmPickState
	// Me is the "me" page (see me.go).
	Me meState
	// pending is a conversation waiting for the cursor to rest on it.
	pending *pendingSettle
	// landed is set once the first conversation list after signing in has put
	// the user on the list; it is cleared when signing in again.
	landed bool
	// SessionReplaced holds the notice shown when another login displaced this
	// one.
	SessionReplaced views.SessionReplacedModel
	Scroll          int // lines scrolled up from the bottom
	Selected        int // conversation list cursor
	Quitting        bool
	// Wheel is the quick wheel (wheel.go).
	Wheel wheelState
	// SettingsDrawn is where the settings page last drew its item and preview;
	// ChoiceFan is the candidate fan opened from it (settings_fan.go).
	SettingsDrawn views.SettingsDrawn
	ChoiceFan     choiceFan
	// NeedResume marks that the active conversation's history still has to be
	// fetched after a reconnect.
	NeedResume bool
}

// DefaultServer is where the client looks for a server when nothing else says
// otherwise: the machine it is running on.
//
// The address lives in configuration rather than in the login form
// (design.md decision 6): almost everyone talks to one server, and asking for it
// on every sign-in is a field that exists only to be typed over.
const DefaultServer = "ws://127.0.0.1:8100/ws"

// LoadConfig reads the client config, creating a default if absent.
//
// When a per-instance file is missing, a credentials file from before instances
// existed is adopted instead: upgrading should not force everyone to sign in
// again.
func LoadConfig(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return Config{}, err
		}
		// First run for this instance: inherit the legacy config once.
		if legacy, lerr := legacyConfigPath(); lerr == nil && legacy != path {
			if lb, rerr := os.ReadFile(legacy); rerr == nil {
				var c Config
				if json.Unmarshal(lb, &c) == nil {
					c.normalise()
					// Persist under the new name so the adoption happens once.
					_ = SaveConfig(path, c)
					return c, nil
				}
			}
		}
		return freshConfig(), nil
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return Config{}, err
	}
	c.normalise()
	return c, nil
}

// freshConfig is the first-run configuration.
func freshConfig() Config {
	c := Config{
		Server:   DefaultServer,
		Theme:    "ink",
		Language: "zh",
		Instance: DefaultInstance,
	}
	c.SetRemember(true)
	return c
}

// Remember is the effective value of RememberSession, defaulting to true when the
// key was never written.
func (c Config) Remember() bool {
	if c.RememberSession == nil {
		return true
	}
	return *c.RememberSession
}

// SetRemember writes the switch explicitly, so the choice survives a reload.
func (c *Config) SetRemember(on bool) {
	v := on
	c.RememberSession = &v
}

// normalise fills in defaults for absent fields.
//
// RememberSession defaults to true only when the key is genuinely absent, which
// is why it is a pointer in the file: an explicit "remember_session": false must
// survive a load, and a bool without presence information cannot express that.
func (c *Config) normalise() {
	if c.Server == "" {
		c.Server = DefaultServer
	}
	if c.Theme == "" {
		c.Theme = "ink"
	}
	if c.Language == "" {
		c.Language = "zh"
	}
	if c.Instance == "" {
		c.Instance = DefaultInstance
	}
	c.migrateAccountSettings()
	// Before sign-in the most recent account's settings are in effect.
	if len(c.Accounts) > 0 {
		c.useAccountSettings(c.Accounts[0])
	}
}

// SaveConfig writes the config with restrictive permissions. It no longer holds a
// session token — that lives in the OS credential store — but it still names the
// account, so the file stays owner-only.
//
// Token is blanked before marshalling rather than relied upon to be empty: a
// single caller forgetting to clear it would write a live credential to disk, and
// that is exactly the mistake this whole design exists to prevent.
func SaveConfig(path string, c Config) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	c.Token = ""
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	// Written to a temporary file and renamed into place, so a reader never sees
	// a half-written config: os.WriteFile truncates first, and a load that lands
	// in between reads an empty file.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.tmp")
	if err != nil {
		return os.WriteFile(path, b, 0o600)
	}
	name := tmp.Name()
	_, werr := tmp.Write(b)
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(name)
		return os.WriteFile(path, b, 0o600)
	}
	_ = os.Chmod(name, 0o600)
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return os.WriteFile(path, b, 0o600)
	}
	return nil
}

// persistToken stores or clears the session credential.
//
// It only exists when the user asked to be remembered; otherwise the token is
// dropped at logout and on exit, so the next launch asks for a password
// (spec account-auth / 登录凭据存储).
func (a *App) persistToken(token string) {
	a.mu.Lock()
	a.persistTokenLocked(token)
	a.mu.Unlock()
}

// persistTokenLocked is the same, for callers already holding a.mu.
func (a *App) persistTokenLocked(token string) {
	if !a.cfg.Remember() {
		// Not remembering: make sure nothing is left behind from an earlier run.
		_ = a.creds.forUser(a.st.Username).clear()
		_ = a.creds.clear()
		a.cfg.Token = ""
		return
	}
	if err := a.creds.forUser(a.st.Username).save(token); err != nil {
		// A credential store failure must not break the running session; the
		// user simply gets asked for a password next time.
		a.setStatus(i18n.CredentialSaveFailed.Get(a.lang), true)
	}
	a.cfg.Token = ""
}

// New builds an app around a screen.
//
// The config is normalised first: callers may hand in a bare struct literal, and
// an empty Instance would otherwise give the credential store no key to save
// under, silently discarding the token.
func New(scr tcell.Screen, cfg Config, cfgPath string) *App {
	cfg.normalise()

	a := &App{
		cfg:     cfg,
		cfgPath: cfgPath,
		st:      client.NewAppState(),
		scr:     scr,
		lang:    i18n.Parse(cfg.Language),
		dirty:   make(chan struct{}, 1),
		ui:      UIState{Selected: 0},
	}
	a.st.ThemeName = cfg.Theme
	a.st.MotionOff = cfg.MotionOff
	a.st.Lang = string(a.lang)
	a.st.ServerURL = cfg.Server
	a.st.Username = cfg.Username
	a.st.UserID = cfg.UserID

	// The token lives in the OS credential store, not in the config file.
	a.creds = tokenStore{backend: credentials(cfgPath), instance: cfg.Instance}
	if cfg.Username != "" {
		// A token from before accounts had their own keys belongs to whoever was
		// signed in then, which is the username in the config.
		a.creds.forUser(cfg.Username).migrateLegacy()
	}
	if cfg.Remember() {
		if tok, err := a.creds.forUser(cfg.Username).load(); err == nil {
			a.st.Token = tok
		}
	}
	// A token left in an older config file is migrated once, then dropped from
	// the file so the plaintext copy does not linger.
	if cfg.Token != "" {
		if a.st.Token == "" && cfg.Remember() {
			if err := a.creds.forUser(cfg.Username).save(cfg.Token); err == nil {
				a.st.Token = cfg.Token
			}
		}
		// Drop the plaintext copy from the config file either way: it has either
		// moved into the credential store or been discarded.
		cfg.Token = ""
		a.cfg.Token = ""
		_ = SaveConfig(cfgPath, a.cfg)
	}

	a.applyTheme()

	// The focus marker starts resting on the initially focused region, so the
	// first frame does not slide in from nowhere.
	a.anim.slide = anim.NewSlide(a.regionX(a.st.Focus))
	a.anim.shimmerStart = nowTime()
	a.anim.stageStart = nowTime()

	a.ui.Login = views.LoginModel{Username: cfg.Username, ServerShown: cfg.Server}
	// The sign-in panel is always the first screen. When a saved session exists it
	// is offered as a one-key login rather than taken automatically: resuming
	// displaces the account's session elsewhere, and that should be something the
	// user chose, not a side effect of opening the program (design.md decision 2).
	a.ui.Dialog = views.DialogLogin
	a.ui.Login.Username = a.st.Username
	if a.st.Token != "" {
		a.ui.Login.Remembered = true
		a.ui.Login.Focus = views.LoginFocusSubmit
	} else if a.ui.Login.Username != "" {
		a.ui.Login.Focus = views.LoginFocusPassword
	}
	a.anim.buttonStart = nowTime()
	return a
}

// applyTheme rebuilds tokens from the current theme name and terminal
// capabilities. Capability detection happens once, here
// (spec tui-design-system / 终端能力降级).
func (a *App) applyTheme() {
	p, ok := theme.Resolve(a.st.ThemeName)
	if !ok {
		a.setStatus(i18n.SetUnknown.Get(a.lang), true)
	}
	cap := theme.ColorTrue
	if a.scr != nil && a.scr.Colors() < 1<<24 {
		if a.scr.Colors() <= 256 {
			cap = theme.Color256
		}
		if a.scr.Colors() <= 8 {
			cap = theme.ColorMono
		}
	}
	unicodeOK := detectUnicode(a.scr)
	a.tokens = theme.NewTokens(p, cap, unicodeOK)
}

// detectUnicode decides whether box-drawing characters are safe. UTF-8 locales
// and Windows Terminal both handle them; a plain C locale does not.
func detectUnicode(scr tcell.Screen) bool {
	if scr == nil {
		return true
	}
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	// tcell exposes encoding support indirectly; a UTF-8 run of box chars is the
	// practical check.
	return true
}

func (a *App) setStatus(msg string, isErr bool) {
	// Any new message replaces a connection notice, so it is no longer one.
	a.connStatus = false
	a.st.StatusMsg = msg
	a.st.StatusErr = isErr
	// A passing notice ("saved") goes by itself after a moment; an error stays
	// until something replaces it, so it is not missed.
	a.statusUntil = time.Time{}
	if msg != "" && !isErr {
		a.statusUntil = nowTime().Add(statusLife)
	}
}

// statusLife is how long a passing notice stays in the status bar.
const statusLife = 3 * time.Second

// expireStatus clears a passing notice once its time is up. The main loop's
// ticker repaints often enough for it to go on time.
func (a *App) expireStatus(now time.Time) {
	if !a.statusUntil.IsZero() && now.After(a.statusUntil) && !a.connStatus {
		a.st.StatusMsg, a.st.StatusErr = "", false
		a.statusUntil = time.Time{}
	}
}

// Run starts networking and the input loop, and blocks until quit.
func (a *App) Run(ctx context.Context) error {
	a.runCtx = ctx
	// Nothing connects at startup. The sign-in panel is showing, and anything that
	// signs in — including resuming a saved session — waits for the user to press
	// the button, because resuming displaces the account's session elsewhere.

	// Repaint on a timer as well as on input: a reconnect countdown and a
	// motion toggle both need the screen to advance without a keystroke.
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	evCh := make(chan tcell.Event, 32)
	go func() {
		for {
			ev := a.scr.PollEvent()
			if ev == nil {
				return
			}
			select {
			case evCh <- ev:
			case <-ctx.Done():
				return
			}
		}
	}()

	for {
		// Network goroutines change state under a.mu; drawing under it too means
		// a frame never reads a list half-way through being replaced.
		a.mu.Lock()
		a.expireStatus(nowTime())
		a.reportViewedChannel()
		quit := a.render()
		a.mu.Unlock()
		if quit {
			// render returns true when the user quit.
			return nil
		}
		if a.onFrame != nil {
			a.onFrame()
		}

		// The animation clock exists only while something is animating. At rest
		// frame is a nil channel, which blocks forever in the select below, so a
		// still screen has no timer, no wakeups and no repainting. That is the
		// whole animation budget (design.md decision 4).
		// A conversation the cursor is resting on is settled here, and the loop
		// wakes for the next one that falls due.
		a.mu.Lock()
		a.settlePending(nowTime())
		a.mu.Unlock()
		var frame <-chan time.Time
		if d, ok := a.frameDelay(nowTime()); ok {
			frame = time.After(d)
		}
		if d, ok := a.pendingIn(nowTime()); ok && (frame == nil || d < frameInterval) {
			frame = time.After(d)
		}

		select {
		case <-ctx.Done():
			return nil
		case ev := <-evCh:
			a.handleEvent(ctx, ev)
		case <-ticker.C:
			a.tick()
		case <-frame:
			// Nothing to do: waking is the point, the loop redraws at the top.
		case <-a.dirty:
		}
	}
}

// reportViewedChannel reflects the visible page, independently of subscriptions.
func (a *App) reportViewedChannel() {
	if a.tr == nil {
		return
	}
	id := ""
	w, h := a.scr.Size()
	chatVisible := w >= 10 && h >= 6 && !a.onStage() &&
		(views.ContentRect(w, h).W >= layout.MinViewWidth || a.st.Focus != client.FocusList)
	if a.st.State == client.StateReady && chatVisible && a.page() == client.PageConversation {
		if cv := a.st.Active(); cv != nil && cv.Kind == protocol.KindChannel {
			if a.ui.pending != nil {
				return
			}
			id = cv.ID
		}
	}
	a.tr.SetViewedChannel(id)
}

// tick advances time-dependent UI: the reconnect countdown.
func (a *App) tick() {
	// A sign-in countdown ends on its own; the panel must open up again without
	// a keystroke (spec tui-chat-client / 倒计时结束后恢复).
	if a.ui.Login.RetryAt > 0 && a.ui.Login.RetryAt <= nowTime().UnixMilli() {
		a.ui.Login.RetryAt = 0
		a.ui.Login.Err = ""
		a.markDirty()
	}
	// A mute ends on its own at its expiry; nothing is pushed for that, so the
	// composer unlocks here (design.md decision 11).
	if a.st.MutedUntil > 0 && !a.st.MutedForever && a.st.MutedUntil <= nowTime().UnixMilli() {
		a.st.MutedUntil = 0
	}
	a.tickMoments()
	if a.st.State == client.StateDisconnected && a.st.RetryIn > 0 {
		a.st.RetryIn -= 250 * time.Millisecond
		if a.st.RetryIn <= 0 {
			a.st.RetryIn = 0
			a.tryReconnect()
		}
	}
}

// connect dials and, when a token exists, resumes the session.
//
// Used by the startup path and by reconnection: in both cases the outcome of
// authentication is decided here, because there is no form the user is filling
// in that a failure could disrupt.
func (a *App) connect(ctx context.Context) {
	a.connectToResume(ctx)
}

// connectToResume dials and resumes a session, falling back to the login form
// when there is no token.
func (a *App) connectToResume(ctx context.Context) {
	a.dial(ctx, true)
}

// connectForLogin dials with the intent to send a login or register request
// immediately afterwards.
//
// It must not decide what an unauthenticated connection means: the login form is
// already open and the caller is about to submit it. Treating "no token yet" as
// a failure here is what makes a first sign-in impossible.
func (a *App) connectForLogin(ctx context.Context) {
	a.dial(ctx, false)
}

// dial opens the transport. resumeAfter says whether a stored token should be
// used to re-authenticate, and whether a missing token counts as failure.
func (a *App) dial(ctx context.Context, resumeAfter bool) {
	// A fresh attempt gets a fresh verdict: the server may have lowered its
	// minimum since the last refusal.
	a.st.Outdated = nil
	tr := client.NewTransport(a.st.ServerURL)
	tr.OnEvent = a.onEvent
	tr.OnStateChange = func(s client.ConnState, err error) {
		// A transport that has been replaced must not overwrite the state of
		// the one that replaced it.
		if a.tr != tr {
			return
		}
		a.onStateChange(s, err)
	}
	a.tr = tr

	go func() {
		err := tr.Connect(ctx)
		a.markDirty()
		if err != nil {
			a.onConnectFailed(err)
			return
		}
		tr.ResetBackoff()
		if !resumeAfter {
			// Leave the connection in the authenticating state; the caller's
			// login request completes the transition.
			return
		}
		if a.st.Token != "" {
			a.resume(ctx)
			return
		}
		a.mu.Lock()
		a.st.State = client.StateAuthFailed
		a.mu.Unlock()
		a.openLogin()
		a.markDirty()
	}()
}

func (a *App) onConnectFailed(err error) {
	var oe *client.OutdatedError
	if errors.As(err, &oe) {
		a.onOutdated(oe)
		return
	}
	a.st.State = client.StateDisconnected
	a.st.LastError = err.Error()
	a.st.RetryIn = a.tr.NextBackoff()
	a.markDirty()
}

// onOutdated handles a server that refuses this client's version. Unlike an
// unreachable server, retrying cannot help, so reconnection stops and the user
// is sent to the login panel with what to upgrade to. The saved token is kept:
// once upgraded, the user should be able to resume without a password
// (spec tui-chat-client / 客户端版本过旧时的界面).
func (a *App) onOutdated(oe *client.OutdatedError) {
	a.mu.Lock()
	a.st.Outdated = oe
	a.st.State = client.StateAuthFailed
	a.st.RetryIn = 0
	msg := a.outdatedMessage()
	a.st.LastError = msg
	if !a.onStage() {
		a.openLogin()
	}
	a.ui.Login.Busy = false
	a.ui.Login.Err = msg
	a.mu.Unlock()
	a.markDirty()
}

// outdatedMessage is the panel text for a refused version.
func (a *App) outdatedMessage() string {
	oe := a.st.Outdated
	if oe == nil {
		return ""
	}
	return fmt.Sprintf(i18n.ClientOutdated.Get(a.lang), oe.Current, oe.Min)
}

// applyMuteLocked locks the composer until the mute ends. The caller holds a.mu.
func (a *App) applyMuteLocked(info protocol.SanctionInfo) {
	a.st.MutedForever = info.ExpiresAt == 0
	a.st.MutedUntil = info.ExpiresAt
	a.st.InputErr = ""
}

// mutedText is the composer's explanation while muted, or "".
func (a *App) mutedText() string {
	if !a.st.Muted(nowTime().UnixMilli()) {
		return ""
	}
	if a.st.MutedForever {
		return i18n.MutedForever.Get(a.lang)
	}
	return fmt.Sprintf(i18n.MutedUntil.Get(a.lang), sanctionDate(a.st.MutedUntil))
}

// sanctionDate renders a sanction's end for the panel and composer: the local
// date and time, or "permanent".
func sanctionDate(ms int64) string {
	if ms == 0 {
		return ""
	}
	return time.UnixMilli(ms).Local().Format("2006-01-02 15:04")
}

// bannedMessage is the login panel text for a ban.
func (a *App) bannedMessage(info protocol.SanctionInfo) string {
	until := i18n.Forever.Get(a.lang)
	if info.ExpiresAt != 0 {
		until = sanctionDate(info.ExpiresAt)
	}
	reason := info.Reason
	if reason == "" {
		reason = "—"
	}
	return fmt.Sprintf(i18n.AccountBanned.Get(a.lang), reason, until)
}

// handleBanned reacts to the account being banned while signed in: stop
// reconnecting, drop the saved login (the server already revoked it), and go
// back to the sign-in stage with the reason and when it ends
// (spec tui-chat-client / 被封禁时的界面).
func (a *App) handleBanned(info protocol.SanctionInfo) {
	// Drop the dead connection before the panel appears: a sign-in submitted
	// from the panel would otherwise reuse it and wait out the dial timeout.
	if a.tr != nil {
		a.tr.Close()
		a.tr = nil
	}
	a.mu.Lock()
	a.st.State = client.StateAuthFailed
	a.st.RetryIn = 0
	a.st.Token = ""
	a.persistTokenLocked("")
	msg := a.bannedMessage(info)
	a.st.LastError = msg
	a.st.MutedUntil, a.st.MutedForever = 0, false
	a.openLogin()
	a.ui.Login.Remembered = false
	a.ui.Login.Err = msg
	a.mu.Unlock()

	_ = SaveConfig(a.cfgPath, a.cfg)
	a.markDirty()
}

// resume re-authenticates with a stored token.
func (a *App) resume(ctx context.Context) {
	go func() {
		f, err := a.tr.Call(ctx, protocol.OpAuthResume, protocol.ResumeReq{
			Token: a.st.Token, Device: a.device,
		}, 15*time.Second)
		if err != nil {
			a.onConnectFailed(err)
			return
		}
		if f.Type == protocol.TypeError {
			code := errorCode(f)
			if code == protocol.CodeTokenExpired || code == protocol.CodeUnauthorized {
				// Retrying with a dead token would loop forever; ask the user.
				a.st.Token = ""
				a.persistToken("")
				_ = SaveConfig(a.cfgPath, a.cfg)
				a.st.State = client.StateAuthFailed
				a.openLogin()
				a.markDirty()
				return
			}
			if code == protocol.CodeAccountBanned {
				var info protocol.SanctionInfo
				_ = decodeData(f, &info)
				a.handleBanned(info)
				return
			}
			a.onConnectFailed(errors.New(protocol.DefaultMessage(code)))
			return
		}

		// The server rotates the token on every resume, so the stored credential
		// must be replaced. Keeping the old one would leave us holding a token the
		// server has already revoked, and the next launch would demand a password.
		var res protocol.AuthRes
		if err := decodeData(f, &res); err == nil && res.Token != "" {
			a.mu.Lock()
			a.st.Token = res.Token
			a.persistTokenLocked(res.Token)
			a.mu.Unlock()
		}

		a.onAuthenticated(protocol.AuthRes{
			User:   protocol.User{ID: a.st.UserID, Username: a.st.Username},
			Device: a.device,
		})
	}()
}

// onAuthenticated loads the conversation list and marks the client ready.
//
// It is called from the goroutine that finished signing in, while the main loop is
// drawing, so it takes the lock: the sign-in panel coming down and the entrance
// starting must happen together, never half seen by a frame in between.
func (a *App) onAuthenticated(res protocol.AuthRes) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.st.State = client.StateReady
	// A notice about the connection that just ended ("server restarting") is
	// stale once a new one is up. Other messages are not this one's to clear.
	if a.connStatus {
		a.setStatus("", false)
		a.connStatus = false
	}
	a.st.UserID = res.User.ID
	if res.User.Username != "" {
		a.st.Username = res.User.Username
	}
	a.cfg.UserID = a.st.UserID
	a.cfg.Username = a.st.Username
	a.cfg.rememberAccount(a.st.Username)
	_ = SaveConfig(a.cfgPath, a.cfg)

	// Signing in from the stage plays the entrance; it must be begun while the
	// panel is still up, because it starts from where the panel was. A reconnect
	// is not on the stage, so it plays nothing.
	a.beginEntrance()
	// A sign-in from the panel lands on the list once its conversations arrive. A
	// reconnect keeps whatever the user was doing.
	if a.ui.Dialog != views.DialogNone {
		a.ui.landed = false
	}
	a.ui.Dialog = views.DialogNone
	a.markDirty()

	go a.refreshConversations()
	go a.refreshUsers()
	go a.syncSettingsAfterSignIn()
	a.loadSocialAfterSignIn()
}

func (a *App) refreshConversations() {
	if a.tr == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	f, err := a.tr.Call(ctx, protocol.OpChannelList, nil, 15*time.Second)
	if err != nil || f.Type != protocol.TypeRes {
		return
	}
	var res protocol.ChannelListRes
	if err := decodeData(f, &res); err != nil {
		return
	}
	a.mu.Lock()
	a.st.SetConversations(res.Conversations)
	// A refresh can drop the open conversation (a channel deleted while this client
	// was offline); the keyboard must not be left in a composer with nothing behind
	// it.
	a.reconcileFocus()
	a.landOnList()
	a.mu.Unlock()
	a.markDirty()
}

// landOnList is the rule for where a freshly loaded conversation list leaves the
// user. Nothing is opened for them: with no open conversation the keyboard is on
// the list, the list is open and the first selectable row is under the cursor. A
// reconnect, which already has a conversation open, is left as it is. Caller holds
// a.mu.
func (a *App) landOnList() {
	// Only the first list after signing in lands: a later refresh (a reconnect, a
	// list re-fetched) must not pull the keyboard back to the list or throw away
	// the page the user is looking at.
	if a.ui.landed || a.st.ActiveID != "" {
		a.ui.landed = true
		return
	}
	a.ui.landed = true
	a.st.Focus = client.FocusList
	a.clampSelection(a.st.SidebarRows())
	// The page stays the welcome page: nothing is shown until the user moves the
	// cursor, which is what choosing means (spec tui-chat-client /
	// 登录后列表展开且不自动打开会话).
	a.ui.Page = client.PageWelcome
	a.syncFold()
	a.syncMarker()
}

func (a *App) refreshUsers() {
	if a.tr == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	f, err := a.tr.Call(ctx, protocol.OpUserList, nil, 15*time.Second)
	if err != nil || f.Type != protocol.TypeRes {
		return
	}
	var res protocol.UserListRes
	if err := decodeData(f, &res); err != nil {
		return
	}
	a.mu.Lock()
	for _, u := range res.Users {
		a.st.Users[u.ID] = u
	}
	a.mu.Unlock()
	a.markDirty()
}

// openConversation switches the active conversation and loads its history.
func (a *App) openConversation(id string) {
	// Whatever opens a conversation — a new channel, a direct message, a test —
	// puts it on the right and the list's cursor on its row.
	a.ui.Page = client.PageConversation
	a.ui.pending = nil
	if i := client.IndexOfConversation(a.st.SidebarRows(), id); i >= 0 {
		a.ui.Selected = i
	}
	if a.st.ActiveID == id {
		return
	}
	a.st.StashDraft()
	a.st.ActiveID = id
	a.st.LoadDraft(id)
	a.ui.Scroll = 0
	a.st.MarkRead(id)
	a.ui.NeedResume = true
	// Opening can be what makes folding possible, so the fold is re-aimed.
	a.syncFold()
	a.syncMarker()
	if a.tr != nil {
		a.joinAndLoad(id)
	}
}

// joinAndLoad makes the server aware this client is watching the conversation,
// then fetches its history.
//
// Both steps are needed and they are not interchangeable: history alone does not
// put the connection on the fan-out list, so opening a channel without joining
// leaves a client reading a channel it will never hear from
// (spec chat-rooms / 频道加入与退出).
func (a *App) joinAndLoad(id string) {
	if a.onLoad != nil {
		a.onLoad(id)
	}
	tr := a.tr
	if tr == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		cv := a.st.Conversations[id]
		if cv != nil && cv.Kind == protocol.KindChannel {
			// Joining an already-joined channel is idempotent and silent on the
			// server, so this is safe to call every time.
			if reply, err := tr.Call(ctx, protocol.OpChannelJoin, protocol.ConversationRef{
				ConversationID: id,
			}, 15*time.Second); err == nil && reply.Type == protocol.TypeRes {
				tr.ChannelJoined(id)
			}
		}
		a.fetchHistory(ctx, id, 0)
	}()
}

// loadHistory pages older messages in; the connection is already subscribed.
func (a *App) loadHistory(id string, beforeSeq int64) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		a.fetchHistory(ctx, id, beforeSeq)
	}()
}

// fetchHistory performs one history request and merges the result.
func (a *App) fetchHistory(ctx context.Context, id string, beforeSeq int64) {
	if a.tr == nil {
		return
	}
	f, err := a.tr.Call(ctx, protocol.OpMessageHistory, protocol.HistoryReq{
		ConversationID: id, BeforeSeq: beforeSeq, Limit: 50,
	}, 15*time.Second)
	if err != nil || f.Type != protocol.TypeRes {
		return
	}
	var res protocol.HistoryRes
	if err := decodeData(f, &res); err != nil {
		return
	}
	a.mu.Lock()
	a.st.SetHistory(id, res.Messages, res.HasMore, beforeSeq > 0)
	a.mu.Unlock()
	a.markDirty()
}

// onStateChange reacts to transport state transitions.
func (a *App) onStateChange(s client.ConnState, err error) {
	a.mu.Lock()
	a.st.State = s
	switch s {
	case client.StateDisconnected:
		a.st.RetryIn = a.tr.NextBackoff()
	case client.StateReady:
		a.st.RetryIn = 0
	}
	if err != nil {
		a.st.LastError = err.Error()
	}
	a.mu.Unlock()
	a.markDirty()
}

// onEvent applies a server push.
func (a *App) onEvent(name string, data json.RawMessage) {
	switch name {
	case protocol.EvMessage, protocol.EvMessageEdit, protocol.EvMessageDel:
		var me protocol.MessageEvent
		if err := json.Unmarshal(data, &me); err != nil {
			return
		}
		a.mu.Lock()
		active := a.st.ActiveID == me.Message.ConversationID
		// A message for a conversation this client has never listed (the first
		// direct message someone sends) needs the list fetched to name it.
		_, known := a.st.Conversations[me.Message.ConversationID]
		isNew := a.st.InsertMessage(me.Message, active)
		if !known && name == protocol.EvMessage {
			go a.refreshConversations()
		}
		if isNew && active {
			// Only offer the jump hint when the viewport is scrolled up.
			if a.ui.Scroll > 0 {
				a.st.PendingNewMessage = true
			}
		}
		a.mu.Unlock()
		a.markDirty()
	case protocol.EvPresence:
		var pe protocol.PresenceEvent
		if err := json.Unmarshal(data, &pe); err != nil {
			return
		}
		a.mu.Lock()
		a.st.SetPresence(pe)
		a.mu.Unlock()
		a.markDirty()
	case protocol.EvChannelGone:
		var ce protocol.ChannelGoneEvent
		if err := json.Unmarshal(data, &ce); err != nil {
			return
		}
		a.mu.Lock()
		a.st.RemoveConversation(ce.ConversationID)
		// If that was the open conversation, the keyboard must leave the composer.
		a.reconcileFocus()
		a.mu.Unlock()
		a.markDirty()
	case protocol.EvShutdown:
		a.mu.Lock()
		a.setStatus(i18n.ErrText("server_shutting_down", "").Get(a.lang), true)
		a.connStatus = true
		a.mu.Unlock()
		a.markDirty()
	case protocol.EvAccountBanned:
		var info protocol.SanctionInfo
		_ = json.Unmarshal(data, &info)
		a.handleBanned(info)
	case protocol.EvAccountMuted:
		var info protocol.SanctionInfo
		_ = json.Unmarshal(data, &info)
		a.mu.Lock()
		a.applyMuteLocked(info)
		a.mu.Unlock()
		a.markDirty()
	case protocol.EvAccountUnmuted:
		a.mu.Lock()
		a.st.MutedUntil, a.st.MutedForever = 0, false
		a.mu.Unlock()
		a.markDirty()
	case protocol.EvSessionReplaced:
		var se protocol.SessionReplacedEvent
		if err := json.Unmarshal(data, &se); err != nil {
			return
		}
		a.handleSessionReplaced(se)
	case protocol.EvSettingsUpdated:
		a.onSettingsUpdated(data)
	default:
		a.onSocialEvent(name, data)
	}
}

// handleSessionReplaced reacts to losing the account to a newer login.
//
// This is deliberately not a "disconnected" state: reconnecting would fight the
// new session forever, so the token is dropped and the user is shown why. The
// spec calls this out explicitly (spec login-session / 被顶掉一端的后续行为).
func (a *App) handleSessionReplaced(ev protocol.SessionReplacedEvent) {
	a.mu.Lock()
	// Stop the reconnect machinery before it can fire.
	a.st.State = client.StateAuthFailed
	a.st.RetryIn = 0
	a.st.Token = ""
	a.persistTokenLocked("")
	a.st.LastError = i18n.SessionReplaced.Get(a.lang)
	a.ui.SessionReplaced = views.SessionReplacedModel{
		Username: ev.Username,
		Location: ev.Location,
		At:       ev.At,
		Device:   ev.Device,
	}
	a.ui.Dialog = views.DialogSessionReplaced
	a.mu.Unlock()

	// Persist the cleared token so a restart does not try to resume with it.
	_ = SaveConfig(a.cfgPath, a.cfg)
	if a.tr != nil {
		a.tr.Close()
		a.tr = nil
	}
	a.markDirty()
}

func (a *App) markDirty() {
	select {
	case a.dirty <- struct{}{}:
	default:
	}
}

// tryReconnect re-dials after a backoff.
//
// The dial runs on its own goroutine and the connection it opens lives on, so
// its context must outlive this call: cancelling here on return aborted every
// reconnect before it reached the server. It hangs off Run's context instead.
func (a *App) tryReconnect() {
	if a.st.ServerURL == "" {
		return
	}
	ctx := a.runCtx
	if ctx == nil {
		ctx = context.Background()
	}
	a.connect(ctx)
}

func errorCode(f protocol.Frame) string {
	var p protocol.ResPayload
	_ = json.Unmarshal(f.Payload, &p)
	return p.Code
}

func decodeData(f protocol.Frame, out any) error {
	var p protocol.ResPayload
	if err := json.Unmarshal(f.Payload, &p); err != nil {
		return err
	}
	if len(p.Data) == 0 {
		return nil
	}
	return json.Unmarshal(p.Data, out)
}

// openLogin raises the sign-in panel, pre-filling only what the user has not
// already entered. Wiping a half-filled form because a background reconnect
// happened is exactly the kind of thing that makes a client feel hostile.
func (a *App) openLogin() {
	a.ui.Dialog = views.DialogLogin
	if strings.TrimSpace(a.ui.Login.Username) == "" {
		a.ui.Login.Username = a.st.Username
	}
	a.ui.Login.Password = ""
	a.ui.Login.Focus = views.LoginFocusPassword
	if strings.TrimSpace(a.ui.Login.Username) == "" {
		a.ui.Login.Focus = views.LoginFocusUsername
	}
	a.ui.Login.ServerShown = a.cfg.Server
	a.ui.Login.Err = ""
	a.ui.Login.Busy = false
}

var _ = fmt.Sprintf
var _ = layout.GutterX
var _ = widget.Rect{}

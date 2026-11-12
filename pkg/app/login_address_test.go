package app

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/tui/views"
)

// A server that cannot be reached is reported with its address on the error line,
// the only place the panel shows the address now.
func TestUnreachableServerNamesTheAddress(t *testing.T) {
	const addr = "ws://127.0.0.1:1/ws" // nothing listens on port 1
	scr := newTestScreen(t, 100, 30)
	a := New(scr, Config{Server: addr, Theme: "ink", Language: "zh"}, filepath.Join(t.TempDir(), "u.json"))

	a.mu.Lock()
	a.ui.Login.Username = "nobody"
	a.ui.Login.Password = "whatever"
	a.ui.Login.Focus = views.LoginFocusSubmit
	a.mu.Unlock()
	a.submitLogin(t.Context())

	waitFor(t, 20*time.Second, "the login to fail", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return !a.ui.Login.Busy && a.ui.Login.Err != ""
	})
	a.mu.Lock()
	defer a.mu.Unlock()
	if !strings.Contains(a.ui.Login.Err, addr) {
		t.Fatalf("the error %q does not name the address %s", a.ui.Login.Err, addr)
	}
	a.render()
	if !strings.Contains(screenText(scr.(tcell.SimulationScreen)), addr) {
		t.Fatal("the address is not on screen with the error")
	}
}

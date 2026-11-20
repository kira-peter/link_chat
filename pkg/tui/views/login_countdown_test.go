package views

import (
	"strings"
	"testing"

	"link-chat/pkg/i18n"
)

// The sign-in panel says how long is left while a backoff is counting down,
// and takes the place of the plain error line.
func TestLoginPanelShowsRetryCountdown(t *testing.T) {
	now := int64(1_800_000_000_000)
	m := LoginModel{Username: "dana", RetryAt: now + 4*60_000 + 12_000, Now: now}
	out := screenString(renderLogin(t, distinctToks(), m, i18n.ZH))
	if !strings.Contains(out, "等待 4 分 12 秒 后可重试") {
		t.Fatalf("countdown missing from the panel:\n%s", out)
	}

	// Past the deadline the line goes away and the panel is ordinary again.
	m.Now = m.RetryAt + 1
	out = screenString(renderLogin(t, distinctToks(), m, i18n.ZH))
	if strings.Contains(out, "等待") {
		t.Fatalf("countdown still drawn after it ended:\n%s", out)
	}
}

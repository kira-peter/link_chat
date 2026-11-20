package i18n

import (
	"testing"
	"time"
)

func TestWaitDuration(t *testing.T) {
	cases := []struct {
		d  time.Duration
		zh string
		en string
	}{
		{45 * time.Second, "45 秒", "45s"},
		{60 * time.Second, "1 分 0 秒", "1m 0s"},
		{4*time.Minute + 12*time.Second, "4 分 12 秒", "4m 12s"},
		{time.Hour, "1 小时 0 分", "1h 0m"},
		{3*time.Hour + 5*time.Minute, "3 小时 5 分", "3h 5m"},
		{24 * time.Hour, "24 小时 0 分", "24h 0m"},
		{-1, "0 秒", "0s"},
	}
	for _, c := range cases {
		if got := WaitDuration(c.d, ZH); got != c.zh {
			t.Errorf("ZH %v = %q, want %q", c.d, got, c.zh)
		}
		if got := WaitDuration(c.d, EN); got != c.en {
			t.Errorf("EN %v = %q, want %q", c.d, got, c.en)
		}
	}
}

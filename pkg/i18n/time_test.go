package i18n

import (
	"testing"
	"time"
)

func at(h, m int) int64 {
	return time.Date(2026, 11, 12, h, m, 0, 0, time.Local).UnixMilli()
}

func TestClockBothFormats(t *testing.T) {
	for _, c := range []struct {
		h, m        int
		h24, zh, en string
	}{
		{0, 5, "00:05", "上午 12:05", "12:05 AM"},
		{9, 30, "09:30", "上午 9:30", "9:30 AM"},
		{11, 59, "11:59", "上午 11:59", "11:59 AM"},
		{12, 0, "12:00", "下午 12:00", "12:00 PM"},
		{20, 26, "20:26", "下午 8:26", "8:26 PM"},
		{23, 59, "23:59", "下午 11:59", "11:59 PM"},
	} {
		ms := at(c.h, c.m)
		if got := Clock(ms, true, ZH); got != c.h24 {
			t.Errorf("%02d:%02d 24h = %q, want %q", c.h, c.m, got, c.h24)
		}
		if got := Clock(ms, false, ZH); got != c.zh {
			t.Errorf("%02d:%02d 12h zh = %q, want %q", c.h, c.m, got, c.zh)
		}
		if got := Clock(ms, false, EN); got != c.en {
			t.Errorf("%02d:%02d 12h en = %q, want %q", c.h, c.m, got, c.en)
		}
	}
}

// The 24-hour clock is exactly what the client always showed.
func TestClock24MatchesTheOldClock(t *testing.T) {
	for _, ms := range []int64{at(0, 0), at(7, 3), at(13, 45), at(23, 59)} {
		if got, want := Clock(ms, true, ZH), ClockOnly(ms); got != want {
			t.Errorf("Clock24 %q differs from the old clock %q", got, want)
		}
	}
}

func TestDateClockCarriesTheDate(t *testing.T) {
	ms := at(20, 26)
	if got := DateClock(ms, true, ZH); got != FullTime(ms) {
		t.Errorf("24h date line %q differs from the old one %q", got, FullTime(ms))
	}
	if got, want := DateClock(ms, false, EN), "2026-11-12 8:26 PM"; got != want {
		t.Errorf("12h date line %q, want %q", got, want)
	}
}

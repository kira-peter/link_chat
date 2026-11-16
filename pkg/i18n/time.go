package i18n

import (
	"fmt"
	"time"
)

// civilTime holds the local wall-clock fields of a timestamp. Splitting this out
// keeps i18n.go free of time-zone logic.
type civilTime struct {
	Year, Month, Day, Hour, Minute int
}

// timeFromMillis converts epoch milliseconds to local wall-clock fields.
func timeFromMillis(ms int64) civilTime {
	t := time.UnixMilli(ms)
	return civilTime{
		Year:   t.Year(),
		Month:  int(t.Month()),
		Day:    t.Day(),
		Hour:   t.Hour(),
		Minute: t.Minute(),
	}
}

// Clock formats a time of day, on a 24-hour or a 12-hour clock. The 12-hour form
// carries its half of the day the way each language writes it: before the time in
// Chinese ("下午 8:26"), after it in English ("8:26 PM").
func Clock(ms int64, clock24 bool, lang Lang) string {
	t := timeFromMillis(ms)
	if clock24 {
		return fmt.Sprintf("%02d:%02d", t.Hour, t.Minute)
	}
	h := t.Hour % 12
	if h == 0 {
		h = 12
	}
	pm := t.Hour >= 12
	if lang == EN {
		half := "AM"
		if pm {
			half = "PM"
		}
		return fmt.Sprintf("%d:%02d %s", h, t.Minute, half)
	}
	half := "上午"
	if pm {
		half = "下午"
	}
	return fmt.Sprintf("%s %d:%02d", half, h, t.Minute)
}

// DateClock is a date with a time of day, for a time line that is not today.
func DateClock(ms int64, clock24 bool, lang Lang) string {
	if ms == 0 {
		return ""
	}
	t := timeFromMillis(ms)
	return fmt.Sprintf("%04d-%02d-%02d %s", t.Year, t.Month, t.Day, Clock(ms, clock24, lang))
}

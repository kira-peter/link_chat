package i18n

import "time"

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

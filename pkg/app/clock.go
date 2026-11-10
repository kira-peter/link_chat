package app

import "time"

// nowMillis is the clock used for relative timestamps, injectable in tests.
var nowMillis = func() int64 { return time.Now().UnixMilli() }

// nowTime is the clock used by animation. It is a variable so a test can freeze
// a moment and assert exactly what is on screen, instead of racing a real timer.
var nowTime = time.Now

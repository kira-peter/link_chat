package anim

import (
	"testing"
	"time"
)

// The phases come in order, each runs its local progress from 0 to 1, and the
// whole thing is over at EntranceDuration.
func TestEntrancePhasesRunInOrder(t *testing.T) {
	last := PhaseCollapse
	lastP := -1.0
	for ms := 0; ms <= int(EntranceDuration/time.Millisecond)+50; ms += 5 {
		ph, p := EntranceAt(time.Duration(ms) * time.Millisecond)
		if ph < last {
			t.Fatalf("%dms: phase went back from %v to %v", ms, last, ph)
		}
		if ph == last && p < lastP {
			t.Fatalf("%dms: progress within phase %v went back from %.3f to %.3f", ms, ph, lastP, p)
		}
		if p < 0 || p > 1 {
			t.Fatalf("%dms: progress %.3f out of range", ms, p)
		}
		if ph != last {
			lastP = -1
		}
		last, lastP = ph, p
	}
	if last != PhaseDone {
		t.Fatalf("the entrance did not finish: ended in %v", last)
	}
}

func TestEntranceBoundaries(t *testing.T) {
	d := EntranceDuration
	for _, c := range []struct {
		at   time.Duration
		want EntrancePhase
	}{
		{0, PhaseCollapse},
		{time.Duration(float64(d) * 0.24), PhaseCollapse},
		{time.Duration(float64(d) * 0.26), PhaseStretch},
		{time.Duration(float64(d) * 0.44), PhaseStretch},
		{time.Duration(float64(d) * 0.46), PhaseOpen},
		{d - time.Millisecond, PhaseOpen},
		{d, PhaseDone},
		{10 * d, PhaseDone},
	} {
		if got, _ := EntranceAt(c.at); got != c.want {
			t.Errorf("at %v: phase %v, want %v", c.at, got, c.want)
		}
	}
	if EntranceDuration > 800*time.Millisecond {
		t.Fatalf("the entrance lasts %v, over the 800ms ceiling", EntranceDuration)
	}
}

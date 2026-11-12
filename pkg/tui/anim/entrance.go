package anim

import "time"

// The sign-in entrance: "collapse to a line, then open".
//
// When the user signs in from the stage, the panel squashes down to a single line,
// the line stretches to the full width of the screen as a bright rule, and the rule
// splits in two and opens up and down, uncovering the main interface between the
// two halves, until they come to rest as the main interface's own top and bottom
// rules (spec tui-login-stage / 登录成功的入场动画).
//
// The whole thing is one timeline read from the time since it began. Each phase
// has its own local progress, 0 at its start and 1 at its end, so a renderer only
// asks "which phase, and how far into it".

// EntranceDuration is the length of the whole entrance, under the 800ms ceiling.
const EntranceDuration = 600 * time.Millisecond

// EntrancePhase is one stage of the entrance.
type EntrancePhase int

const (
	// PhaseCollapse: the panel's height shrinks about its middle to one row.
	PhaseCollapse EntrancePhase = iota
	// PhaseStretch: that row stretches from the panel's width to the screen's.
	PhaseStretch
	// PhaseOpen: the rule splits and the two halves travel to the main
	// interface's top and bottom rules, uncovering it between them.
	PhaseOpen
	// PhaseDone: the entrance is over and draws nothing.
	PhaseDone
)

// The phases' boundaries as fractions of the whole duration.
const (
	collapseEnd = 0.25
	stretchEnd  = 0.45
)

// EntranceAt is the phase the entrance is in after elapsed, and how far into that
// phase it is, eased. Before it starts it is at the beginning of the collapse;
// from EntranceDuration on it is done.
func EntranceAt(elapsed time.Duration) (EntrancePhase, float64) {
	if elapsed >= EntranceDuration {
		return PhaseDone, 1
	}
	p := float64(elapsed) / float64(EntranceDuration)
	if p < 0 {
		p = 0
	}
	switch {
	case p < collapseEnd:
		return PhaseCollapse, EaseOutCubic(p / collapseEnd)
	case p < stretchEnd:
		return PhaseStretch, EaseOutCubic((p - collapseEnd) / (stretchEnd - collapseEnd))
	default:
		return PhaseOpen, EaseOutCubic((p - stretchEnd) / (1 - stretchEnd))
	}
}

// ExitAt is the sign-out exit after elapsed: the entrance played backwards, so it
// starts at the end of the opening and finishes at the start of the collapse, with
// the panel whole. Before it starts it is at the end of the opening; from
// EntranceDuration on it is done.
func ExitAt(elapsed time.Duration) (EntrancePhase, float64) {
	if elapsed >= EntranceDuration {
		return PhaseDone, 0
	}
	if elapsed < 0 {
		elapsed = 0
	}
	return EntranceAt(EntranceDuration - elapsed - 1)
}

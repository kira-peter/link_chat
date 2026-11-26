package main

import "testing"

func TestLiveTerminalMode(t *testing.T) {
	for _, tc := range []struct {
		name       string
		plain, tty bool
		want       bool
	}{
		{name: "interactive terminal", tty: true, want: true},
		{name: "redirected output"},
		{name: "plain flag at a terminal", plain: true, tty: true},
		{name: "plain flag with redirected output", plain: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := useLiveTerminal(tc.plain, tc.tty); got != tc.want {
				t.Fatalf("live terminal = %t, want %t", got, tc.want)
			}
		})
	}
}

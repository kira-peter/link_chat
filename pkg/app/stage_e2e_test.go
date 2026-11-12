package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// Against a real server: the stage before sign-in, the entrance straight after,
// and the main interface once it is over, with the frame clock gone quiet.
func TestSignInFromTheStageEndToEnd(t *testing.T) {
	hs := startServer(t)
	url := "ws" + strings.TrimPrefix(hs.URL, "http") + "/ws"
	scr := newTestScreen(t, 100, 30)
	a := New(scr, Config{Server: url, Theme: "ink", Language: "zh"}, filepath.Join(t.TempDir(), "e.json"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.Run(ctx)

	// On the stage: the panel alone, none of the main interface.
	a.mu.Lock()
	a.render()
	stage := screenText(scr)
	onStage := a.onStage()
	a.mu.Unlock()
	if !onStage || hasChromeRules(scr) || strings.Contains(stage, "私聊") {
		t.Fatalf("before sign-in the stage is not showing on its own:\n%s", stage)
	}

	registerAndWait(t, a, ctx, "stagey")

	// Straight after: the entrance has begun.
	a.mu.Lock()
	running := a.entranceRunning(nowTime())
	a.mu.Unlock()
	if !running {
		t.Fatal("signing in from the stage did not start the entrance")
	}

	// Once over, the main interface is whole, and nothing keeps the frame clock on
	// for the entrance or the stage.
	renderSettled(t, a)
	a.mu.Lock()
	defer a.mu.Unlock()
	if !hasChromeRules(scr) || !strings.Contains(screenText(scr), "私聊") {
		t.Fatalf("after the entrance the main interface is not whole:\n%s", screenText(scr))
	}
	if a.onStage() || a.entranceRunning(nowTime()) {
		t.Fatal("still on the stage or in the entrance after it finished")
	}
}

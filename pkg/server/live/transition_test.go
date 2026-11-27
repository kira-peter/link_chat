package live

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/i18n"
	"link-chat/pkg/server"
	"link-chat/pkg/tui/anim"
	"link-chat/pkg/tui/views"
)

type shutdownTestSource struct{}

func (shutdownTestSource) SubscribeActivity(context.Context) <-chan server.Activity { return nil }
func (shutdownTestSource) LiveSnapshot() (server.LiveSnapshot, error) {
	return server.LiveSnapshot{}, nil
}
func (shutdownTestSource) ShutdownPhase() server.ShutdownPhase       { return server.ShutdownDraining }
func (shutdownTestSource) DashboardConnections() []server.OnlineConn { return nil }
func (shutdownTestSource) DashboardChannelCounts([]string) (map[string]server.LiveChannel, error) {
	return nil, nil
}

func TestQuitKeepsDashboardUntilServerFinishes(t *testing.T) {
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	requested, finished, result := make(chan struct{}), make(chan error, 1), make(chan error, 1)
	defer close(finished)
	go func() {
		result <- Run(ctx, screen, shutdownTestSource{}, NewLogSink(), func() {
			cancel()
			close(requested)
		}, finished)
	}()
	post := func(key tcell.Key, r rune) {
		t.Helper()
		event := tcell.NewEventKey(key, r, tcell.ModNone)
		until := time.Now().Add(time.Second)
		for {
			err := screen.PostEvent(event)
			if err == nil {
				return
			}
			if err != tcell.ErrEventQFull || time.Now().After(until) {
				t.Fatal(err)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	waitForText := func(text string, want bool) {
		t.Helper()
		until := time.Now().Add(3 * time.Second)
		for time.Now().Before(until) {
			if strings.Contains(screenText(screen), text) == want {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("expected %q visibility %t", text, want)
	}
	post(tcell.KeyF1, 0)
	waitForText("展开系统日志", true)
	post(tcell.KeyEscape, 0)
	waitForText("展开系统日志", false)
	waitForText("确认关闭服务端", false)

	post(tcell.KeyF1, 0)
	waitForText("展开系统日志", true)
	post(tcell.KeyDown, 0)
	post(tcell.KeyEnter, 0)
	waitForText("S 返回", true)
	post(tcell.KeyF1, 0)
	waitForText("收起系统日志", true)
	post(tcell.KeyDown, 0)
	post(tcell.KeyEnter, 0)
	waitForText("S 返回", false)

	selectMenu := func(index int) {
		t.Helper()
		post(tcell.KeyF1, 0)
		for range index {
			post(tcell.KeyTab, 0)
		}
		post(tcell.KeyEnter, 0)
	}
	selectMenu(2)
	waitForText("已暂停", true)
	selectMenu(2)
	waitForText("已暂停", false)
	selectMenu(3)
	post(tcell.KeyBacktab, 0)
	post(tcell.KeyEnter, 0)
	waitForText("警告和错误", true)
	post(tcell.KeyEscape, 0)
	waitForText("警告和错误", false)
	selectMenu(4)
	waitForText("在线连接", true)
	post(tcell.KeyEscape, 0)
	waitForText("在线连接", false)
	selectMenu(5)
	waitForText("最近两分钟", true)
	post(tcell.KeyEscape, 0)
	waitForText("最近两分钟", false)
	selectMenu(6)
	selectMenu(7)
	waitForText("F1 Menu", true)
	post(tcell.KeyEscape, 0)
	waitForText("Confirm server shutdown", true)
	post(tcell.KeyEscape, 0)
	waitForText("Confirm server shutdown", false)
	selectMenu(7)
	waitForText("F1 菜单", true)

	post(tcell.KeyEscape, 0)
	waitForText("确认关闭服务端", true)
	post(tcell.KeyEnter, 0)
	waitForText("确认关闭服务端", false)
	select {
	case <-requested:
		t.Fatal("the default button should cancel shutdown")
	default:
	}
	post(tcell.KeyF1, 0)
	waitForText("展开系统日志", true)
	post(tcell.KeyEnter, 0)
	waitForText("确认关闭服务端", true)
	post(tcell.KeyRight, 0)
	post(tcell.KeyEnter, 0)
	select {
	case <-requested:
	case <-time.After(3 * time.Second):
		t.Fatal("confirm did not request shutdown")
	}
	select {
	case err := <-result:
		t.Fatalf("dashboard returned before the server finished: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	finished <- nil
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("dashboard did not restore the terminal after shutdown")
	}
}

func TestQuitDialogDimsDashboardAndHighlightsSelection(t *testing.T) {
	const width, height = 80, 24
	screen := simulationScreen(t, width, height)
	d := dashboard{}
	paint := func(choice quitChoice) {
		d.paint(screen, time.Now())
		drawQuitDialog(screen, choice, anim.ButtonShimmer.Travel, i18n.ZH)
	}
	tokens := dashboardOverlayTheme(screen)
	buttonY := (height-11)/2 + 7
	buttonColors := func(label rune) (tcell.Color, tcell.Color) {
		t.Helper()
		for x := range width {
			glyph, style, _ := screen.Get(x, buttonY)
			if glyph == string(label) {
				foreground, background, _ := style.Decompose()
				return foreground, background
			}
		}
		t.Fatalf("button %q is missing", label)
		return tcell.ColorDefault, tcell.ColorDefault
	}
	assertButton := func(label rune, want tcell.Color) {
		t.Helper()
		if fg, bg := buttonColors(label); fg != want || bg != tokens.Surface {
			t.Fatalf("button %q colors = %v, %v; want %v, %v", label, fg, bg, want, tokens.Surface)
		}
	}

	paint(quitCancel)
	if glyph, style, _ := screen.Get(1, 0); glyph != "实" || style != style.Dim(true) {
		t.Fatalf("expected a dimmed but visible dashboard, got %q with %v", glyph, style)
	}
	assertButton('取', tokens.Accent)
	assertButton('关', tokens.TextMuted)
	paint(quitConfirm)
	assertButton('关', tokens.Accent)
	assertButton('取', tokens.TextMuted)
}

func TestDashboardMenuFansOutFromLeft(t *testing.T) {
	const width, height = 80, 24
	screen := simulationScreen(t, width, height)
	now := time.Date(2026, 11, 27, 17, 25, 8, 0, time.Local)
	d := dashboard{}
	d.paint(screen, now)
	m := openDashboardMenu(now.Add(-time.Second), &d, false, false)
	m.draw(screen, now)
	if m.direction != views.FanRight {
		t.Fatalf("menu direction = %v, want right from the left edge", m.direction)
	}
	if marker, _, _ := screen.Get(0, height/2); marker != "│" && marker != "|" {
		t.Fatalf("left edge is missing the menu anchor: %q", marker)
	}
	if got := screenText(screen); !strings.Contains(got, "展开系统日志") || !strings.Contains(got, "17:25:08") {
		t.Fatalf("menu and clock should both be visible: %s", got)
	}
}

func TestCRTEdgesMeetAtTheCenter(t *testing.T) {
	const width, height = 40, 15
	screen := simulationScreen(t, width, height)
	litRows := func() []int {
		var rows []int
		for y := range height {
			glyph, _, _, _ := screen.GetContent(width/2, y)
			if glyph != ' ' {
				rows = append(rows, y)
			}
		}
		return rows
	}
	drawCRT(screen, 1)
	if rows := litRows(); len(rows) != 2 || rows[0] != 0 || rows[1] != height-1 {
		t.Fatalf("fully open edges: %v", rows)
	}
	drawCRT(screen, 0.43)
	if rows := litRows(); len(rows) != 2 || rows[0] <= 0 || rows[0]+rows[1] != height-1 {
		t.Fatalf("edges should move together symmetrically: %v", rows)
	}
	drawCRT(screen, 0.1)
	if rows := litRows(); len(rows) != 1 || rows[0] != height/2 {
		t.Fatalf("center flash: %v", rows)
	}
	drawCRT(screen, 0)
	if rows := litRows(); len(rows) != 0 {
		t.Fatalf("screen should be dark: %v", rows)
	}
	_, style, _ := screen.Get(width/2, height/2)
	_, background, _ := style.Decompose()
	if background != tcell.ColorBlack {
		t.Fatalf("dark frame has background %v", background)
	}
}

func TestDashboardModulesRevealInSequence(t *testing.T) {
	screen := simulationScreen(t, 80, 24)
	d := dashboard{}
	now := time.Now()
	d.drawModules(screen, now, 0)
	if got := strings.TrimSpace(screenText(screen)); got != "" {
		t.Fatalf("closed modules are visible: %s", got)
	}
	d.drawModules(screen, now, 0.22)
	if got := screenText(screen); !strings.Contains(got, "─") || strings.Contains(got, "系统") {
		t.Fatalf("expected the top rule before the system panel: %s", got)
	}
	d.drawModules(screen, now, 1)
	if got := screenText(screen); !strings.Contains(got, "实况") || !strings.Contains(got, "系统") {
		t.Fatalf("settled dashboard is missing modules: %s", got)
	}
}

func TestShutdownWaitsForTeardownBeforeShowingCompletion(t *testing.T) {
	screen := simulationScreen(t, 80, 24)
	now := time.Now()
	transition := dashboardTransition{started: now.Add(-time.Minute), stopping: now}
	d := dashboard{}

	progressStart := now.Add(moduleDuration)
	transition.drawShutdown(screen, &d, progressStart, server.ShutdownDraining)
	waiting := progressStart.Add(10 * time.Second)
	if end := transition.drawShutdown(screen, &d, waiting, server.ShutdownDraining); end ||
		!strings.Contains(screenText(screen), "72%") || strings.Contains(screenText(screen), "100%") {
		t.Fatalf("progress should wait for connections: %s", screenText(screen))
	}
	transition.finished = waiting.Add(time.Second)
	if end := transition.drawShutdown(screen, &d, transition.finished, server.ShutdownGraceExpired); end ||
		!strings.Contains(screenText(screen), "100%") || !strings.Contains(screenText(screen), "连接等待超时") {
		t.Fatalf("completion should describe the actual outcome: %s", screenText(screen))
	}
	closing := transition.finished.Add(completionPause)
	if end := transition.drawShutdown(screen, &d, closing, server.ShutdownGraceExpired); end ||
		strings.Contains(screenText(screen), "100%") {
		t.Fatalf("CRT phase should take over after completion: %s", screenText(screen))
	}
	if end := transition.drawShutdown(screen, &d, closing.Add(crtDuration+blackPause), server.ShutdownGraceExpired); !end ||
		strings.TrimSpace(screenText(screen)) != "" {
		t.Fatalf("terminal should end on a black frame: %s", screenText(screen))
	}
}

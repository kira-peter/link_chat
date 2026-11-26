package live

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/mattn/go-runewidth"

	"link-chat/pkg/server"
)

func TestDashboardUserGridAndOverflow(t *testing.T) {
	screen := simulationScreen(t, 80, 14)
	now := time.Now()
	d := dashboard{users: make(map[string]server.LiveUser)}
	for i := range 30 {
		id := fmt.Sprintf("user%02d", i)
		d.users[id] = server.LiveUser{ActivityUser: server.ActivityUser{ID: id, Name: id}}
	}
	for _, user := range []server.LiveUser{
		{ActivityUser: server.ActivityUser{ID: "carol", Name: "carol"}},
		{ActivityUser: server.ActivityUser{ID: "bob", Name: "bob"}, Online: true, OnlineSince: now.Add(-time.Hour).UnixMilli()},
		{ActivityUser: server.ActivityUser{ID: "alice", Name: "alice"}, Online: true, OnlineSince: now.UnixMilli()},
	} {
		d.users[user.ID] = user
	}
	rows, overflow := d.drawUsers(screen, 64, 9, now)
	screen.Show()
	if overflow == nil {
		t.Fatal("missing overflow anchor")
	}
	if marker := fmt.Sprintf("还有 %d 人", len(d.users)-len(rows)); !strings.Contains(screenText(screen), marker) {
		t.Fatalf("missing %q in %s", marker, screenText(screen))
	}
	for _, expected := range []struct {
		id    string
		color tcell.Color
	}{{"alice", tcell.ColorPaleGreen}, {"bob", tcell.ColorGreen}, {"carol", tcell.ColorGray}} {
		location, visible := rows[expected.id]
		if !visible {
			t.Fatalf("%s missing from first row", expected.id)
		}
		letter, _, style, _ := screen.GetContent(location.x, location.y-1)
		fg, _, _ := style.Decompose()
		if letter != rune(expected.id[0]) || fg != expected.color {
			t.Fatalf("%s: initial %q, color %v", expected.id, letter, fg)
		}
	}
	if rows["alice"].x >= rows["bob"].x || rows["bob"].x >= rows["carol"].x {
		t.Fatalf("grid order: %v", rows)
	}
}

func TestDashboardFourPanelsWrapSystemMessages(t *testing.T) {
	now := time.Now()
	d := dashboard{panels: make(map[server.ActivityCategory]server.ActivityPanel)}
	for _, category := range server.ActivityCategories() {
		for i := range 12 {
			entry := server.ActivityEntry{At: now.Add(-time.Duration(14-i) * time.Second).UnixMilli(), Summary: fmt.Sprintf("activity %02d", i)}
			d.panels[category] = d.panels[category].Add(entry)
		}
	}
	logs := NewLogSink()
	const long = "WRN storage error err=SQLite busy while reading D:/very/long/server/data/database.sqlite END-OF-DIAGNOSTIC"
	for i := range 8 {
		fmt.Fprintf(logs, "11:20:00 INF system event %d\n", i)
	}
	fmt.Fprintln(logs, long)
	d.technical = logs.snapshot()
	if len(d.technical.Recent) != server.ActivityPreviewLimit || d.technical.Total != 9 {
		t.Fatalf("technical log snapshot: %+v", d.technical)
	}

	screen := simulationScreen(t, 80, 24)
	d.draw(screen, now)
	body := screenText(screen)
	for _, want := range []string{"消息 12", "社交 12", "账号 12", "系统 21", "S 展开", "WRN storage error", "…"} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in %s", want, body)
		}
	}
	if strings.Contains(body, "activity 00") {
		t.Fatal("old activity outside the preview window")
	}

	d.systemExpanded = true
	expanded := simulationScreen(t, 50, 14)
	d.draw(expanded, now)
	if body := screenText(expanded); !strings.Contains(body, "S 返回") || !strings.Contains(body, "END-OF-DIAGNOSTIC") {
		t.Fatalf("expanded system log: %s", body)
	}
	for _, line := range wrapSystemEntry("封禁已解除 alice bob carol 确认完成", 12, 6) {
		if runewidth.StringWidth(line) > 12 || strings.HasSuffix(line, "…") {
			t.Fatalf("incorrect wrapping with wide characters: %q", line)
		}
	}
}

func TestDashboardLinkGrowsAndRetractsFromSender(t *testing.T) {
	screen := simulationScreen(t, 50, 14)
	now := time.Now()
	link := linkAnimation{at: now, style: accent}
	from, to := point{1, 4}, point{16, 6}
	path := linkPath(from, to, 0)
	if path[0] != from || path[len(path)-1] != to {
		t.Fatalf("wrong endpoints: %v", path)
	}
	for _, phase := range []struct {
		progress float64
		from, to bool
	}{{0.10, true, false}, {0.50, true, true}, {0.85, false, true}, {1.0, false, false}} {
		screen.Clear()
		link.draw(screen, now.Add(time.Duration(phase.progress*float64(linkDuration))), path)
		start, _, _, _ := screen.GetContent(from.x, from.y)
		end, _, _, _ := screen.GetContent(to.x, to.y)
		if (start != ' ') != phase.from || (end != ' ') != phase.to {
			t.Fatalf("phase %.2f: sender=%q recipient=%q", phase.progress, start, end)
		}
	}
}

func simulationScreen(t *testing.T, width, height int) tcell.SimulationScreen {
	t.Helper()
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(screen.Fini)
	screen.SetSize(width, height)
	return screen
}

func screenText(screen tcell.SimulationScreen) string {
	cells, width, _ := screen.GetContents()
	var text strings.Builder
	for i, cell := range cells {
		if i%width == 0 {
			text.WriteByte('\n')
		}
		text.WriteString(string(cell.Runes))
	}
	return text.String()
}

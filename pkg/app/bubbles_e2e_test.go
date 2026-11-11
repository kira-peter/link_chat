package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/client"
	"link-chat/pkg/tui/views"
)

// findCell locates the first cell of the first occurrence of sub on the screen.
func findCell(s tcell.SimulationScreen, sub string) (x, y int, ok bool) {
	cells, width, height := s.GetContents()
	want := []rune(sub)
	for row := 0; row < height; row++ {
		for col := 0; col+len(want) <= width; col++ {
			match := true
			for i, r := range want {
				rs := cells[row*width+col+i].Runes
				if len(rs) == 0 || rs[0] != r {
					match = false
					break
				}
			}
			if match {
				return col, row, true
			}
		}
	}
	return 0, 0, false
}

func cellFG(s tcell.SimulationScreen, x, y int) tcell.Color {
	cells, w, _ := s.GetContents()
	fg, _, _ := cells[y*w+x].Style.Decompose()
	return fg
}

// Two real clients talk to each other. On each screen the other person's message
// sits on the left with the other-side border colour and the user's own message
// sits on the right with the self-side colour — from both points of view.
func TestBothSidesSeeTheirOwnMessagesOnTheRight(t *testing.T) {
	hs := startServer(t)
	url := "ws" + strings.TrimPrefix(hs.URL, "http") + "/ws"

	scrA := newTestScreen(t, 100, 30)
	a := New(scrA, Config{Server: url, Theme: "ink", Language: "zh"}, filepath.Join(t.TempDir(), "a.json"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.Run(ctx)
	registerAndWait(t, a, ctx, "alice")

	a.mu.Lock()
	a.ui.Prompt = views.PromptModel{Value: "lobby"}
	a.mu.Unlock()
	a.submitPrompt(ctx, true)
	waitFor(t, 15*time.Second, "lobby created", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.st.Active() != nil && a.st.Active().Name == "lobby"
	})

	scrB := newTestScreen(t, 100, 30)
	b := New(scrB, Config{Server: url, Theme: "ink", Language: "zh"}, filepath.Join(t.TempDir(), "b.json"))
	ctxB, cancelB := context.WithCancel(context.Background())
	defer cancelB()
	go b.Run(ctxB)
	registerAndWait(t, b, ctxB, "bob")

	a.mu.Lock()
	lobby := a.st.ActiveID
	a.mu.Unlock()
	openViaList(t, b, ctxB, lobby)
	waitFor(t, 15*time.Second, "bob joined lobby", func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		return b.st.Active() != nil && strings.Contains(b.st.Active().Name, "lobby")
	})

	send := func(app *App, cx context.Context, body string) {
		app.mu.Lock()
		app.st.RestoreInput(body)
		app.mu.Unlock()
		app.sendCurrent(cx)
	}
	send(a, ctx, "from alice")
	waitFor(t, 15*time.Second, "bob sees alice", func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		return hasMessage(b.st.Active(), "from alice")
	})
	send(b, ctxB, "from bob")
	waitFor(t, 15*time.Second, "alice sees bob", func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return hasMessage(a.st.Active(), "from alice") && hasMessage(a.st.Active(), "from bob")
	})

	for _, c := range []struct {
		name    string
		app     *App
		scr     tcell.SimulationScreen
		other   string
		self    string
		povName string
	}{
		{"alice", a, scrA, "from bob", "from alice", "alice's screen"},
		{"bob", b, scrB, "from alice", "from bob", "bob's screen"},
	} {
		c.app.mu.Lock()
		c.app.render()
		c.app.mu.Unlock()

		ox, _, ok := findCell(c.scr, c.other)
		if !ok {
			t.Fatalf("%s: the other's message is not drawn", c.povName)
		}
		sx, _, ok := findCell(c.scr, c.self)
		if !ok {
			t.Fatalf("%s: the user's own message is not drawn", c.povName)
		}
		if ox >= sx {
			t.Errorf("%s: other's message starts at %d, own at %d — own must be further right", c.povName, ox, sx)
		}
		if sx < 45 {
			t.Errorf("%s: own message starts at %d, want the right half", c.povName, sx)
		}
		if ox > 40 {
			t.Errorf("%s: other's message starts at %d, want the left side", c.povName, ox)
		}

		// The text sits inside a border: the left border cell one column and
		// two cells left of the text carries the border colour for that side.
		wantOther := c.app.tokens.BubbleOther
		wantSelf := c.app.tokens.BubbleSelf
		if wantOther == wantSelf {
			t.Fatalf("%s: theme draws both sides in one colour", c.povName)
		}
		gotOther := cellFG(c.scr, ox-2, mustY(c.scr, c.other))
		gotSelf := cellFG(c.scr, sx-2, mustY(c.scr, c.self))
		if gotOther != wantOther {
			t.Errorf("%s: other's border colour %v, want %v", c.povName, gotOther, wantOther)
		}
		if gotSelf != wantSelf {
			t.Errorf("%s: own border colour %v, want %v", c.povName, gotSelf, wantSelf)
		}
	}
}

func hasMessage(c *client.Conversation, body string) bool {
	if c == nil {
		return false
	}
	for _, m := range c.Messages {
		if m.Body == body {
			return true
		}
	}
	return false
}

func mustY(s tcell.SimulationScreen, sub string) int {
	_, y, _ := findCell(s, sub)
	return y
}

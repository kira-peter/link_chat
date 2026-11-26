// Package live renders the server's activity dashboard in an interactive terminal.
package live

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/server"
)

type Source interface {
	SubscribeActivity(context.Context) <-chan server.Activity
	LiveSnapshot() (server.LiveSnapshot, error)
}

type LogSink struct {
	mu      sync.Mutex
	panel   server.ActivityPanel
	changed chan struct{}
}

func NewLogSink() *LogSink { return &LogSink{changed: make(chan struct{}, 1)} }

func (l *LogSink) Write(p []byte) (int, error) {
	now := time.Now().UnixMilli()
	l.mu.Lock()
	for _, line := range strings.Split(strings.TrimRight(string(p), "\r\n"), "\n") {
		if len(line) > 9 && line[2] == ':' && line[5] == ':' && line[8] == ' ' {
			line = line[9:]
		}
		if line != "" {
			l.panel = l.panel.Add(server.ActivityEntry{At: now, Summary: line})
		}
	}
	l.mu.Unlock()
	select {
	case l.changed <- struct{}{}:
	default:
	}
	return len(p), nil
}

func (l *LogSink) snapshot() server.ActivityPanel {
	l.mu.Lock()
	defer l.mu.Unlock()
	panel := l.panel
	panel.Recent = slices.Clone(panel.Recent)
	return panel
}

// Open initializes the screen before the server redirects its logs.
func Open() (tcell.Screen, error) {
	screen, err := tcell.NewScreen()
	if err != nil {
		return nil, err
	}
	if err := screen.Init(); err != nil {
		return nil, err
	}
	screen.HideCursor()
	return screen, nil
}

// Run owns an initialized screen until the server stops. quit requests
// shutdown, allowing the dashboard to keep showing activity during the grace period.
func Run(ctx context.Context, screen tcell.Screen, src Source, logs *LogSink, quit func()) {
	defer screen.Fini()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	activities := src.SubscribeActivity(ctx)
	d := dashboard{
		users:    make(map[string]server.LiveUser),
		channels: make(map[string]channelBox),
		panels:   make(map[server.ActivityCategory]server.ActivityPanel),
	}
	if err := d.load(src); err != nil {
		fmt.Fprintf(logs, "读取实况失败：%v", err)
	}
	d.technical = logs.snapshot()

	events := make(chan tcell.Event)
	go screen.ChannelEvents(events, ctx.Done())
	frames := time.NewTicker(time.Second / 30)
	defer frames.Stop()
	seconds := time.NewTicker(time.Second)
	defer seconds.Stop()

	d.draw(screen, time.Now())
	dirty := false
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				quit()
				return
			}
			switch event := ev.(type) {
			case *tcell.EventResize:
				screen.Sync()
			case *tcell.EventKey:
				if event.Key() == tcell.KeyCtrlC || event.Rune() == 'q' || event.Rune() == 'Q' {
					quit()
				}
				if event.Rune() == 's' || event.Rune() == 'S' {
					d.systemExpanded = !d.systemExpanded
				}
			}
			dirty = true
		case a, ok := <-activities:
			if !ok {
				activities = nil
				continue
			}
			now := time.Now()
			if a.Sequence > d.sequence {
				if a.Sequence != d.sequence+1 {
					// A busy producer can overrun the bounded local queue.
					// Reconcile presence and ordering from the current state.
					_ = d.load(src)
				}
				if a.Sequence > d.sequence {
					d.apply(a)
				}
			}
			d.animate(a, now)
			dirty = true
		case <-logs.changed:
			d.technical = logs.snapshot()
			dirty = true
		case <-seconds.C:
			dirty = true
		case now := <-frames.C:
			if dirty || (!d.systemExpanded && len(d.links) != 0) {
				d.draw(screen, now)
				dirty = false
			}
		}
	}
}

const (
	linkDuration = 1100 * time.Millisecond
	channelTTL   = 8 * time.Second
)

type channelBox struct {
	name string
	at   time.Time
}

type linkAnimation struct {
	from, to, channel string
	at                time.Time
	style             tcell.Style
}

type dashboard struct {
	users          map[string]server.LiveUser
	ordered        []string
	stats          server.LiveStats
	sequence       uint64
	channels       map[string]channelBox
	links          []linkAnimation
	panels         map[server.ActivityCategory]server.ActivityPanel
	technical      server.ActivityPanel
	systemExpanded bool
}

func (d *dashboard) load(src Source) error {
	snap, err := src.LiveSnapshot()
	if err != nil {
		return err
	}
	clear(d.users)
	for _, u := range snap.Users {
		d.users[u.ID] = u
	}
	d.stats, d.sequence, d.ordered = snap.Stats, snap.Sequence, nil
	if snap.Activity != nil {
		d.panels = snap.Activity
	} else {
		clear(d.panels)
	}
	return nil
}

func (d *dashboard) apply(a server.Activity) {
	d.sequence = a.Sequence
	d.panels[a.Category] = d.panels[a.Category].Add(server.ActivityEntry{At: a.At, Summary: a.Summary})
	if a.Actor.ID == "cli" || a.Actor.ID == "" {
		return
	}
	u := d.users[a.Actor.ID]
	newUser := u.ID == ""
	u.ActivityUser = a.Actor
	switch a.Kind {
	case "online":
		u.Online, u.OnlineSince = true, a.At
	case "login":
		if u.Online {
			u.OnlineSince = a.At
		}
	case "offline":
		u.Online, u.OnlineSince = false, 0
	}
	d.users[u.ID] = u
	if newUser {
		d.ordered = nil
	}
}

func (d *dashboard) animate(a server.Activity, now time.Time) {
	if a.Kind == "channel.deleted" && a.Channel != nil {
		delete(d.channels, a.Channel.ID)
	}
	if a.Kind != "message" {
		return
	}
	f := linkAnimation{from: a.Actor.ID, at: now, style: accent}
	if a.Channel != nil {
		f.channel = a.Channel.ID
		d.channels[f.channel] = channelBox{a.Channel.Name, now}
	} else if a.Target != nil {
		f.to = a.Target.ID
	}
	if len(d.links) == 50 {
		copy(d.links, d.links[1:])
		d.links = d.links[:49]
	}
	d.links = append(d.links, f)
}

func (d *dashboard) orderedUsers() []string {
	if d.ordered == nil {
		d.ordered = make([]string, 0, len(d.users))
		for id := range d.users {
			d.ordered = append(d.ordered, id)
		}
		slices.SortFunc(d.ordered, func(a, b string) int {
			return strings.Compare(d.users[a].Name, d.users[b].Name)
		})
	}
	return d.ordered
}

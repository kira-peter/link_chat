// Package live renders the server's activity dashboard in an interactive terminal.
package live

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/i18n"
	"link-chat/pkg/server"
)

type Source interface {
	SubscribeActivity(context.Context) <-chan server.Activity
	LiveSnapshot() (server.LiveSnapshot, error)
	ShutdownPhase() server.ShutdownPhase
	DashboardConnections() []server.OnlineConn
	DashboardChannelCounts([]string) (map[string]server.LiveChannel, error)
	DashboardSimulation() (url string, enabled bool)
	SetDashboardSimulation(enabled bool)
}

type LogSink struct {
	mu       sync.Mutex
	panel    server.ActivityPanel
	warnings server.ActivityPanel
	changed  chan struct{}
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
			entry := server.ActivityEntry{At: now, Summary: line}
			l.panel = l.panel.Add(entry)
			level, _, _ := strings.Cut(line, " ")
			if level == "WRN" || level == "ERR" {
				l.warnings = l.warnings.Add(entry)
			}
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
	return clonePanel(l.panel)
}

func (l *LogSink) update(d *dashboard) {
	l.mu.Lock()
	defer l.mu.Unlock()
	d.technical, d.warnings = clonePanel(l.panel), clonePanel(l.warnings)
}

func clonePanel(panel server.ActivityPanel) server.ActivityPanel {
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

// Run owns the screen through the opening and closing animations. ctx is
// cancelled when shutdown is requested. finished reports when teardown ends.
func Run(ctx context.Context, screen tcell.Screen, src Source, logs *LogSink, openPage func(string) error, quit func(), finished <-chan error) error {
	defer screen.Fini()
	eventsCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	activities := src.SubscribeActivity(eventsCtx)
	d := dashboard{
		users:    make(map[string]server.LiveUser),
		channels: make(map[string]channelBox),
		panels:   make(map[server.ActivityCategory]server.ActivityPanel),
	}
	d.adminURL, d.simulationEnabled = src.DashboardSimulation()
	if err := d.load(src); err != nil {
		fmt.Fprintf(logs, "读取实况失败：%v", err)
	}
	logs.update(&d)

	events := make(chan tcell.Event)
	go screen.ChannelEvents(events, eventsCtx.Done())
	frames := time.NewTicker(time.Second / 30)
	defer frames.Stop()

	transition := dashboardTransition{started: time.Now()}
	confirm := quitHidden
	var menu dashboardMenu
	var confirmFocusAt time.Time
	var paintedSecond int64
	var detailSecond int64
	var nextSnapshot time.Time
	var snapshotNeeded bool
	var paused *dashboard
	var pausedAt time.Time
	stop := ctx.Done()
	beginShutdown := func(now time.Time) {
		if transition.stopping.IsZero() {
			transition.stopping = now
			stop, activities = nil, nil
		}
	}
	finishStartup := func(now time.Time) {
		if intro := crtDuration + moduleDuration; now.Sub(transition.started) < intro {
			transition.started = now.Add(-intro)
		}
	}
	openConfirm := func(now time.Time) {
		menu = dashboardMenu{}
		confirm, confirmFocusAt = quitCancel, now
		finishStartup(now)
	}
	openDetails := func(page detailPage, now time.Time) {
		paused = nil
		d.page, d.detailOffset = page, 0
		d.refreshDetails(src, now)
		detailSecond = now.Unix()
	}
	drawCRT(screen, 0)
	dirty := false
	for {
		select {
		case <-stop:
			beginShutdown(time.Now())
		case err := <-finished:
			if err != nil {
				return err
			}
			now := time.Now()
			beginShutdown(now)
			transition.finished = now
			finished = nil
		case ev, ok := <-events:
			if !ok {
				quit()
				if finished != nil {
					return <-finished
				}
				return nil
			}
			switch event := ev.(type) {
			case *tcell.EventResize:
				screen.Sync()
			case *tcell.EventKey:
				now := time.Now()
				if !transition.stopping.IsZero() {
					break
				}
				if confirm != quitHidden {
					previous := confirm
					if confirm.handle(event) {
						quit()
						beginShutdown(now)
					}
					if confirm != quitHidden && confirm != previous {
						confirmFocusAt = now
					}
					break
				}
				switch {
				case event.Key() == tcell.KeyCtrlC:
					openConfirm(now)
				case menu.open:
					switch action := menu.handle(event.Key(), now); action {
					case menuQuit:
						openConfirm(now)
					case menuNone:
					case menuFilters, menuSimulation:
						menu = openDashboardMenu(now, &d, paused != nil, action)
					case menuParent:
						parent := menu.level
						menu = openDashboardMenu(now, &d, paused != nil, menuNone)
						menu.selected = slices.Index(menu.actions, parent)
					case menuSystemLogs:
						menu.open, menu.changed = false, now
						paused, d.page = nil, noDetails
						d.systemExpanded = !d.systemExpanded
					case menuPause:
						menu.open, menu.changed = false, now
						if paused == nil {
							paused, pausedAt = d.freeze(), now
						} else {
							paused = nil
						}
					case menuMotion:
						d.still = !d.still
						d.links = nil
						if paused != nil {
							paused.still, paused.links = d.still, nil
						}
						menu = dashboardMenu{}
					case menuLanguage:
						if d.lang == i18n.EN {
							d.lang = i18n.ZH
						} else {
							d.lang = i18n.EN
						}
						if paused != nil {
							paused.lang = d.lang
						}
						menu = dashboardMenu{}
					case menuConnections:
						menu.open, menu.changed = false, now
						openDetails(connectionDetails, now)
					case menuChannels:
						menu.open, menu.changed = false, now
						openDetails(channelDetails, now)
					case menuSimulationEnable, menuSimulationDisable:
						src.SetDashboardSimulation(action == menuSimulationEnable)
						d.adminURL, d.simulationEnabled = src.DashboardSimulation()
						menu = openDashboardMenu(now, &d, paused != nil, menuNone)
						menu.selected = slices.Index(menu.actions, menuSimulation)
					case menuOpenAdmin, menuOpenSimulation:
						menu.open, menu.changed = false, now
						address := d.adminURL
						if action == menuOpenSimulation {
							address += "#simulation"
						}
						message := tr(d.lang, "无法打开浏览器，请手动访问", "Unable to open browser. Open this address manually")
						go func() {
							if err := openPage(address); err != nil {
								fmt.Fprintf(logs, "WRN %s %s (%v)\n", message, address, err)
							}
						}()
					default:
						menu.open, menu.changed = false, now
						d.filter = action
						openDetails(logDetails, now)
					}
				case d.page != noDetails && (event.Key() == tcell.KeyEscape || event.Key() == tcell.KeyBackspace):
					paused, d.page = nil, noDetails
				case event.Key() == tcell.KeyEscape:
					openConfirm(now)
				case event.Key() == tcell.KeyF1:
					finishStartup(now)
					menu = openDashboardMenu(now, &d, paused != nil, menuNone)
				case event.Rune() == 's' || event.Rune() == 'S':
					paused, d.page = nil, noDetails
					d.systemExpanded = !d.systemExpanded
				case d.page != noDetails:
					display := &d
					if paused != nil {
						display = paused
					}
					_, height := screen.Size()
					switch event.Key() {
					case tcell.KeyDown:
						display.detailOffset++
					case tcell.KeyUp:
						display.detailOffset = max(0, display.detailOffset-1)
					case tcell.KeyPgDn:
						display.detailOffset += max(1, height-2)
					case tcell.KeyPgUp:
						display.detailOffset = max(0, display.detailOffset-max(1, height-2))
					}
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
					// Reconcile at most once a second while activity is heavy.
					snapshotNeeded = true
				}
				d.apply(a)
			}
			d.animate(a, now)
			dirty = true
		case <-logs.changed:
			logs.update(&d)
			dirty = true
		case now := <-frames.C:
			if !transition.stopping.IsZero() {
				if transition.drawShutdown(screen, &d, now, src.ShutdownPhase()) {
					return nil
				}
				continue
			}
			if snapshotNeeded && !now.Before(nextSnapshot) {
				snapshotNeeded = d.load(src) != nil
				nextSnapshot = now.Add(time.Second)
				dirty = true
			}
			if now.Unix() != detailSecond {
				detailSecond = now.Unix()
				for id, ch := range d.channels {
					if now.Sub(ch.at) >= channelActivityWindow {
						delete(d.channels, id)
					}
				}
				if paused == nil {
					d.refreshDetails(src, now)
				}
			}
			display, shownAt := &d, now
			if paused != nil {
				display, shownAt = paused, pausedAt
			}
			if transition.drawStartup(screen, &d, now) {
				dirty = true // Paint the settled dashboard on the next frame.
				continue
			}
			if menu.visible(now) {
				display.paint(screen, shownAt)
				menu.draw(screen, now)
				screen.Show()
				dirty = true // Repaint once more when the fan finishes closing.
				continue
			}
			focusAge := now.Sub(confirmFocusAt)
			linksMoving := d.page == noDetails && !d.systemExpanded && len(d.links) != 0
			if dirty || paused == nil && (paintedSecond != now.Unix() || linksMoving) || !d.still && confirm.sweeping(focusAge) {
				if confirm == quitHidden {
					display.draw(screen, shownAt)
				} else {
					display.paint(screen, shownAt)
					if d.still {
						focusAge = time.Hour
					}
					drawQuitDialog(screen, confirm, focusAge, d.lang)
				}
				paintedSecond = now.Unix()
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
	users             map[string]server.LiveUser
	ordered           []string
	stats             server.LiveStats
	sequence          uint64
	channels          map[string]channelBox
	links             []linkAnimation
	panels            map[server.ActivityCategory]server.ActivityPanel
	technical         server.ActivityPanel
	warnings          server.ActivityPanel
	systemExpanded    bool
	lang              i18n.Lang
	still, frozen     bool
	page              detailPage
	filter            menuAction
	detailOffset      int
	detailError       bool
	connections       []server.OnlineConn
	channelCounts     map[string]server.LiveChannel
	adminURL          string
	simulationEnabled bool
}

func (d *dashboard) freeze() *dashboard {
	frozen := *d
	frozen.frozen = true
	frozen.users, frozen.channels = maps.Clone(d.users), maps.Clone(d.channels)
	frozen.panels = maps.Clone(d.panels)
	for category, panel := range frozen.panels {
		frozen.panels[category] = clonePanel(panel)
	}
	frozen.links = slices.Clone(d.links)
	return &frozen
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
	entry := server.ActivityEntry{At: a.At, Summary: a.Summary, ActorID: a.Actor.ID}
	if a.Target != nil {
		entry.TargetID = a.Target.ID
	}
	if a.Kind == "account.deleted" {
		for k, panel := range d.panels {
			d.panels[k] = panel.WithoutUser(a.Actor.ID)
		}
		delete(d.users, a.Actor.ID)
		d.ordered = nil
	}
	d.panels[a.Category] = d.panels[a.Category].Add(entry)
	if a.Kind == "account.deleted" {
		return
	}
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
	colors := [...]int32{0x669dff, 0x55e2eb, 0xffdf71, 0xffac52, 0xff706c}
	if a.MessageBand > 0 && a.MessageBand <= len(colors) {
		f.style = accent.Foreground(tcell.NewHexColor(colors[a.MessageBand-1]))
	}
	if a.Channel != nil {
		f.channel = a.Channel.ID
		d.channels[f.channel] = channelBox{a.Channel.Name, now}
	} else if a.Target != nil {
		f.to = a.Target.ID
	}
	if d.still {
		return
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

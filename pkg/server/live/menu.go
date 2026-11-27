package live

import (
	"time"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/i18n"
	"link-chat/pkg/server"
	"link-chat/pkg/tui/anim"
	"link-chat/pkg/tui/views"
	"link-chat/pkg/tui/widget"
)

type dashboardMenu struct {
	open, filters, still bool
	changed              time.Time
	selected             int
	direction            views.FanDir
	labels               []string
	actions              []menuAction
}

type menuAction int

const (
	menuNone menuAction = iota
	menuQuit
	menuSystemLogs
	menuPause
	menuFilters
	menuConnections
	menuChannels
	menuMotion
	menuLanguage
	menuParent
	menuLogAll
	menuLogMessage
	menuLogSocial
	menuLogAccount
	menuLogSystem
	menuLogWarnings
)

func openDashboardMenu(now time.Time, d *dashboard, paused, filters bool) dashboardMenu {
	m := dashboardMenu{open: true, filters: filters, still: d.still, changed: now}
	l := d.lang
	if filters {
		m.labels = []string{tr(l, "全部日志", "All logs")}
		for _, category := range server.ActivityCategories() {
			m.labels = append(m.labels, categoryName(l, category))
		}
		m.labels = append(m.labels, tr(l, "警告和错误", "Warnings and errors"))
		m.actions = []menuAction{menuLogAll, menuLogMessage, menuLogSocial, menuLogAccount, menuLogSystem, menuLogWarnings}
		return m
	}
	logs := tr(l, "展开系统日志", "Expand system logs")
	if d.systemExpanded {
		logs = tr(l, "收起系统日志", "Collapse system logs")
	}
	pause := tr(l, "暂停画面", "Pause display")
	if paused {
		pause = tr(l, "恢复画面", "Resume display")
	}
	motion := tr(l, "关闭动效", "Disable animation")
	if d.still {
		motion = tr(l, "开启动效", "Enable animation")
	}
	m.labels = []string{tr(l, "退出", "Quit"), logs, pause, tr(l, "筛选日志", "Filter logs"),
		tr(l, "查看连接", "Connections"), tr(l, "查看活跃频道", "Active channels"),
		motion, tr(l, "切换语言 · English", "Language · 中文")}
	m.actions = []menuAction{menuQuit, menuSystemLogs, menuPause, menuFilters, menuConnections, menuChannels, menuMotion, menuLanguage}
	return m
}

func (m *dashboardMenu) handle(key tcell.Key, now time.Time) menuAction {
	next, prev, _, back := views.FanKeys(m.direction)
	switch key {
	case tcell.KeyF1:
		m.open, m.changed = false, now
	case tcell.KeyEscape, back:
		if m.filters {
			return menuParent
		}
		m.open, m.changed = false, now
	case next, tcell.KeyTab:
		m.selected = (m.selected + 1) % len(m.actions)
	case prev, tcell.KeyBacktab:
		m.selected = (m.selected + len(m.actions) - 1) % len(m.actions)
	case tcell.KeyEnter:
		return m.actions[m.selected]
	}
	return menuNone
}

func (m *dashboardMenu) visible(now time.Time) bool {
	return m.open || !m.still && !m.changed.IsZero() && now.Sub(m.changed) < views.FanCloseDuration()
}

func (m *dashboardMenu) draw(screen tcell.Screen, now time.Time) {
	w, h := screen.Size()
	if w < 2 || h < 1 {
		return
	}
	tokens := dashboardOverlayTheme(screen)
	anchor := widget.Rect{Y: h / 2, W: 1, H: 1}
	marker := "|"
	if tokens.UnicodeBox {
		marker = "│"
	}
	screen.PutStrStyled(anchor.X, anchor.Y, marker, tokens.Accented())
	region := widget.Rect{W: w, H: h}
	if h > 2 {
		_, region = region.Take(1)
	}
	m.direction = (views.FanWheel{T: tokens}).Render(screen, region, views.FanModel{
		Labels: m.labels, Sel: m.selected,
		Anchor: anchor, Sideways: true, Prefer: views.FanRight,
		Motion: !m.still, Since: max(0, now.Sub(m.changed)), Closing: !m.open,
		Shimmer: m.open && !m.still, Depth: anim.Depth(tokens.ColorCap),
	}, i18n.ZH)
}

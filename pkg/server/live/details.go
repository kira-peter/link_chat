package live

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/i18n"
	"link-chat/pkg/server"
)

type detailPage int

const (
	noDetails detailPage = iota
	connectionDetails
	channelDetails
	logDetails
)

const channelActivityWindow = 2 * time.Minute

func tr(lang i18n.Lang, zh, en string) string {
	if lang == i18n.EN {
		return en
	}
	return zh
}

func categoryName(lang i18n.Lang, category server.ActivityCategory) string {
	switch category {
	case server.ActivityMessage:
		return tr(lang, "消息", "Messages")
	case server.ActivitySocial:
		return tr(lang, "社交", "Social")
	case server.ActivityAccount:
		return tr(lang, "账号", "Accounts")
	default:
		return tr(lang, "系统", "System")
	}
}

func (d *dashboard) activeChannels(now time.Time) []string {
	var ids []string
	for id, ch := range d.channels {
		if now.Sub(ch.at) < channelActivityWindow {
			ids = append(ids, id)
		}
	}
	slices.SortFunc(ids, func(a, b string) int {
		if byTime := d.channels[b].at.Compare(d.channels[a].at); byTime != 0 {
			return byTime
		}
		return strings.Compare(a, b)
	})
	return ids
}

func (d *dashboard) refreshDetails(src Source, now time.Time) {
	d.detailError = false
	switch d.page {
	case connectionDetails:
		d.connections = src.DashboardConnections()
		slices.SortStableFunc(d.connections, func(a, b server.OnlineConn) int {
			if byName := strings.Compare(d.connectionName(a), d.connectionName(b)); byName != 0 {
				return byName
			}
			return strings.Compare(a.Device, b.Device)
		})
	case channelDetails:
		counts, err := src.DashboardChannelCounts(d.activeChannels(now))
		d.channelCounts, d.detailError = counts, err != nil
	}
}

func (d *dashboard) connectionName(c server.OnlineConn) string {
	if name := d.users[c.UserID].Name; name != "" {
		return name
	}
	return c.UserID
}

func nonempty(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func (d *dashboard) detailRows(now time.Time) (title string, rows []string) {
	l := d.lang
	switch d.page {
	case connectionDetails:
		title = tr(l, "在线连接", "Online connections")
		for _, c := range d.connections {
			rows = append(rows, fmt.Sprintf("%s  │  %s %s  │  %s %s  │  %s %s",
				d.connectionName(c), tr(l, "设备", "Device"), nonempty(c.Device),
				tr(l, "客户端", "Client"), nonempty(c.ClientVersion), tr(l, "已连接", "Connected"), since(now, c.Since, l)))
		}
	case channelDetails:
		title = tr(l, "活跃频道 · 最近两分钟", "Active channels · Last two minutes")
		for _, id := range d.activeChannels(now) {
			activity := d.channels[id]
			members, known := d.channelCounts[id]
			count := "—"
			if known {
				count = fmt.Sprintf("%d/%d", members.OnlineCount, members.MemberCount)
			}
			rows = append(rows, fmt.Sprintf("#%s  │  %s %s  │  %s %s", activity.name,
				tr(l, "在线", "Online"), count, tr(l, "最近活跃", "Last active"), activity.at.Format("15:04:05")))
		}
	case logDetails:
		return d.filteredLogs()
	}
	return
}

func (d *dashboard) filteredLogs() (string, []string) {
	type row struct {
		server.ActivityEntry
		category server.ActivityCategory
	}
	l := d.lang
	title := tr(l, "全部日志", "All logs")
	var entries []row
	add := func(category server.ActivityCategory, panel server.ActivityPanel) {
		for i := len(panel.Recent) - 1; i >= 0; i-- {
			entries = append(entries, row{panel.Recent[i], category})
		}
	}
	switch d.filter {
	case menuLogWarnings:
		title = tr(l, "警告和错误", "Warnings and errors")
		add(server.ActivitySystem, d.warnings)
	case menuLogMessage, menuLogSocial, menuLogAccount, menuLogSystem:
		category := server.ActivityCategories()[d.filter-menuLogMessage]
		title = categoryName(l, category)
		add(category, d.panels[category])
		if category == server.ActivitySystem {
			add(category, d.technical)
		}
	default:
		for _, category := range server.ActivityCategories() {
			add(category, d.panels[category])
		}
		add(server.ActivitySystem, d.technical)
	}
	slices.SortStableFunc(entries, func(a, b row) int { return time.UnixMilli(b.At).Compare(time.UnixMilli(a.At)) })
	rows := make([]string, 0, len(entries))
	for _, entry := range entries {
		rows = append(rows, fmt.Sprintf("%s  %s  %s", time.UnixMilli(entry.At).Format(activityTimeFormat), categoryName(l, entry.category), entry.Summary))
	}
	return title, rows
}

func (d *dashboard) drawDetails(screen tcell.Screen, now time.Time, headerRight, w, h int) {
	title, rows := d.detailRows(now)
	put(screen, 1, 0, headerRight, accent.Bold(true), title)
	if h < 3 {
		return
	}
	drawDivider(screen, 1, w)
	if d.detailError {
		put(screen, 1, 2, w, muted, tr(d.lang, "暂时无法读取频道成员", "Unable to load channel membership"))
		return
	}
	if len(rows) == 0 {
		put(screen, 1, 2, w, muted, tr(d.lang, "暂无记录", "No entries"))
		return
	}
	var lines []string
	for _, row := range rows {
		lines = append(lines, wrapSystemEntry(singleLine(row), w-1, 4)...)
	}
	available := h - 2
	d.detailOffset = min(d.detailOffset, max(0, len(lines)-available))
	for i, line := range lines[d.detailOffset:min(len(lines), d.detailOffset+available)] {
		put(screen, 1, 2+i, w, bright, line)
	}
}

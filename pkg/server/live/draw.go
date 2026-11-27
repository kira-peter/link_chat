package live

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/gdamore/tcell/v2"
	"github.com/mattn/go-runewidth"
	"github.com/rivo/uniseg"

	"link-chat/pkg/server"
)

var categoryColor = map[server.ActivityCategory]tcell.Color{
	server.ActivityMessage: tcell.ColorAqua,
	server.ActivitySocial:  tcell.ColorFuchsia,
	server.ActivityAccount: tcell.ColorLightGreen,
	server.ActivitySystem:  tcell.ColorYellow,
}

var (
	bright = tcell.StyleDefault.Foreground(tcell.ColorWhite)
	muted  = tcell.StyleDefault.Foreground(tcell.ColorGray)
	accent = tcell.StyleDefault.Foreground(tcell.ColorAqua)
)

type point struct{ x, y int }

func put(screen tcell.Screen, x, y, limit int, style tcell.Style, text string) int {
	if x >= limit {
		return x
	}
	text = runewidth.Truncate(singleLine(text), limit-x, "…")
	screen.PutStrStyled(x, y, text, style)
	return x + runewidth.StringWidth(text)
}

func drawDivider(screen tcell.Screen, y, width int) {
	const glyph = "─"
	// Use the same cell widths as tcell. The console locale may make
	// go-runewidth measure these glyphs differently from the renderer.
	cells := uniseg.StringWidth(glyph)
	line := strings.Repeat(glyph, width/cells) + strings.Repeat("-", width%cells)
	screen.PutStrStyled(0, y, line, muted)
}

func singleLine(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, text)
}

func (d *dashboard) draw(screen tcell.Screen, now time.Time) {
	w, h := screen.Size()
	screen.Clear()
	if w < 2 || h < 1 {
		screen.Show()
		return
	}
	if d.systemExpanded {
		d.drawSystemPanel(screen, 0, w, 0, h, true)
		screen.Show()
		return
	}
	online := 0
	for _, u := range d.users {
		if u.Online {
			online++
		}
	}
	stats := fmt.Sprintf(" v%s  │  在线 %d/%d  │  运行 %s  │  Q 退出",
		d.stats.Version, online, len(d.users), since(now, d.stats.StartedAt))
	x := put(screen, 1, 0, w, accent.Bold(true), "实况")
	put(screen, x, 0, w, bright, stats)
	if h < 6 {
		screen.Show()
		return
	}

	divider := h - min(server.ActivityPreviewLimit+1, max(2, h/3)) - 1
	drawDivider(screen, 1, w)
	drawDivider(screen, divider, w)

	split := w
	if w >= 38 {
		split = w - min(26, max(15, w/5))
		for y := 2; y < divider; y++ {
			screen.PutStrStyled(split, y, "│", muted)
		}
		put(screen, split+2, 2, w, accent.Bold(true), "活跃频道")
	}
	put(screen, 1, 2, split, accent.Bold(true), "用户")
	if split >= 32 {
		x := put(screen, 8, 2, split, tcell.StyleDefault.Foreground(tcell.ColorLightGreen), "浅绿新登录")
		put(screen, x+2, 2, split, tcell.StyleDefault.Foreground(tcell.ColorGreen), "深绿长在线")
	}
	userRows, more := d.drawUsers(screen, split, divider, now)
	channelRows := d.drawChannels(screen, split, divider, w, now)
	d.drawLinks(screen, now, userRows, channelRows, more, split)
	d.drawPanels(screen, w, h, divider+1)
	screen.Show()
}

func (d *dashboard) drawUsers(screen tcell.Screen, width, end int, now time.Time) (map[string]point, *point) {
	users := d.orderedUsers()
	rows := make(map[string]point)
	if end < 5 {
		return rows, nil
	}
	type placement struct {
		id string
		at point
	}
	var placed []placement
	x, y := 1, 3
	for _, id := range users {
		slot := min(width-1, max(7, runewidth.StringWidth(d.users[id].Name)+3))
		if x > 1 && x+slot > width {
			x, y = 1, y+2
		}
		if y+1 >= end {
			break
		}
		placed = append(placed, placement{id, point{x, y}})
		x += slot
	}
	var more *point
	if len(placed) < len(users) {
		last := placed[len(placed)-1].at.y
		placed = slices.DeleteFunc(placed, func(p placement) bool { return p.at.y == last })
		put(screen, 1, last, width, muted, fmt.Sprintf("还有 %d 人", len(users)-len(placed)))
		more = &point{1, last + 1}
	}
	for _, p := range placed {
		u := d.users[p.id]
		put(screen, p.at.x, p.at.y, width, userStyle(u, now), u.Name)
		rows[p.id] = point{p.at.x, p.at.y + 1}
	}
	return rows, more
}

func userStyle(user server.LiveUser, now time.Time) tcell.Style {
	if !user.Online {
		return muted
	}
	age := now.Sub(time.UnixMilli(user.OnlineSince))
	color := tcell.ColorPaleGreen
	switch {
	case age >= time.Hour:
		color = tcell.ColorGreen
	case age >= 10*time.Minute:
		color = tcell.ColorSpringGreen
	case age >= time.Minute:
		color = tcell.ColorLightGreen
	}
	return tcell.StyleDefault.Foreground(color)
}

func (d *dashboard) drawChannels(screen tcell.Screen, split, end, width int, now time.Time) map[string]point {
	type item struct {
		id string
		channelBox
	}
	var channels []item
	for id, ch := range d.channels {
		if now.Sub(ch.at) >= channelTTL {
			delete(d.channels, id)
		} else {
			channels = append(channels, item{id, ch})
		}
	}
	rows := make(map[string]point)
	if split == width {
		return rows
	}
	slices.SortFunc(channels, func(a, b item) int {
		if a.at.Equal(b.at) {
			return strings.Compare(a.id, b.id)
		}
		return b.at.Compare(a.at)
	})
	for i, ch := range channels[:min(max(0, end-3)/3, len(channels))] {
		style := accent
		switch age := now.Sub(ch.at); {
		case age >= 7*time.Second:
			style = muted
		case age >= 5*time.Second:
			style = tcell.StyleDefault.Foreground(tcell.ColorTeal)
		}
		x, y := split+2, 3+i*3
		line := strings.Repeat("─", width-x-3)
		put(screen, x, y, width, style, "┌"+line+"┐")
		put(screen, x, y+1, width, style, "│")
		put(screen, x+2, y+1, width-3, style, "#"+ch.name)
		put(screen, width-2, y+1, width, style, "│")
		put(screen, x, y+2, width, style, "└"+line+"┘")
		rows[ch.id] = point{x - 1, y + 1}
	}
	return rows
}

func (d *dashboard) drawLinks(screen tcell.Screen, now time.Time, users, channels map[string]point, more *point, split int) {
	d.links = slices.DeleteFunc(d.links, func(link linkAnimation) bool { return now.Sub(link.at) >= linkDuration })
	for _, link := range d.links {
		from, shown := users[link.from]
		if !shown {
			if more == nil {
				continue
			}
			from = *more
		}
		to, shown := users[link.to]
		if link.channel != "" {
			to, shown = channels[link.channel]
		}
		if !shown {
			if more == nil || link.channel != "" {
				continue
			}
			to = *more
		}
		elbow := 0
		if link.channel != "" {
			elbow = split
		}
		link.draw(screen, now, linkPath(from, to, elbow))
	}
}

func linkPath(from, to point, elbow int) []point {
	path := []point{from}
	current := from
	if from.y != to.y {
		for current.x != elbow {
			current.x = stepToward(current.x, elbow)
			path = append(path, current)
		}
		for current.y != to.y {
			current.y = stepToward(current.y, to.y)
			path = append(path, current)
		}
	}
	for current.x != to.x {
		current.x = stepToward(current.x, to.x)
		path = append(path, current)
	}
	return path
}

func stepToward(from, to int) int {
	if to < from {
		return from - 1
	}
	return from + 1
}

func (link linkAnimation) draw(screen tcell.Screen, now time.Time, path []point) {
	progress := float64(now.Sub(link.at)) / float64(linkDuration)
	grow := min(1, max(0, progress/0.45))
	retract := min(1, max(0, (progress-0.57)/0.43))
	head := 1 + int(math.Round(grow*float64(len(path)-1)))
	tail := int(math.Floor(retract * float64(len(path))))
	arrived := head == len(path)
	path = path[min(tail, head):head]
	for i, p := range path {
		mask := 0
		if i > 0 {
			mask |= direction(p, path[i-1])
		} else if tail == 0 {
			mask |= north
		}
		if i+1 < len(path) {
			mask |= direction(p, path[i+1])
		} else if arrived {
			if link.channel == "" {
				mask |= north
			} else {
				mask |= east
			}
		}
		screen.PutStrStyled(p.x, p.y, lineGlyphs[mask], link.style)
	}
}

const (
	north = 1 << iota
	east
	south
	west
)

var lineGlyphs = [16]string{
	0: "·", north: "╵", east: "╶", south: "╷", west: "╴",
	north | south: "│", east | west: "─",
	south | east: "┌", south | west: "┐", north | east: "└", north | west: "┘",
}

func direction(from, to point) int {
	switch {
	case to.y < from.y:
		return north
	case to.y > from.y:
		return south
	case to.x > from.x:
		return east
	default:
		return west
	}
}

func (d *dashboard) drawPanels(screen tcell.Screen, width, height, start int) {
	categories := server.ActivityCategories()
	// Give technical log entries more room on terminals wide enough to keep
	// the other three columns readable.
	sharedWidth := width * 3 / 4
	if width >= 72 {
		sharedWidth = width * 2 / 3
	}
	for i, category := range categories {
		left, end := i*sharedWidth/3, (i+1)*sharedWidth/3
		if i != 0 {
			for y := start; y < height; y++ {
				screen.PutStrStyled(left, y, "│", muted)
			}
		}
		if category == server.ActivitySystem {
			d.drawSystemPanel(screen, left, width, start, height, false)
			continue
		}
		panel := d.panels[category]
		style := tcell.StyleDefault.Foreground(categoryColor[category]).Bold(true)
		put(screen, left+1, start, end, style, fmt.Sprintf("%s %d", category, panel.Total))
		for row := 0; row < min(height-start-1, len(panel.Recent)); row++ {
			entry := panel.Recent[len(panel.Recent)-1-row]
			text := entry.Summary
			if end-left >= 30 {
				text = time.UnixMilli(entry.At).Format("15:04") + " " + text
			}
			put(screen, left+1, start+1+row, end, bright, text)
		}
	}
}

func (d *dashboard) drawSystemPanel(screen tcell.Screen, left, right, top, bottom int, expanded bool) {
	panel := d.panels[server.ActivitySystem]
	panel.Total += d.technical.Total
	panel.Recent = append(slices.Clone(panel.Recent), d.technical.Recent...)
	slices.Reverse(panel.Recent)
	slices.SortStableFunc(panel.Recent, func(a, b server.ActivityEntry) int {
		return time.UnixMilli(b.At).Compare(time.UnixMilli(a.At))
	})
	style := tcell.StyleDefault.Foreground(categoryColor[server.ActivitySystem]).Bold(true)
	x := put(screen, left+1, top, right, style, fmt.Sprintf("系统 %d", panel.Total))
	hint := "S 展开"
	if expanded {
		hint = "S 返回  Q 退出"
	}
	put(screen, max(x+2, right-runewidth.StringWidth(hint)-1), top, right, muted, hint)

	columns := right - left - 1
	if columns < 2 {
		return
	}
	y := top + 1
	for i, entry := range panel.Recent[:min(len(panel.Recent), server.ActivityPreviewLimit)] {
		if y == bottom {
			break
		}
		text := singleLine(entry.Summary)
		room := bottom - y
		if expanded {
			text = time.UnixMilli(entry.At).Format("15:04") + " " + text
		} else if i+1 < len(panel.Recent) {
			room = min(room, 3)
		}
		for _, line := range wrapSystemEntry(text, columns, room) {
			put(screen, left+1, y, right, bright, line)
			y++
		}
	}
}

func wrapSystemEntry(text string, width, rows int) []string {
	var lines []string
	for len(lines) < rows && text != "" {
		if len(lines)+1 == rows {
			return append(lines, runewidth.Truncate(text, width, "…"))
		}
		line := runewidth.Truncate(text, width, "")
		if len(line) == len(text) {
			return append(lines, line)
		}
		// Prefer a word boundary for diagnostic fields, but use the full
		// width for Chinese text and long unbroken values such as paths.
		if cut := strings.LastIndexByte(line, ' '); cut > 0 && runewidth.StringWidth(line[:cut]) >= width/2 {
			line = line[:cut]
		}
		lines = append(lines, line)
		text = strings.TrimSpace(text[len(line):])
	}
	return lines
}

func since(now time.Time, at int64) string {
	if at == 0 {
		return "—"
	}
	d := max(0, now.Sub(time.UnixMilli(at)))
	switch {
	case d >= 24*time.Hour:
		return fmt.Sprintf("%d天", d/(24*time.Hour))
	case d >= time.Hour:
		return fmt.Sprintf("%d小时", d/time.Hour)
	case d >= time.Minute:
		return fmt.Sprintf("%d分", d/time.Minute)
	default:
		return fmt.Sprintf("%d秒", d/time.Second)
	}
}

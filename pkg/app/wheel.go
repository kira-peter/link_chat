package app

import (
	"context"
	"math"
	"time"

	"github.com/gdamore/tcell/v2"

	"link-chat/pkg/client"
	"link-chat/pkg/i18n"
	"link-chat/pkg/protocol"
	"link-chat/pkg/tui/layout"
	"link-chat/pkg/tui/views"
	"link-chat/pkg/tui/widget"
)

// The quick wheel (spec tui-quick-wheel): the wheel key fans out what can be
// done to the thing selected where the keyboard is; the arrows turn the fan,
// Enter runs the item in the middle, and the wheel key again or Esc closes it.

// wheelItem is one action on the wheel. Show, when set, says whether it can be
// done right now; an item that cannot is left off the wheel.
type wheelItem struct {
	Label i18n.T
	Run   func(a *App, ctx context.Context)
	Show  func(a *App) bool
	// Setting, when set, makes the item open a submenu of that setting's
	// candidates instead of running anything.
	Setting views.SettingID
}

// noSetting marks an item that is an action. SetUsername is read-only, so it is
// never a submenu.
const noSetting = views.SetUsername

// wheelSub is an open submenu: a setting's candidates fanned out from the item
// that opened it. Turning it puts each candidate in effect at once, unsaved, so
// the page shows it; Enter keeps it, Esc puts the original back.
type wheelSub struct {
	Open     bool
	Setting  views.SettingID
	Sel      int
	Original int
	opened   time.Time
	// labels are the first level's labels as they were when it opened.
	labels []string
	// dir is the way the submenu last opened, for its keys; anchor is the
	// item it grows from, kept for its closing.
	dir    views.FanDir
	anchor widget.Rect
	// roll is the fan rolling between its ends.
	roll *wheelRoll
}

func absDiff(a, b int) int {
	if a > b {
		return a - b
	}
	return b - a
}

// wheelState is the wheel while it is open. Items is taken when it opens, so the
// list cannot shift under the cursor while it is turning.
type wheelState struct {
	Open   bool
	Items  []wheelItem
	Sel    int
	Sub    wheelSub
	opened time.Time
	// anchor is where the selected thing was last drawn.
	anchor      widget.Rect
	anchorKnown bool
	// dir is the way the first level last opened, for its keys.
	dir views.FanDir
	// subClosing is a submenu folding back into its item.
	subClosing *subClosing
	// roll is the first level rolling between its ends.
	roll *wheelRoll
	// closing is the wheel as it was when it closed, drawn while its closing
	// animation runs. The keyboard is already back with the page by then.
	closing *wheelClosing
}

type wheelClosing struct {
	labels []string
	sel    int
	anchor widget.Rect
	at     time.Time
}

// wheelKey is the wheel key in effect.
func (a *App) wheelKey() string {
	for _, k := range protocol.WheelKeys {
		if k == a.cfg.WheelKey {
			return k
		}
	}
	return protocol.WheelKeys[0]
}

// wheelKeyMatches reports whether ev is the wheel key in effect.
func (a *App) wheelKeyMatches(ev *tcell.EventKey) bool {
	switch a.wheelKey() {
	case "ctrl_g":
		return ev.Key() == tcell.KeyCtrlG
	case "ctrl_k":
		return ev.Key() == tcell.KeyCtrlK
	case "ctrl_o":
		return ev.Key() == tcell.KeyCtrlO
	case "ctrl_space":
		// Ctrl+Space arrives as NUL on most terminals, or as a space with Ctrl.
		return ev.Key() == tcell.KeyCtrlSpace ||
			(ev.Key() == tcell.KeyRune && ev.Rune() == ' ' && ev.Modifiers()&tcell.ModCtrl != 0)
	}
	return ev.Key() == tcell.KeyF1
}

// handleWheelKey runs before every other key handler. It reports whether the key
// was the wheel's: always while the wheel is open, and the wheel key when it
// opens it.
func (a *App) handleWheelKey(ctx context.Context, ev *tcell.EventKey) bool {
	w := &a.ui.Wheel
	if !w.Open {
		if !a.wheelKeyMatches(ev) {
			return false
		}
		// On the settings page the wheel key is Enter: it goes in a level and
		// opens an item's candidates; pressed again it closes them unchanged
		// (spec tui-quick-wheel / 设置页的呼出键).
		if a.page() == client.PageSettings && a.st.Focus == client.FocusInput {
			k := tcell.KeyEnter
			if a.ui.SettingsNav.Level == views.LevelChoose {
				k = tcell.KeyEscape
			}
			a.handleSettingsPageKey(tcell.NewEventKey(k, 0, tcell.ModNone))
			return true
		}
		a.openWheel()
		return true
	}
	if w.Sub.Open {
		a.handleSubKey(ev)
		return true
	}
	if a.wheelKeyMatches(ev) {
		a.closeWheel()
		return true
	}
	// The arrows follow the fan: along its items they turn it. Along the line
	// they only move between levels: out opens a setting's submenu, back closes
	// the wheel. Actions run only on Enter.
	next, prev, out, back := views.FanKeys(w.dir)
	it := w.Items[w.Sel]
	switch k := ev.Key(); {
	case k == tcell.KeyEscape || k == back:
		a.closeWheel()
	case k == next:
		a.turnWheel(1)
	case k == prev:
		a.turnWheel(-1)
	case k == out && it.Setting != noSetting:
		a.openSub(it.Setting)
	case k == tcell.KeyEnter:
		if it.Setting != noSetting {
			a.openSub(it.Setting)
			return true
		}
		a.closeWheel()
		it.Run(a, ctx)
	}
	return true
}

// openSub opens a setting's candidates from the selected item.
func (a *App) openSub(id views.SettingID) {
	i := a.currentIndexOf(id)
	a.ui.Wheel.Sub = wheelSub{Open: true, Setting: id, Sel: i, Original: i, opened: nowTime(), labels: a.wheelLabels()}
}

// handleSubKey drives an open submenu: the arrows try each candidate on the
// page, Enter keeps it and folds back to the first level, Esc (or the wheel
// key) puts the original back.
func (a *App) handleSubKey(ev *tcell.EventKey) {
	sub := &a.ui.Wheel.Sub
	vals := a.choiceValues(sub.Setting)
	n := len(vals)
	if n == 0 {
		a.ui.Wheel.Sub = wheelSub{}
		return
	}
	try := func(i int) {
		// Past an end the list starts over from the other, rolling there.
		from := sub.Sel
		switch {
		case i >= n:
			i = 0
		case i < 0:
			i = n - 1
		}
		if absDiff(i, from) > 1 {
			sub.roll = startRoll(from, i, a.animated())
		} else {
			sub.roll = nil
		}
		sub.Sel = i
		if key := settingKey(sub.Setting); key != "" {
			a.previewSetting(key, vals[sub.Sel])
		}
	}
	next, prev, _, back := views.FanKeys(sub.dir)
	k := ev.Key()
	switch {
	case k == next:
		try(sub.Sel + 1)
	case k == prev:
		try(sub.Sel - 1)
	case k == tcell.KeyEnter:
		id, v := sub.Setting, vals[sub.Sel]
		if key := settingKey(id); key != "" && sub.Sel != sub.Original {
			// Put the original back first so the commit is a real change.
			a.previewSetting(key, vals[sub.Original])
		}
		if v != a.currentValue(id) {
			a.uploadSettings(a.applySetting(id, v))
			a.setStatus(i18n.SetSaved.Get(a.lang), false)
		}
		a.foldSub()
	case k == tcell.KeyEscape || k == back || a.wheelKeyMatches(ev):
		// Back without Enter: nothing is kept.
		if sub.Sel != sub.Original {
			try(sub.Original)
		}
		a.foldSub()
	}
}

// foldSub closes the submenu back into its item, playing the closing; then the
// first level's other items light up again from the middle.
func (a *App) foldSub() {
	w := &a.ui.Wheel
	if a.animated() && w.Sub.anchor.W > 0 {
		sub := w.Sub
		now := nowTime()
		w.subClosing = &subClosing{sub: sub, labels: a.choiceLabels(sub.Setting), at: now}
		// The first level's clock: past its line when the fold ends.
		w.opened = now.Add(views.FanFoldDuration() - fanSettle)
	}
	w.Sub = wheelSub{}
}

// subClosing is a submenu playing its closing after it was folded.
type subClosing struct {
	sub    wheelSub
	labels []string
	at     time.Time
}

func (a *App) subFolding(now time.Time) bool {
	w := &a.ui.Wheel
	if w.subClosing != nil && now.Sub(w.subClosing.at) < views.FanFoldDuration() {
		return true
	}
	w.subClosing = nil
	return false
}

// currentIndexOf is the position of setting id's value among its choices.
func (a *App) currentIndexOf(id views.SettingID) int {
	cur := a.currentValue(id)
	for i, v := range a.choiceValues(id) {
		if v == cur {
			return i
		}
	}
	return 0
}

// openWheel opens the wheel on what can be done now.
func (a *App) openWheel() {
	items := a.wheelItems()
	if len(items) == 0 {
		return
	}
	a.ui.Wheel = wheelState{Open: true, Items: items, opened: nowTime()}
}

// closeWheel gives the keyboard back at once. With animation on, the fan as it
// was keeps playing its closing over the page until it has gone.
func (a *App) closeWheel() {
	w := &a.ui.Wheel
	if w.Sub.Open && w.Sub.Sel != w.Sub.Original {
		if key := settingKey(w.Sub.Setting); key != "" {
			a.previewSetting(key, a.choiceValues(w.Sub.Setting)[w.Sub.Original])
		}
	}
	var cl *wheelClosing
	if w.Open && a.animated() && w.anchorKnown {
		cl = &wheelClosing{labels: a.wheelLabels(), sel: w.Sel, anchor: w.anchor, at: nowTime()}
	}
	a.ui.Wheel = wheelState{closing: cl}
}

// turnWheel moves the selection by d. The list has ends: past the last item it
// starts again from the first, and past the first from the last. That jump is
// shown as the fan rolling back through the items in between (wheelRoll), so it
// is clear the list has started over rather than wrapped round.
func (a *App) turnWheel(d int) {
	w := &a.ui.Wheel
	n := len(w.Items)
	to := w.Sel + d
	switch {
	case to >= n:
		to = 0
	case to < 0:
		to = n - 1
	default:
		w.Sel, w.roll = to, nil
		return
	}
	w.roll = startRoll(w.Sel, to, a.animated())
	w.Sel = to
}

// wheelRoll is the fan rolling from one end of its list to the other.
type wheelRoll struct {
	from, to int
	at       time.Time
}

// wheelRollStep is how long the roll shows each item it passes.
const wheelRollStep = 18 * time.Millisecond

func startRoll(from, to int, animated bool) *wheelRoll {
	if !animated || from == to {
		return nil
	}
	return &wheelRoll{from: from, to: to, at: nowTime()}
}

// shown is the item the roll is passing at now, and whether it is still going.
func (r *wheelRoll) shown(now time.Time) (int, bool) {
	if r == nil {
		return 0, false
	}
	steps := int(now.Sub(r.at) / wheelRollStep)
	span := r.to - r.from
	if span < 0 {
		span = -span
	}
	if steps >= span {
		return r.to, false
	}
	if r.to > r.from {
		return r.from + steps, true
	}
	return r.from - steps, true
}

func (a *App) wheelLabels() []string {
	out := make([]string, len(a.ui.Wheel.Items))
	for i, it := range a.ui.Wheel.Items {
		out[i] = it.Label.Get(a.lang)
		if it.Setting != noSetting {
			out[i] += " · " + a.settingText(it.Setting)
		}
	}
	return out
}

// wheelMoving reports whether the wheel needs frames: opening, open (for the
// band of light) or closing.
func (a *App) wheelMoving(now time.Time) bool {
	w := &a.ui.Wheel
	if w.Open {
		return true
	}
	w.subClosing = nil
	if w.closing != nil && now.Sub(w.closing.at) < views.FanCloseDuration() {
		return true
	}
	w.closing = nil
	return false
}

// renderWheel draws the wheel over the finished frame: it finds the selected
// thing on screen, dims everything else and fans the items out from it.
func (a *App) renderWheel(content widget.Rect) {
	now := nowTime()
	w := &a.ui.Wheel
	m := views.FanModel{Motion: a.animated(), Shimmer: a.animated(), Depth: a.animDepth()}
	switch {
	case w.Open:
		region := content
		if content.W >= layout.MinViewWidth {
			fold := a.foldNow(now)
			if a.st.Focus == client.FocusInput {
				region = views.MainRectAt(content, fold)
			} else {
				region = views.SidebarRectAt(content, fold)
			}
		}
		w.anchor, w.anchorKnown = a.wheelAnchor(region), true
		sel := w.Sel
		if s, ok := w.roll.shown(now); ok {
			sel = s
		} else {
			w.roll = nil
		}
		m.Labels, m.Sel, m.Anchor, m.Since = a.wheelLabels(), sel, w.anchor, now.Sub(w.opened)
	case a.wheelMoving(now):
		cl := w.closing
		m.Labels, m.Sel, m.Anchor, m.Since, m.Closing = cl.labels, cl.sel, cl.anchor, now.Sub(cl.at), true
	default:
		return
	}
	fan := views.FanWheel{T: a.tokens}
	folding := w.Open && a.subFolding(now)
	if !w.Open || (!w.Sub.Open && !folding) {
		dir := fan.Render(a.scr, content, m, a.lang)
		if w.Open {
			w.dir = dir
		}
		return
	}
	// A submenu is open, or folding back: the page shows the candidate plainly,
	// so nothing is dimmed; the first level keeps only its selected item and
	// line, and the candidates fan out from that item.
	sub, labels, since, closing := w.Sub, []string(nil), now.Sub(w.Sub.opened), false
	if !w.Sub.Open {
		sc := w.subClosing
		sub, labels, since, closing = sc.sub, sc.labels, now.Sub(sc.at), true
	} else {
		labels = a.choiceLabels(sub.Setting)
	}
	// The first level holds still with only its selected item while the
	// submenu is out; once the submenu has folded away it lights up again
	// (foldSub sets its clock so the items come back from the middle).
	m.Shimmer, m.Muted, m.Since = false, true, time.Hour
	m.Labels = sub.labels
	if closing {
		m.Labels = a.wheelLabels()
	}
	// Only the menu being chosen from is shaded, over the area it can take
	// whichever item is selected; the first level's remaining item and line
	// get the same shade under them, so they read the same way. The rest of the
	// page shows the candidate plainly.
	from, dir := fan.Draw(a.scr, content, m)
	if !closing {
		w.dir = dir
	}
	subSel := sub.Sel
	if s, ok := sub.roll.shown(now); ok && !closing {
		subSel = s
	} else if w.Sub.Open {
		w.Sub.roll = nil
	}
	sm := views.FanModel{
		Labels: labels, Sel: subSel, Anchor: from, Prefer: w.dir,
		Motion: a.animated(), Shimmer: a.animated() && !closing, Depth: a.animDepth(),
		Since: since, Closing: closing,
	}
	sm.Colors = a.choiceColors(sub.Setting)
	strength := 1.0
	if closing {
		// The shade lifts as the submenu folds away.
		strength = 1 - float64(since)/float64(views.FanFoldDuration())
	} else if a.animated() {
		strength = math.Min(1, float64(since)/float64(views.FanOpenDuration(len(labels))))
	}
	keep := []widget.Rect{w.anchor}
	fan.Shade(a.scr, append(fan.Cover(content, sm), fan.Trail(content, m)...), keep, strength)
	fan.Draw(a.scr, content, m)
	_, d := fan.Draw(a.scr, content, sm)
	if w.Sub.Open {
		w.Sub.dir, w.Sub.anchor = d, from
	}
}

// fanSettle is how far into its opening the first level is drawn as it comes
// back from a submenu: past the line, so only the items light up again.
const fanSettle = 140 * time.Millisecond

func isColorSetting(id views.SettingID) bool {
	switch id {
	case views.SetSelfText, views.SetOtherText, views.SetSelfBorder, views.SetOtherBorder:
		return true
	}
	return false
}

// wheelAnchor is the selected thing the fan grows from. In a conversation the
// keyboard is in the composer at the bottom, so the fan grows up from its box;
// elsewhere the selection is found on screen.
func (a *App) wheelAnchor(region widget.Rect) widget.Rect {
	if a.st.Focus == client.FocusInput && a.page() == client.PageConversation {
		inputLines := wrappedLineCount(a.st.InputText(), region.W-6)
		_, comp := views.ComposerRect(region, inputLines)
		// The box sits under one blank row.
		return widget.Rect{X: comp.X, Y: comp.Y + 1, W: comp.W, H: comp.H - 1}
	}
	return views.FindSelection(a.scr, region, a.tokens)
}

// --- the items ---
//
// The wheel starts from what is selected where the keyboard is: the actions on
// that thing first, then what the page can do, then what every page can do.
// Every action is shown. Most item actions are the page's own letter keys, fed
// back to its handler, so the wheel and the key do exactly the same thing,
// confirmations included.

func ready(a *App) bool { return a.st.State == client.StateReady }

// press sends the page a letter, as if typed.
func wheelPress(r rune) func(*App, context.Context) {
	return func(a *App, ctx context.Context) {
		a.handleRightKey(ctx, tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
	}
}

func sendKey(k tcell.Key) func(*App, context.Context) {
	return func(a *App, ctx context.Context) {
		a.handleRightKey(ctx, tcell.NewEventKey(k, 0, tcell.ModNone))
	}
}

func profileOf(name string) wheelItem {
	return wheelItem{Label: i18n.WheelViewProfile, Run: func(a *App, _ context.Context) { a.openProfile(name) }}
}

var (
	wiHelp = wheelItem{Label: i18n.WheelHelp, Run: func(a *App, _ context.Context) { a.showMeSubpage(client.PageHelp) }}
	wiBack = wheelItem{Label: i18n.WheelBackToList, Run: func(a *App, _ context.Context) { a.moveFocus(client.FocusList) }}
	wiToMe = wheelItem{Label: i18n.WheelBackToMe, Run: func(a *App, _ context.Context) { a.backToMe() }}
)

// setting is a first-level item that opens a setting's candidates.
func setting(id views.SettingID, l i18n.T) wheelItem {
	return wheelItem{Label: l, Setting: id}
}

// listItems are what the conversation list itself can do.
func listItems() []wheelItem {
	return []wheelItem{
		{Label: i18n.WheelNewDM, Show: ready, Run: func(a *App, _ context.Context) { a.openNewPage(client.SectionDM) }},
		{Label: i18n.WheelNewChannel, Show: ready, Run: func(a *App, _ context.Context) { a.openNewPage(client.SectionChannel) }},
		{Label: i18n.WheelFilter, Run: func(a *App, _ context.Context) {
			a.st.Filter = []rune{}
			a.ui.Selected = 0
		}},
	}
}

// homeItems are the application-wide ones, offered only on the foot entries of
// the home list (好友, 发现, 我) (spec tui-quick-wheel / 操作清单跟着选中的条目).
func homeItems() []wheelItem {
	return []wheelItem{
		setting(views.SetTheme, i18n.SetTheme),
		setting(views.SetMotion, i18n.SetMotion),
		setting(views.SetLanguage, i18n.WheelLanguage),
		{Label: i18n.WheelSettings, Run: func(a *App, _ context.Context) { a.openSettings() }},
		wiHelp,
		{Label: i18n.WheelQuit, Run: func(a *App, _ context.Context) { a.ui.Quitting = true }},
	}
}

// chatSettings are the settings that change how a conversation looks.
func chatSettings() []wheelItem {
	return []wheelItem{
		setting(views.SetBubbleWidth, i18n.SetBubbleWidth),
		setting(views.SetDensity, i18n.SetDensity),
		setting(views.SetTimeFormat, i18n.SetTimeFormat),
		setting(views.SetMsgBorder, i18n.SetMsgBorder),
		setting(views.SetMsgTime, i18n.SetMsgTime),
		setting(views.SetSelfText, i18n.SetSelfText),
		setting(views.SetOtherText, i18n.SetOtherText),
		setting(views.SetSelfBorder, i18n.SetSelfBorder),
		setting(views.SetOtherBorder, i18n.SetOtherBorder),
	}
}

// isFriend reports whether name is a friend.
func (a *App) isFriend(name string) bool {
	for _, u := range a.ui.Moments.Model.Friends {
		if u.Username == name {
			return true
		}
	}
	return false
}

// listRowItems are the actions on the sidebar row under the cursor.
func (a *App) listRowItems() []wheelItem {
	rows := a.st.SidebarRows()
	if a.ui.Selected < 0 || a.ui.Selected >= len(rows) {
		return nil
	}
	row := rows[a.ui.Selected]
	enter := func(a *App, _ context.Context) { a.enterRight() }
	switch row.Kind {
	case client.RowConversation:
		cv := row.Conversation
		if cv.Kind != protocol.KindDM {
			return []wheelItem{{Label: i18n.WheelEnter, Run: enter}}
		}
		name := cv.Name
		out := []wheelItem{{Label: i18n.WheelMessage, Run: enter}, profileOf(name)}
		if a.isFriend(name) {
			out = append(out,
				wheelItem{Label: i18n.WheelRemoveFriend, Run: func(a *App, ctx context.Context) { a.friendAction(ctx, name, 'd') }},
				wheelItem{Label: i18n.WheelBlock, Run: func(a *App, ctx context.Context) { a.friendAction(ctx, name, 'b') }})
		}
		return out
	case client.RowEntry:
		return append([]wheelItem{{Label: i18n.WheelOpen, Run: enter}}, homeItems()...)
	case client.RowNew:
		return []wheelItem{{Label: i18n.WheelOpen, Run: enter}}
	}
	return nil
}

// friendAction does a contacts key to the friend called name: it opens the
// friends page on that friend and presses the key there, so the same question is
// asked as from the page itself.
func (a *App) friendAction(ctx context.Context, name string, r rune) {
	for i, u := range a.ui.Moments.Model.Friends {
		if u.Username == name {
			a.showEntryPage(client.EntryFriends)
			a.switchFriendsTab(views.TabContacts)
			a.ui.Friends.Selected[views.TabContacts] = i
			a.scrollFriends()
			wheelPress(r)(a, ctx)
			return
		}
	}
}

// friendsRowItems are the actions on the friends page's selected row.
func (a *App) friendsRowItems() []wheelItem {
	f := &a.ui.Friends
	m := a.friendsModel(true, false)
	switch f.Tab {
	case views.TabRequests:
		i := f.Selected[views.TabRequests]
		if i < 0 || i >= len(m.Requests) {
			return nil
		}
		return []wheelItem{
			{Label: i18n.WheelAccept, Run: wheelPress('a')},
			{Label: i18n.WheelDecline, Run: wheelPress('x')},
			profileOf(m.Requests[i].From.Username),
		}
	case views.TabPlaza:
		sel := f.Selected[views.TabPlaza]
		if sel == 0 {
			return []wheelItem{{Label: i18n.WheelSendRequest, Show: func(a *App) bool { return len(a.ui.Friends.Name) > 0 }, Run: sendKey(tcell.KeyEnter)}}
		}
		if i := sel - 1; i < len(m.Plaza) {
			return []wheelItem{profileOf(m.Plaza[i].Username)}
		}
		return nil
	}
	row := views.ContactRowAt(m, f.Selected[views.TabContacts])
	switch {
	case row.Friend >= 0:
		return []wheelItem{
			{Label: i18n.WheelMessage, Run: wheelPress('m')},
			profileOf(m.Friends[row.Friend].Username),
			{Label: i18n.WheelRemoveFriend, Run: wheelPress('d')},
			{Label: i18n.WheelBlock, Run: wheelPress('b')},
		}
	case row.Removed >= 0:
		return []wheelItem{
			profileOf(m.RemovedBy[row.Removed].Username),
			{Label: i18n.WheelRemoveFriend, Run: wheelPress('d')},
			{Label: i18n.WheelBlock, Run: wheelPress('b')},
		}
	case row.Blocked >= 0:
		return []wheelItem{{Label: i18n.WheelUnblock, Run: wheelPress('u')}}
	}
	return nil
}

func friendsPageItems() []wheelItem {
	tab := func(t views.FriendsTab, l i18n.T) wheelItem {
		return wheelItem{Label: l, Show: func(a *App) bool { return a.ui.Friends.Tab != t }, Run: func(a *App, _ context.Context) { a.switchFriendsTab(t) }}
	}
	return []wheelItem{
		{Label: i18n.WheelAddFriend, Run: func(a *App, _ context.Context) {
			a.switchFriendsTab(views.TabPlaza)
			a.ui.Friends.Selected[views.TabPlaza] = 0
			a.scrollFriends()
		}},
		tab(views.TabContacts, i18n.WheelContacts),
		tab(views.TabRequests, i18n.WheelRequests),
		tab(views.TabPlaza, i18n.WheelPlaza),
		wiBack,
	}
}

// momentsRowItems are the actions on the selected moment, its selected comment,
// or the selected friend request among the notices.
func (a *App) momentsRowItems() []wheelItem {
	m := &a.ui.Moments.Model
	if m.Unsupported {
		return nil
	}
	if m.Tab == views.TabNotices {
		if req, _ := views.NoticeTarget(*m, m.Selected[views.TabNotices]); req >= 0 {
			return []wheelItem{
				{Label: i18n.WheelAccept, Run: wheelPress('a')},
				{Label: i18n.WheelDecline, Run: wheelPress('x')},
				profileOf(m.Requests[req].From.Username),
			}
		}
		return nil
	}
	i := TimelineSelected(m)
	if i < 0 {
		return nil
	}
	mo := m.Moments[i]
	me := a.st.UserID
	if cm := selectedComment(m, i); cm != nil {
		out := []wheelItem{{Label: i18n.WheelReply, Run: wheelPress('r')}}
		if cm.Author.ID == me || mo.Author.ID == me {
			out = append(out, wheelItem{Label: i18n.WheelDeleteComment, Run: wheelPress('d')})
		}
		return append(out, profileOf(cm.Author.Username))
	}
	like := i18n.WheelLike
	if mo.Liked {
		like = i18n.WheelUnlike
	}
	out := []wheelItem{{Label: like, Run: wheelPress('l')}, {Label: i18n.WheelComment, Run: wheelPress('c')}}
	if mo.Author.ID == me {
		out = append(out, wheelItem{Label: i18n.WheelDelete, Run: wheelPress('d')})
	}
	author := mo.Author.Username
	return append(out, wheelItem{Label: i18n.WheelAuthor, Run: func(a *App, _ context.Context) { a.openProfile(author) }})
}

func momentsPageItems() []wheelItem {
	return []wheelItem{
		{Label: i18n.WheelPost, Show: func(a *App) bool { return !a.ui.Moments.Model.Unsupported }, Run: func(a *App, _ context.Context) {
			a.switchMomentsTab(views.TabTimeline)
			a.ui.Moments.Model.Input = views.MomentsInput{Kind: views.InputPost}
		}},
		{Label: i18n.WheelNextTab, Run: func(a *App, _ context.Context) { a.switchMomentsTab(a.ui.Moments.Model.Tab.Next()) }},
		{Label: i18n.WheelMyProfile, Run: func(a *App, _ context.Context) { a.openProfile(a.st.Username) }},
		wiBack,
	}
}

func profileItems() []wheelItem {
	p := func(a *App) *protocol.Profile { return a.ui.Moments.Profile.Profile }
	btn := func(b views.ProfileButton) func(*App, context.Context) {
		return func(a *App, ctx context.Context) { a.pressProfileButton(ctx, b) }
	}
	return []wheelItem{
		{Label: i18n.WheelMessage, Show: func(a *App) bool { pr := p(a); return pr != nil && pr.IsFriend }, Run: btn(views.BtnMessage)},
		{Label: i18n.WheelAddFriend, Show: func(a *App) bool { pr := p(a); return pr != nil && !pr.IsSelf && !pr.IsFriend }, Run: btn(views.BtnAdd)},
		{Label: i18n.WheelEditProfile, Show: func(a *App) bool { pr := p(a); return pr != nil && pr.IsSelf }, Run: func(a *App, _ context.Context) { a.startOwnEdit() }},
		{Label: i18n.WheelBlock, Show: func(a *App) bool { pr := p(a); return pr != nil && !pr.IsSelf }, Run: btn(views.BtnBlock)},
		{Label: i18n.WheelBack, Run: func(a *App, _ context.Context) { a.leaveProfile() }},
	}
}

func (a *App) meRowItems() []wheelItem {
	it := views.MeItems[clamp(a.ui.Me.Selected, 0, len(views.MeItems)-1)]
	return []wheelItem{{Label: i18n.WheelOpen, Run: func(a *App, _ context.Context) { a.openMeItem(it) }}}
}

func composerItems() []wheelItem {
	return []wheelItem{
		{Label: i18n.WheelLatest, Show: func(a *App) bool { return a.ui.Scroll > 0 || a.st.PendingNewMessage }, Run: func(a *App, _ context.Context) {
			a.ui.Scroll = 0
			a.st.PendingNewMessage = false
		}},
		wiBack,
	}
}

// wheelItems is what the wheel offers now: the selected thing's actions, then
// the page's, then the general ones, each once, those that cannot be done left
// off (spec tui-quick-wheel / 操作清单跟着选中的条目).
func (a *App) wheelItems() []wheelItem {
	var all []wheelItem
	if a.st.Focus != client.FocusInput {
		all = append(a.listRowItems(), listItems()...)
	} else {
		switch a.page() {
		case client.PageFriends:
			all = append(a.friendsRowItems(), friendsPageItems()...)
			all = append(all, setting(views.SetPlaza, i18n.SetPlaza))
		case client.PageMoments:
			all = append(a.momentsRowItems(), momentsPageItems()...)
		case client.PageProfile:
			all = profileItems()
		case client.PageMe:
			all = append(a.meRowItems(),
				setting(views.SetWheelKey, i18n.SetWheelKey),
				setting(views.SetRemember, i18n.SetRemember),
				wiBack)
		case client.PageSettings, client.PageHelp, client.PageAbout:
			all = []wheelItem{wiToMe}
		case client.PageNewDM, client.PageNewChannel:
			all = []wheelItem{wiBack}
		case client.PageConversation:
			all = append(composerItems(), chatSettings()...)
		default:
			all = composerItems()
		}
	}
	var out []wheelItem
	seen := map[i18n.T]bool{}
	for _, it := range all {
		if seen[it.Label] || (it.Show != nil && !it.Show(a)) {
			continue
		}
		seen[it.Label] = true
		out = append(out, it)
	}
	return out
}

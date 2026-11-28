package server

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"

	"link-chat/pkg/protocol"
	"link-chat/pkg/storage"
	"link-chat/pkg/version"
)

type ActivityCategory string

const (
	ActivityMessage ActivityCategory = "消息"
	ActivitySocial  ActivityCategory = "社交"
	ActivityAccount ActivityCategory = "账号"
	ActivitySystem  ActivityCategory = "系统"
)

func ActivityCategories() [4]ActivityCategory {
	return [4]ActivityCategory{ActivityMessage, ActivitySocial, ActivityAccount, ActivitySystem}
}

type ActivityUser struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Activity struct {
	Sequence    uint64           `json:"sequence"`
	At          int64            `json:"at"`
	Kind        string           `json:"kind"`
	Category    ActivityCategory `json:"category"`
	Actor       ActivityUser     `json:"actor"`
	Target      *ActivityUser    `json:"target,omitempty"`
	Channel     *LiveChannel     `json:"channel,omitempty"`
	Summary     string           `json:"summary"`
	Content     string           `json:"content,omitempty"`
	MessageBand int              `json:"message_band,omitempty"`
}

type LiveUser struct {
	ActivityUser
	Online         bool   `json:"online"`
	OnlineSince    int64  `json:"online_since,omitempty"`
	ViewingChannel string `json:"viewing_channel,omitempty"`
	LastActive     int64  `json:"last_active"`
	LastSequence   uint64 `json:"last_sequence,omitempty"`
	Summary        string `json:"summary,omitempty"`
}

type LiveChannel struct {
	ID          string   `json:"id"`
	Name        string   `json:"name,omitempty"`
	Members     []string `json:"members,omitempty"`
	MemberCount int      `json:"member_count,omitempty"`
	OnlineCount int      `json:"online_count,omitempty"`
}

type LiveStats struct {
	Version        string               `json:"version"`
	StartedAt      int64                `json:"started_at"`
	MessagesToday  int64                `json:"messages_today"`
	MessagesMinute int64                `json:"messages_minute"`
	Trend          []LiveActivityBucket `json:"trend"`
	ActivityTrend  LiveTrend            `json:"activity_trend"`
}

type LiveSnapshot struct {
	Sequence      uint64                             `json:"sequence"`
	Users         []LiveUser                         `json:"users"`
	Channels      []LiveChannel                      `json:"channels"`
	Stats         LiveStats                          `json:"stats"`
	Activity      map[ActivityCategory]ActivityPanel `json:"activity"`
	ChatDetails   bool                               `json:"chat_details,omitempty"`
	FocusSequence uint64                             `json:"focus_sequence,omitempty"`
}

const ActivityPreviewLimit = 6

type ActivityEntry struct {
	At       int64  `json:"at"`
	Summary  string `json:"summary"`
	ActorID  string `json:"-"`
	TargetID string `json:"-"`
}

type ActivityPanel struct {
	Total  uint64          `json:"total"`
	Recent []ActivityEntry `json:"recent"`
}

func (p ActivityPanel) Add(entry ActivityEntry) ActivityPanel {
	p.Total++
	if len(p.Recent) == ActivityPreviewLimit {
		copy(p.Recent, p.Recent[1:])
		p.Recent = p.Recent[:ActivityPreviewLimit-1]
	}
	p.Recent = append(p.Recent, entry)
	return p
}

func (p ActivityPanel) WithoutUser(userID string) ActivityPanel {
	p.Recent = slices.DeleteFunc(p.Recent, func(entry ActivityEntry) bool {
		return entry.ActorID == userID || entry.TargetID == userID
	})
	return p
}

type recentActivity struct {
	targetID          string
	at                int64
	sequence          uint64
	summary, redacted string
}

type activityState struct {
	mu            sync.Mutex
	sequence      uint64
	recent        map[string]recentActivity
	subs          map[chan Activity]struct{}
	panels        map[ActivityCategory]ActivityPanel
	redacted      map[ActivityCategory]ActivityPanel
	onlineSince   map[string]int64
	viewed        map[string]channelView
	focusSequence uint64
	trend         [maxLiveTrendMinutes * liveTrendBuckets]liveTrendSlot
}

// SubscribeActivity registers a local, content-free subscription for the
// lifetime of ctx. Subscribe before reading the snapshot to avoid a gap.
func (s *Server) SubscribeActivity(ctx context.Context) <-chan Activity {
	ch := make(chan Activity, 256)
	s.live.mu.Lock()
	if s.live.subs == nil {
		s.live.subs = make(map[chan Activity]struct{})
	}
	s.live.subs[ch] = struct{}{}
	s.live.mu.Unlock()
	go func() {
		select {
		case <-ctx.Done():
		case <-s.shuttingDown:
		}
		s.live.mu.Lock()
		delete(s.live.subs, ch)
		close(ch)
		s.live.mu.Unlock()
	}()
	return ch
}

func (s *Server) activity(kind string, category ActivityCategory, actorID, targetID string, cv storage.Conversation, content string) {
	actor := ActivityUser{ID: actorID, Name: "系统"}
	if actorID != "" {
		actor.Name = s.userName(actorID)
	}
	a := Activity{
		Kind: kind, Category: category,
		Actor: actor,
	}
	if targetID != "" {
		a.Target = &ActivityUser{ID: targetID, Name: s.userName(targetID)}
	}
	if cv.Kind == protocol.KindChannel {
		a.Channel = &LiveChannel{ID: cv.ID, Name: cv.Name}
	}
	s.publishActivity(a, content)
}

func (s *Server) publishActivity(a Activity, content string) {
	a.Summary = activitySummary(a)
	public := a
	if a.Kind == "message" && content != "" {
		a.MessageBand = messageLengthBand(content)
	}
	if a.Channel != nil {
		public.Channel = &LiveChannel{ID: a.Channel.ID}
		public.Summary = activitySummary(public)
	}

	s.live.mu.Lock()
	defer s.live.mu.Unlock()
	s.live.sequence++
	a.At, a.Sequence = storage.Now(), s.live.sequence
	s.live.countTrend(a)
	public.At, public.Sequence = a.At, a.Sequence
	if s.live.recent == nil {
		s.live.recent = make(map[string]recentActivity)
	}
	targetID := ""
	if a.Target != nil {
		targetID = a.Target.ID
	}
	if a.Kind == "account.deleted" {
		delete(s.live.recent, a.Actor.ID)
		for id, entry := range s.live.recent {
			if entry.targetID == a.Actor.ID {
				delete(s.live.recent, id)
			}
		}
		for k, panel := range s.live.panels {
			s.live.panels[k] = panel.WithoutUser(a.Actor.ID)
		}
		for k, panel := range s.live.redacted {
			s.live.redacted[k] = panel.WithoutUser(a.Actor.ID)
		}
	} else if a.Actor.ID != "" {
		s.live.recent[a.Actor.ID] = recentActivity{targetID, a.At, a.Sequence, a.Summary, public.Summary}
	}
	if s.live.panels == nil {
		s.live.panels = make(map[ActivityCategory]ActivityPanel)
		s.live.redacted = make(map[ActivityCategory]ActivityPanel)
		s.live.onlineSince = make(map[string]int64)
	}
	entry := ActivityEntry{At: a.At, Summary: a.Summary, ActorID: a.Actor.ID, TargetID: targetID}
	s.live.panels[a.Category] = s.live.panels[a.Category].Add(entry)
	entry.Summary = public.Summary
	s.live.redacted[a.Category] = s.live.redacted[a.Category].Add(entry)
	switch a.Kind {
	case "online", "login":
		s.live.onlineSince[a.Actor.ID] = a.At
	case "offline", "account.deleted":
		delete(s.live.onlineSince, a.Actor.ID)
	}
	for ch := range s.live.subs {
		select {
		case ch <- a:
		default:
			select {
			case <-ch:
			default:
			}
			ch <- a
		}
	}
	a.Content = content
	s.adminEmit(adminEvent{Type: "activity", Required: PermStatsView, Perm: PermChatView, Data: a, Redacted: public})
}

func messageLengthBand(body string) int {
	length := utf8.RuneCountInString(body)
	for i, limit := range []int{16, 40, 72, 100} {
		if length <= limit {
			return i + 1
		}
	}
	return 5
}

func activitySummary(a Activity) string {
	name := a.Actor.Name
	peer := ""
	if a.Target != nil {
		peer = a.Target.Name
	}
	channel := "频道"
	if a.Channel != nil && a.Channel.Name != "" {
		channel = "#" + a.Channel.Name
	}
	switch a.Kind {
	case "message":
		if a.Channel != nil {
			peer = channel
		}
		return name + " → " + peer
	case "message.edited":
		return name + " 编辑了消息"
	case "message.deleted":
		return name + " 删除了消息"
	case "online":
		return name + " 上线"
	case "offline":
		return name + " 下线"
	case "registered":
		return name + " 注册"
	case "account.deleted":
		return name + " 的账号已删除"
	case "login":
		return name + " 登录"
	case "friend.requested":
		return name + " 向 " + peer + " 申请好友"
	case "friend.accepted":
		return name + " 与 " + peer + " 成为好友"
	case "friend.removed":
		return name + " 删除好友 " + peer
	case "friend.blocked":
		return name + " 拉黑了 " + peer
	case "moment.posted":
		return name + " 发布了动态"
	case "moment.liked":
		return name + " 赞了 " + peer + " 的动态"
	case "moment.commented":
		return name + " 评论了 " + peer + " 的动态"
	case "moment.deleted":
		if peer != "" {
			return name + " 删除了 " + peer + " 的动态"
		}
		return name + " 删除了动态"
	case "channel.created":
		return name + " 新建了 " + channel
	case "channel.joined":
		return name + " 加入了 " + channel
	case "channel.left":
		return name + " 退出了 " + channel
	case "channel.deleted":
		return name + " 删除了 " + channel
	case "sanction.ban":
		return name + " 封禁了 " + peer
	case "sanction.mute":
		return name + " 禁言了 " + peer
	case "sanction.revoked":
		return name + " 解除对 " + peer + " 的处分"
	case "sanction.expired.ban":
		return peer + " 的封禁已到期"
	case "sanction.expired.mute":
		return peer + " 的禁言已到期"
	}
	return name
}

// The chat protocol handles expiry when checking a sanction. This watcher
// observes time passing so the live views see it even without a chat request.
func (s *Server) watchSanctionExpirations(ctx context.Context) {
	through := s.startedAt.UnixMilli()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-tick.C:
			until := now.UnixMilli()
			if until <= through {
				continue
			}
			expired, err := s.st.ExpiredSanctions(through, until)
			if err != nil {
				s.log.Warn("read sanction expirations", "err", err)
				continue
			}
			for _, sc := range expired {
				s.activity("sanction.expired."+sc.Kind, ActivitySystem, "", sc.UserID, storage.Conversation{}, "")
			}
			through = until
		}
	}
}

func (s *Server) LiveStats() (LiveStats, error) {
	return s.liveStats(1, 0)
}

func (s *Server) liveStats(minutes int, since int64) (LiveStats, error) {
	now := time.Now()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	today, minute, err := s.st.LiveMessageCounts(midnight.UnixMilli(), now.Add(-time.Minute).UnixMilli())
	coarse, detail := s.live.liveTrend(now.UnixMilli(), minutes, since)
	return LiveStats{Version: version.String(), StartedAt: s.startedAt.UnixMilli(),
		MessagesToday: today, MessagesMinute: minute, Trend: coarse, ActivityTrend: detail}, err
}

// LiveSnapshot returns the initial state for the local dashboard.
func (s *Server) LiveSnapshot() (LiveSnapshot, error) {
	return s.liveSnapshot(true, false)
}

// DashboardConnections returns signed-in connections for the local terminal.
func (s *Server) DashboardConnections() []OnlineConn {
	return s.hub.stats().Online
}

// DashboardChannelCounts reads membership only for channels visible on demand.
func (s *Server) DashboardChannelCounts(ids []string) (map[string]LiveChannel, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	online := s.hub.onlineUsers()
	counts := make(map[string]LiveChannel, len(ids))
	if len(ids) > 64 {
		for _, id := range ids {
			counts[id] = LiveChannel{ID: id}
		}
		members, err := s.st.ListChannelMembers()
		if err != nil {
			return nil, err
		}
		for _, member := range members {
			if channel, tracked := counts[member.ConversationID]; tracked {
				channel.MemberCount++
				if online[member.UserID] {
					channel.OnlineCount++
				}
				counts[member.ConversationID] = channel
			}
		}
		return counts, nil
	}
	for _, id := range ids {
		members, err := s.st.ListMembers(id)
		if err != nil {
			return nil, err
		}
		channel := LiveChannel{ID: id, MemberCount: len(members)}
		for _, member := range members {
			if online[member.UserID] {
				channel.OnlineCount++
			}
		}
		counts[id] = channel
	}
	return counts, nil
}

func (s *Server) liveSnapshot(chat, web bool) (LiveSnapshot, error) {
	s.live.mu.Lock()
	sequence := s.live.sequence
	recent := make(map[string]recentActivity, len(s.live.recent))
	for id, last := range s.live.recent {
		recent[id] = last
	}
	panels := s.live.redacted
	if chat {
		panels = s.live.panels
	}
	activity := make(map[ActivityCategory]ActivityPanel, len(ActivityCategories()))
	for _, category := range ActivityCategories() {
		panel := panels[category]
		panel.Recent = slices.Clone(panel.Recent)
		if panel.Recent == nil {
			panel.Recent = []ActivityEntry{}
		}
		activity[category] = panel
	}
	login := make(map[string]int64, len(s.live.onlineSince))
	for id, at := range s.live.onlineSince {
		login[id] = at
	}
	viewing := make(map[string]string)
	var focusSequence uint64
	if chat && web {
		focusSequence = s.live.focusSequence
		for id, view := range s.live.viewed {
			viewing[id] = view.id
		}
	}
	s.live.mu.Unlock()

	users, err := s.st.ListUsers()
	if err != nil {
		return LiveSnapshot{}, err
	}
	online := make(map[string]int64)
	for _, conn := range s.hub.stats().Online {
		online[conn.UserID] = max(online[conn.UserID], conn.Since)
	}
	snap := LiveSnapshot{
		Sequence: sequence, Users: make([]LiveUser, 0, len(users)), Channels: []LiveChannel{}, Activity: activity,
		ChatDetails: web && chat, FocusSequence: focusSequence,
	}
	for _, user := range users {
		actor := ActivityUser{user.ID, user.Username}
		connected, present := online[user.ID]
		u := LiveUser{
			ActivityUser: actor, Online: present, LastActive: user.CreatedAt,
			Summary: activitySummary(Activity{Kind: "registered", Actor: actor}),
		}
		if present {
			u.OnlineSince = max(login[user.ID], connected)
			u.ViewingChannel = viewing[user.ID]
		}
		if last, ok := recent[user.ID]; ok {
			u.LastActive, u.LastSequence, u.Summary = last.at, last.sequence, last.redacted
			if chat {
				u.Summary = last.summary
			}
		}
		snap.Users = append(snap.Users, u)
	}

	convs, err := s.st.ListConversations()
	if err != nil {
		return LiveSnapshot{}, err
	}
	var channels map[string]LiveChannel
	if web {
		members, err := s.st.ListChannelMembers()
		if err != nil {
			return LiveSnapshot{}, err
		}
		channels = make(map[string]LiveChannel)
		for _, member := range members {
			ch := channels[member.ConversationID]
			ch.MemberCount++
			if _, present := online[member.UserID]; present {
				ch.OnlineCount++
			}
			if chat {
				ch.Members = append(ch.Members, member.UserID)
			}
			channels[member.ConversationID] = ch
		}
	}
	for _, cv := range convs {
		if cv.Kind != protocol.KindChannel {
			continue
		}
		ch := channels[cv.ID]
		ch.ID = cv.ID
		if chat {
			ch.Name = cv.Name
		}
		snap.Channels = append(snap.Channels, ch)
	}
	snap.Stats, err = s.LiveStats()
	return snap, err
}

func (s *Server) apiLiveSnapshot(w http.ResponseWriter, _ *http.Request, a adminSession) {
	chat := a.perms[PermChatView]
	snap, err := s.liveSnapshot(chat, true)
	if err != nil {
		s.writeStorageErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func (s *Server) apiLiveStats(w http.ResponseWriter, r *http.Request, _ adminSession) {
	minutes := 1
	if raw := r.URL.Query().Get("minutes"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > maxLiveTrendMinutes {
			writeErr(w, http.StatusBadRequest, protocol.CodeInvalidArgument)
			return
		}
		minutes = value
	}
	var since int64
	if raw := r.URL.Query().Get("since"); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value < 0 || value%liveTrendStep != 0 {
			writeErr(w, http.StatusBadRequest, protocol.CodeInvalidArgument)
			return
		}
		since = value
	}
	if started := r.URL.Query().Get("started_at"); started != "" && started != strconv.FormatInt(s.startedAt.UnixMilli(), 10) {
		since = 0
	}
	stats, err := s.liveStats(minutes, since)
	if err != nil {
		s.writeStorageErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

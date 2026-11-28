package server

import "time"

const (
	liveTrendBuckets    = 60 // One-second slots per minute.
	liveTrendStep       = int64(time.Second / time.Millisecond)
	maxLiveTrendMinutes = 60
)

type LiveActivityBucket struct {
	At     int64     `json:"at"`
	Counts [4]uint64 `json:"counts"` // ActivityCategories order.
}

const (
	trendDM = iota
	trendChannelMessage
	trendMessageChanges
	trendFriendRequest
	trendFriendAccept
	trendFriendRemove
	trendChannelLifecycle
	trendChannelMembership
	trendChannelSwitch
	trendMoment
	trendComment
	trendLike
	trendRegistered
	trendOnline
	trendOffline
	trendAccountChanges
	trendOther
	trendSeriesCount
)

type LiveTrendSeries struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

var liveTrendSeries = [trendSeriesCount]LiveTrendSeries{
	{"dm", "私聊消息"}, {"channel_message", "频道消息"}, {"message_changes", "编辑/删除消息"},
	{"friend_request", "好友申请"}, {"friend_accept", "新增好友"}, {"friend_remove", "删除好友/拉黑"},
	{"channel_lifecycle", "创建/销毁频道"}, {"channel_membership", "加入/退出频道"}, {"channel_switch", "切换频道"},
	{"moment", "发布/删除动态"}, {"comment", "评论"}, {"like", "点赞"},
	{"registered", "注册"}, {"online", "上线"}, {"offline", "下线"}, {"account_changes", "登录/删号"},
	{"system", "系统及其他"},
}

type LiveTrendSample struct {
	At     int64                    `json:"at"`
	Counts [trendSeriesCount]uint64 `json:"counts"`
}

type LiveTrend struct {
	Minutes       int                               `json:"minutes"`
	BucketSeconds int                               `json:"bucket_seconds"`
	StartAt       int64                             `json:"start_at"`
	Series        [trendSeriesCount]LiveTrendSeries `json:"series"`
	Samples       []LiveTrendSample                 `json:"samples"`
}

type liveTrendSlot struct {
	coarse LiveActivityBucket
	detail [trendSeriesCount]uint64
}

func trendSeriesFor(a Activity) int {
	switch a.Kind {
	case "message":
		if a.Channel != nil {
			return trendChannelMessage
		}
		return trendDM
	case "message.edited", "message.deleted":
		return trendMessageChanges
	case "friend.requested":
		return trendFriendRequest
	case "friend.accepted":
		return trendFriendAccept
	case "friend.removed", "friend.blocked":
		return trendFriendRemove
	case "channel.created", "channel.deleted":
		return trendChannelLifecycle
	case "channel.joined", "channel.left":
		return trendChannelMembership
	case "channel.focus":
		return trendChannelSwitch
	case "moment.posted", "moment.deleted":
		return trendMoment
	case "moment.commented":
		return trendComment
	case "moment.liked":
		return trendLike
	case "registered":
		return trendRegistered
	case "online":
		return trendOnline
	case "offline":
		return trendOffline
	case "login", "account.deleted":
		return trendAccountChanges
	default:
		return trendOther
	}
}

// countTrend is called under the activity-state lock.
func (state *activityState) countTrend(a Activity) {
	step := a.At / liveTrendStep
	slot := &state.trend[step%int64(len(state.trend))]
	if slot.coarse.At != step*liveTrendStep {
		*slot = liveTrendSlot{coarse: LiveActivityBucket{At: step * liveTrendStep}}
	}
	for i, kind := range ActivityCategories() {
		if a.Category == kind {
			slot.coarse.Counts[i]++
			break
		}
	}
	slot.detail[trendSeriesFor(a)]++
}

func (state *activityState) liveTrend(now int64, minutes int, since int64) ([]LiveActivityBucket, LiveTrend) {
	state.mu.Lock()
	defer state.mu.Unlock()
	last := now / liveTrendStep
	coarse := make([]LiveActivityBucket, liveTrendBuckets)
	for i := range coarse {
		step := last - liveTrendBuckets + 1 + int64(i)
		coarse[i].At = step * liveTrendStep
		if step >= 0 {
			if slot := state.trend[step%int64(len(state.trend))]; slot.coarse.At == coarse[i].At {
				coarse[i].Counts = slot.coarse.Counts
			}
		}
	}
	first := last - int64(minutes*liveTrendBuckets) + 1
	start := first
	// Include the last bucket the browser saw so its partial count can be replaced.
	if second := since / liveTrendStep; since > 0 && second > first && second <= last {
		start = second
	}
	samples := make([]LiveTrendSample, last-start+1)
	for i := range samples {
		sample := &samples[i]
		step := start + int64(i)
		sample.At = step * liveTrendStep
		if step >= 0 {
			if slot := &state.trend[step%int64(len(state.trend))]; slot.coarse.At == sample.At {
				sample.Counts = slot.detail
			}
		}
	}
	return coarse, LiveTrend{Minutes: minutes, BucketSeconds: 1, StartAt: first * liveTrendStep,
		Series: liveTrendSeries, Samples: samples}
}

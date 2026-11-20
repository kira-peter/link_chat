package server

import (
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"link-chat/pkg/storage"
	"link-chat/pkg/version"
)

// msgStats counts messages sent since start and per hour for the last 24
// hours, for the admin overview (design.md decision 8).
type msgStats struct {
	total atomic.Int64

	mu      sync.Mutex
	buckets [24]int64
	hours   [24]int64 // the hour (unix hours) each bucket currently holds
}

func (m *msgStats) record(now time.Time) {
	m.total.Add(1)
	h := now.Unix() / 3600
	i := h % 24
	m.mu.Lock()
	if m.hours[i] != h {
		m.hours[i], m.buckets[i] = h, 0
	}
	m.buckets[i]++
	m.mu.Unlock()
}

// last24h sums the buckets for the hours within the past day.
func (m *msgStats) last24h(now time.Time) int64 {
	h := now.Unix() / 3600
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for i := range m.buckets {
		if h-m.hours[i] < 24 {
			n += m.buckets[i]
		}
	}
	return n
}

// countMessage records one sent message. Called after a message is stored.
func (s *Server) countMessage() { s.msgs.record(time.Now()) }

// VersionCount is one row of the online client version distribution.
type VersionCount struct {
	Version string `json:"version"`
	Count   int    `json:"count"`
}

// unknownVersion is how a client that reported no version is shown.
const unknownVersion = "unknown"

// versionDistribution groups online connections by client version, newest
// first, with unknown last.
func versionDistribution(conns []OnlineConn) []VersionCount {
	counts := map[string]int{}
	for _, c := range conns {
		v := c.ClientVersion
		if v == "" {
			v = unknownVersion
		}
		counts[v]++
	}
	out := make([]VersionCount, 0, len(counts))
	for v, n := range counts {
		out = append(out, VersionCount{v, n})
	}
	sort.Slice(out, func(i, j int) bool {
		a, aerr := version.Parse(out[i].Version)
		b, berr := version.Parse(out[j].Version)
		switch {
		case aerr != nil && berr != nil:
			return out[i].Version < out[j].Version
		case aerr != nil:
			return false
		case berr != nil:
			return true
		}
		return version.Compare(a, b) > 0
	})
	return out
}

// statsRes is the admin overview.
type statsRes struct {
	ServerVersion    string          `json:"server_version"`
	MinClientVersion string          `json:"min_client_version,omitempty"`
	StartedAt        int64           `json:"started_at"`
	UptimeSeconds    int64           `json:"uptime_seconds"`
	Connections      int             `json:"connections"`
	Online           []onlineUserRes `json:"online"`
	Versions         []VersionCount  `json:"versions"`
	Counts           storage.Counts  `json:"counts"`
	MessagesSent     int64           `json:"messages_since_start"`
	Messages24h      int64           `json:"messages_24h"`
	MemoryBytes      uint64          `json:"memory_bytes"`
	Goroutines       int             `json:"goroutines"`
	Logs             []string        `json:"logs"`
}

// onlineUserRes is one signed-in connection on the overview.
type onlineUserRes struct {
	UserID        string `json:"user_id"`
	Username      string `json:"username"`
	Remote        string `json:"remote"`
	ClientVersion string `json:"client_version"`
	Since         int64  `json:"since"`
}

func (s *Server) buildStats() (statsRes, error) {
	counts, err := s.st.Counts()
	if err != nil {
		return statsRes{}, err
	}
	hs := s.hub.stats()
	names := s.userNames()
	online := make([]onlineUserRes, 0, len(hs.Online))
	for _, c := range hs.Online {
		v := c.ClientVersion
		if v == "" {
			v = unknownVersion
		}
		online = append(online, onlineUserRes{
			UserID: c.UserID, Username: names[c.UserID], Remote: c.Remote,
			ClientVersion: v, Since: c.Since,
		})
	}
	sort.Slice(online, func(i, j int) bool {
		if online[i].Username != online[j].Username {
			return online[i].Username < online[j].Username
		}
		return online[i].Since < online[j].Since
	})

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	now := time.Now()
	res := statsRes{
		ServerVersion: version.String(),
		StartedAt:     s.startedAt.UnixMilli(),
		UptimeSeconds: int64(now.Sub(s.startedAt).Seconds()),
		Connections:   hs.Connections,
		Online:        online,
		Versions:      versionDistribution(hs.Online),
		Counts:        counts,
		MessagesSent:  s.msgs.total.Load(),
		Messages24h:   s.msgs.last24h(now),
		MemoryBytes:   ms.Sys,
		Goroutines:    runtime.NumGoroutine(),
		Logs:          []string{},
	}
	if s.cfg.HasMinClient {
		res.MinClientVersion = s.cfg.MinClient.String()
	}
	if s.logRing != nil {
		res.Logs = s.logRing.Lines()
	}
	return res, nil
}

// userNames maps every user ID to its username.
func (s *Server) userNames() map[string]string {
	out := map[string]string{}
	users, err := s.st.ListUsers()
	if err != nil {
		return out
	}
	for _, u := range users {
		out[u.ID] = u.Username
	}
	return out
}

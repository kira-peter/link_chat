package server

import (
	"container/heap"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"maps"
	random "math/rand/v2"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"link-chat/pkg/protocol"
	"link-chat/pkg/storage"
)

const maxSimulationUsers = 10_000
const simulationRegistrationBatch = 128
const simulationPreparationActionInterval = 25 * time.Millisecond

type simulationConfig struct {
	Prefix          string                    `json:"prefix"`
	Count           int                       `json:"count"`
	Duration        int                       `json:"duration_seconds"`
	OwnChannel      bool                      `json:"own_channel"`
	UsersPerChannel int                       `json:"users_per_channel"`
	UsePublic       bool                      `json:"use_public_channels"`
	Friends         int                       `json:"friends"`
	JoinMin         int                       `json:"join_min"`
	JoinMax         int                       `json:"join_max"`
	Actions         map[string]simulationRate `json:"actions"`
	Texts           []string                  `json:"texts"`
}

type simulationRate struct {
	Min int `json:"min_seconds"`
	Max int `json:"max_seconds"`
}

var simulationActions = []string{"dm", "channel_message", "focus", "post", "comment", "like", "friend_remove", "presence"}
var simulationDefaultTexts = loadSimulationDefaultTexts()

func loadSimulationDefaultTexts() []string {
	body, err := adminWeb.ReadFile("adminweb/simulation-texts.txt")
	if err != nil {
		panic("server: embedded simulation texts missing")
	}
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSpace(line)
	}
	return lines
}

func (c *simulationConfig) validate() bool {
	c.Prefix = strings.TrimSpace(c.Prefix)
	if c.UsersPerChannel == 0 {
		c.UsersPerChannel = 1
	}
	if c.Prefix == "" || c.Count < 1 || c.Count > maxSimulationUsers || c.Duration < 1 || c.Duration > 3600 ||
		c.Friends < 0 || c.Friends > 30 || c.JoinMin < 0 || c.JoinMax < c.JoinMin || c.JoinMax > 30 ||
		c.UsersPerChannel < 1 || c.UsersPerChannel > maxSimulationUsers {
		return false
	}
	if _, err := validateUsername(fmt.Sprintf("%s_%d", c.Prefix, c.Count)); err != nil {
		return false
	}
	for kind, rate := range c.Actions {
		switch kind {
		case "dm", "channel_message", "focus", "post", "comment", "like", "friend_remove", "presence":
		default:
			return false
		}
		if rate.Min < 1 || rate.Max < rate.Min || rate.Max > 600 {
			return false
		}
	}
	if len(c.Texts) > 20 {
		return false
	}
	for i, value := range c.Texts {
		c.Texts[i] = strings.TrimSpace(value)
		if c.Texts[i] == "" || utf8.RuneCountInString(c.Texts[i]) > 120 {
			return false
		}
	}
	return true
}

type simulationStatus struct {
	ID        string         `json:"id"`
	Phase     string         `json:"phase"`
	Started   int64          `json:"started_at"`
	Ends      int64          `json:"ends_at,omitempty"`
	Count     int            `json:"count"`
	Created   int            `json:"created"`
	Reused    int            `json:"reused"`
	Conflicts int            `json:"conflicts"`
	OK        map[string]int `json:"actions"`
	Failed    int            `json:"failed"`
	Skipped   int            `json:"skipped"`
	LastError string         `json:"last_error,omitempty"`
}

type simulationRun struct {
	cancel context.CancelFunc
	done   chan struct{}
	status simulationStatus // Guarded by simulationState.mu.
}

type simulationState struct {
	mu               sync.Mutex
	enabled          atomic.Bool // Changes are serialized with starting a run.
	run              *simulationRun
	deletion         *simulationDeletion
	accountsRevision atomic.Uint64
}

// DashboardSimulation returns the admin console URL and the current simulation setting.
// An empty URL indicates that the admin console is disabled.
func (s *Server) DashboardSimulation() (string, bool) {
	if !s.cfg.AdminEnabled {
		return "", false
	}
	host, port, _ := net.SplitHostPort(s.cfg.Listen)
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "localhost"
	}
	scheme := "http"
	if s.cfg.TLSCert != "" {
		scheme = "https"
	}
	return scheme + "://" + net.JoinHostPort(host, port) + "/admin/", s.simulation.enabled.Load()
}

func (s *Server) SetDashboardSimulation(enabled bool) {
	if !s.cfg.AdminEnabled {
		return
	}
	s.simulation.mu.Lock()
	defer s.simulation.mu.Unlock()
	s.simulation.enabled.Store(enabled)
	if !enabled {
		s.stopSimulationLocked()
		s.stopSimulationDeletionLocked()
	}
}

func (s *Server) simulationRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/api/simulation", s.simulationAuth(s.apiSimulation))
	mux.HandleFunc("POST /admin/api/simulation/start", s.simulationAuth(s.apiStartSimulation))
	mux.HandleFunc("POST /admin/api/simulation/stop", s.simulationAuth(s.apiStopSimulation))
	mux.HandleFunc("DELETE /admin/api/simulation/users/{id}", s.simulationAuth(s.apiDeleteSimulationUser))
	mux.HandleFunc("POST /admin/api/simulation/deletions/start", s.simulationAuth(s.apiStartSimulationDeletion))
	mux.HandleFunc("POST /admin/api/simulation/deletions/stop", s.simulationAuth(s.apiStopSimulationDeletion))
}

func (s *Server) simulationAuth(h adminHandler) http.HandlerFunc {
	auth := s.adminAuth(PermAccountsManage, func(w http.ResponseWriter, r *http.Request, a adminSession) {
		if !a.user.Super {
			writeErr(w, http.StatusForbidden, protocol.CodeForbidden)
			return
		}
		h(w, r, a)
	})
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.simulation.enabled.Load() {
			http.NotFound(w, r)
			return
		}
		auth(w, r)
	}
}

func (s *Server) apiSimulation(w http.ResponseWriter, r *http.Request, _ adminSession) {
	s.simulation.mu.Lock()
	var status *simulationStatus
	if run := s.simulation.run; run != nil {
		copy := run.status
		copy.OK = maps.Clone(copy.OK)
		status = &copy
	}
	var deletion *simulationDeletionStatus
	if job := s.simulation.deletion; job != nil {
		copy := job.status
		deletion = &copy
	}
	s.simulation.mu.Unlock()
	revision := strconv.FormatUint(s.simulation.accountsRevision.Load(), 10)
	var users []storage.SimulationUser
	if r.URL.Query().Get("users_revision") != revision {
		var err error
		users, err = s.st.SimulationUsers()
		if err != nil {
			s.writeStorageErr(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, struct {
		Run           *simulationStatus         `json:"run"`
		Users         []storage.SimulationUser  `json:"users"`
		Deletion      *simulationDeletionStatus `json:"deletion"`
		UsersRevision string                    `json:"users_revision"`
	}{status, users, deletion, revision})
}

func (s *Server) apiStartSimulation(w http.ResponseWriter, r *http.Request, a adminSession) {
	var cfg simulationConfig
	if err := decodeBody(w, r, &cfg); err != nil || !cfg.validate() {
		writeErr(w, http.StatusBadRequest, protocol.CodeInvalidArgument)
		return
	}

	s.simulation.mu.Lock()
	defer s.simulation.mu.Unlock()
	if !s.simulation.enabled.Load() {
		http.NotFound(w, r)
		return
	}
	select {
	case <-s.shuttingDown:
		writeErr(w, http.StatusServiceUnavailable, protocol.CodeShuttingDown)
		return
	default:
	}
	if s.simulation.run != nil && s.simulation.run.status.Phase != "finished" {
		writeErr(w, http.StatusConflict, protocol.CodeInvalidArgument)
		return
	}

	// The password is generated only to provide valid stored credentials. It
	// is never returned to the browser or retained by the simulator.
	passwordHash, err := hashPassword(rand.Text(), defaultArgonParams)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, protocol.CodeInternal)
		return
	}
	now := storage.Now()
	id := newID("sim_")
	users := make([]storage.User, cfg.Count)
	for i := range users {
		users[i] = storage.User{ID: newID("u_"), Username: fmt.Sprintf("%s_%d", cfg.Prefix, i+1),
			PasswordHash: passwordHash, CreatedAt: now}
	}
	ctx, cancel := context.WithCancel(context.Background())
	run := &simulationRun{cancel: cancel, done: make(chan struct{}), status: simulationStatus{
		ID: id, Phase: "setup", Started: now, OK: map[string]int{},
	}}
	s.simulation.run = run
	go s.runSimulation(ctx, run, cfg, users)
	s.audit(a.actor(), "start_simulation", "", map[string]string{"run_id": id})
	writeJSON(w, http.StatusAccepted, struct {
		ID string `json:"id"`
	}{id})
}

func (s *Server) apiStopSimulation(w http.ResponseWriter, _ *http.Request, _ adminSession) {
	s.stopSimulation()
	writeJSON(w, http.StatusOK, struct{}{})
}

func (s *Server) stopSimulation() <-chan struct{} {
	s.simulation.mu.Lock()
	defer s.simulation.mu.Unlock()
	return s.stopSimulationLocked()
}

func (s *Server) stopSimulationLocked() <-chan struct{} {
	run := s.simulation.run
	if run == nil {
		return nil
	}
	if run.status.Phase != "finished" {
		run.status.Phase = "stopping"
		run.cancel()
	}
	return run.done
}

type simulationActor struct {
	user    storage.User
	conn    *Conn
	retired bool
}

func (s *Server) simulationConnect(u storage.User) *Conn {
	s.accountWrites.RLock()
	defer s.accountWrites.RUnlock()
	unlock := s.lockSession(u.ID)
	defer unlock()
	if current, err := s.st.UserByID(u.ID); err != nil || isAdmin(current) || s.isBanned(u.ID) {
		return nil
	}
	c := &Conn{hub: s.hub, srv: s, closed: make(chan struct{}), out: make(chan protocol.Frame, 1),
		connectedAt: storage.Now(), remoteAddr: "simulation", simulated: true}
	s.hub.attach(c)
	first := s.hub.bindUser(c, u.ID, "simulator")
	c.setUserID(u.ID)
	if first {
		s.broadcastPresence(u.ID, true)
		s.adminNotifyNamedUser("online", u.ID, u.Username)
	}
	return c
}

func (s *Server) simulationDisconnect(c *Conn) {
	s.accountWrites.RLock()
	defer s.accountWrites.RUnlock()
	c.close()
	s.clearChannelView(c, "")
	offline, id := s.hub.detach(c)
	select {
	case <-s.shuttingDown:
		return
	default:
	}
	if offline {
		if _, err := s.st.UserByID(id); err == nil {
			s.broadcastPresence(id, false)
			s.adminNotifyPresence(id, false)
		}
	}
}

var errSimulationNoTarget = errors.New("no eligible target")

func simulationCall[T any](ctx context.Context, a *simulationActor, op string, data any) (T, error) {
	var result T
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if a.conn == nil {
		return result, errSimulationNoTarget
	}
	f, err := protocol.NewReq("simulation", op, data)
	if err != nil {
		return result, err
	}
	var request protocol.ReqPayload
	if err := protocol.DecodePayload(f.Payload, &request); err != nil {
		return result, err
	}
	a.conn.srv.dispatch(ctx, a.conn, f.ID, request)
	select {
	case f := <-a.conn.out:
		var reply protocol.ResPayload
		if err := protocol.DecodePayload(f.Payload, &reply); err != nil {
			return result, err
		}
		if f.Type == protocol.TypeError || !reply.OK {
			return result, fmt.Errorf("%s: %s", op, reply.Code)
		}
		return result, protocol.DecodePayload(reply.Data, &result)
	default:
		return result, errSimulationNoTarget
	}
}

func (s *Server) recordSimulation(run *simulationRun, action string, err error) {
	s.simulation.mu.Lock()
	defer s.simulation.mu.Unlock()
	switch {
	case err == nil:
		run.status.OK[action]++
	case errors.Is(err, errSimulationNoTarget):
		run.status.Skipped++
	case !errors.Is(err, context.Canceled):
		run.status.Failed++
		run.status.LastError = err.Error()
	}
}

func (s *Server) registerSimulationUsers(ctx context.Context, run *simulationRun, users []storage.User) ([]storage.User, error) {
	s.accountWrites.RLock()
	defer s.accountWrites.RUnlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	batch, err := s.st.EnsureSimulationUsers(users, run.status.ID)
	if err != nil {
		return nil, err
	}
	s.simulation.mu.Lock()
	run.status.Created += len(batch.Created)
	run.status.Reused += len(batch.Users) - len(batch.Created)
	run.status.Conflicts += len(users) - len(batch.Users)
	if len(batch.Created) > 0 {
		s.simulation.accountsRevision.Add(1)
	}
	s.simulation.mu.Unlock()
	for _, user := range batch.Created {
		s.adminNotifyNamedUser("registered", user.ID, user.Username)
	}
	return batch.Users, nil
}

func (s *Server) runSimulation(ctx context.Context, run *simulationRun, cfg simulationConfig, users []storage.User) {
	actors := make([]*simulationActor, 0, len(users))
	defer func() {
		for _, a := range actors {
			if a.conn != nil {
				s.simulationDisconnect(a.conn)
			}
		}
		s.simulation.mu.Lock()
		run.status.Phase = "finished"
		close(run.done)
		s.simulation.mu.Unlock()
	}()
	schedule := newSimulationSchedule(cfg)
	// Interleave due actions without competing for a connection's reply queue.
	// Give preparation most of the time when the head of the queue is busy.
	var nextAction time.Time
	advance := func() {
		started := time.Now()
		if ctx.Err() == nil && !started.Before(nextAction) && s.stepSimulationTask(ctx, run, schedule, true) {
			finished := time.Now()
			nextAction = finished.Add(max(simulationPreparationActionInterval, 4*finished.Sub(started)))
		}
	}
	var channels []string
	if !cfg.OwnChannel && cfg.UsePublic && cfg.JoinMax > 0 && ctx.Err() == nil {
		channels = s.simulationPublicChannels(run, nil)
	}
	_, sendsDM := cfg.Actions["dm"]
	_, sendsComments := cfg.Actions["comment"]
	_, sendsLikes := cfg.Actions["like"]
	seedFriends := cfg.Friends > 0 && (sendsDM || sendsComments || sendsLikes)
	for offset := 0; offset < len(users); offset += simulationRegistrationBatch {
		end := min(offset+simulationRegistrationBatch, len(users))
		ready, err := s.registerSimulationUsers(ctx, run, users[offset:end])
		if err != nil {
			s.recordSimulation(run, "register", err)
			return
		}
		var skipped int
		first := len(actors)
		for _, u := range ready {
			if ctx.Err() != nil {
				return
			}
			if c := s.simulationConnect(u); c != nil {
				actor := &simulationActor{user: u, conn: c}
				if cfg.OwnChannel && len(actors)%cfg.UsersPerChannel == 0 {
					if id := s.prepareSimulationChannel(ctx, run, actor); id != "" {
						channels = append(channels, id)
					}
				}
				actors = append(actors, actor)
				schedule.add(actor)
			} else {
				skipped++
			}
			advance()
		}
		s.simulation.mu.Lock()
		run.status.Count = len(actors)
		run.status.Skipped += skipped
		s.simulation.mu.Unlock()
		if seedFriends && len(actors) >= 2 {
			// Give some early actors eligible peers without partitioning the full friend graph.
			s.prepareSimulationFriends(ctx, run, actors[:min(len(actors), 16)], 1, advance)
			seedFriends = false
		}
		if !cfg.OwnChannel {
			s.prepareSimulationJoins(ctx, run, cfg, actors[first:], channels, advance)
		}
	}
	if cfg.OwnChannel && ctx.Err() == nil {
		if cfg.UsePublic && cfg.JoinMax > 0 {
			channels = s.simulationPublicChannels(run, channels)
		}
		s.prepareSimulationJoins(ctx, run, cfg, actors, channels, advance)
	}
	if cfg.Friends > 0 && ctx.Err() == nil {
		s.prepareSimulationFriends(ctx, run, actors, cfg.Friends, advance)
	}
	if ctx.Err() != nil || len(actors) == 0 {
		return
	}
	deadline := time.Now().Add(time.Duration(cfg.Duration) * time.Second)
	s.simulation.mu.Lock()
	if run.status.Phase == "setup" {
		run.status.Phase = "running"
		run.status.Ends = deadline.UnixMilli()
	}
	s.simulation.mu.Unlock()
	if rate, enabled := cfg.Actions["presence"]; enabled {
		now := time.Now()
		for _, actor := range actors {
			heap.Push(&schedule.tasks, simulationTask{actor, "presence", rate, now.Add(rate.firstDelay())})
		}
	}
	s.scheduleSimulation(ctx, run, schedule, deadline)
}

func (s *Server) prepareSimulationChannel(ctx context.Context, run *simulationRun, actor *simulationActor) string {
	name := actor.user.Username + "-room"
	if cv, err := s.st.ChannelByNameKey(name); err == nil && cv.CreatedBy == actor.user.ID {
		if cv.Visibility == protocol.ChannelPrivate {
			return ""
		}
		return cv.ID
	}
	channel, err := simulationCall[protocol.Conversation](ctx, actor, protocol.OpChannelCreate, protocol.ChannelCreateReq{Name: name})
	s.recordSimulation(run, "create_channel", err)
	return channel.ID
}

func (s *Server) simulationPublicChannels(run *simulationRun, fallback []string) []string {
	all, err := s.st.ListConversations()
	if err != nil {
		s.recordSimulation(run, "list_channels", err)
		return fallback
	}
	var channels []string
	for _, cv := range all {
		if cv.Kind == protocol.KindChannel && cv.Visibility != protocol.ChannelPrivate {
			channels = append(channels, cv.ID)
		}
	}
	return channels
}

func (s *Server) prepareSimulationJoins(ctx context.Context, run *simulationRun, cfg simulationConfig, actors []*simulationActor, channels []string, advance func()) {
	if cfg.JoinMax == 0 || len(channels) == 0 {
		return
	}
	for _, a := range actors {
		if ctx.Err() != nil {
			return
		}
		advance()
		list, err := s.st.JoinedChannelIDs(a.user.ID)
		if err != nil {
			s.recordSimulation(run, "join_channel", err)
			continue
		}
		joined := make(map[string]bool, len(list))
		for _, id := range list {
			joined[id] = true
		}
		count := cfg.JoinMin + random.IntN(cfg.JoinMax-cfg.JoinMin+1)
		// Lazily shuffle indices so each actor samples only as much of the pool as needed.
		swapped := make(map[int]int)
		for i := 0; i < len(channels) && count > 0 && ctx.Err() == nil; i++ {
			j := i + random.IntN(len(channels)-i)
			pick, ok := swapped[j]
			if !ok {
				pick = j
			}
			if value, ok := swapped[i]; ok {
				swapped[j] = value
			} else {
				swapped[j] = i
			}
			delete(swapped, i)
			id := channels[pick]
			if joined[id] {
				continue
			}
			_, err := simulationCall[protocol.Conversation](ctx, a, protocol.OpChannelJoin, protocol.ConversationRef{ConversationID: id})
			s.recordSimulation(run, "join_channel", err)
			count--
			advance()
		}
	}
}

func (s *Server) prepareSimulationFriends(ctx context.Context, run *simulationRun, actors []*simulationActor, limit int, advance func()) {
	order := random.Perm(len(actors))
	participants := make(map[string]bool, len(actors))
	for _, a := range actors {
		participants[a.user.ID] = true
	}
	friends := make([]map[string]bool, len(actors))
	for i, a := range actors {
		if ctx.Err() != nil {
			return
		}
		advance()
		ids, err := s.st.FriendIDs(a.user.ID)
		if err != nil {
			s.recordSimulation(run, "friendship", err)
		}
		friends[i] = make(map[string]bool)
		for _, id := range ids {
			if participants[id] {
				friends[i][id] = true
			}
		}
	}
	// Visit a bounded number of neighbours on a shuffled ring rather than
	// building every possible pair. Each edge is visited at most once.
	for offset := 1; offset <= min(len(order)/2, 2*limit); offset++ {
		positions := len(order)
		if 2*offset == len(order) {
			positions /= 2
		}
		for p, i := range order[:positions] {
			if ctx.Err() != nil {
				return
			}
			advance()
			j := order[(p+offset)%len(order)]
			a, b := actors[i], actors[j]
			if len(friends[i]) >= limit || len(friends[j]) >= limit || friends[i][b.user.ID] {
				continue
			}
			_, err := simulationCall[protocol.FriendRequestRes](ctx, a, protocol.OpFriendRequest, protocol.FriendRequestReq{Username: b.user.Username})
			if err == nil {
				_, err = simulationCall[protocol.FriendRequestRes](ctx, b, protocol.OpFriendRequest, protocol.FriendRequestReq{Username: a.user.Username})
			}
			if err == nil {
				friends[i][b.user.ID] = true
				friends[j][a.user.ID] = true
			}
			s.recordSimulation(run, "friendship", err)
		}
	}
}

type simulationTask struct {
	actor *simulationActor
	kind  string
	rate  simulationRate
	due   time.Time
}

type simulationTasks []simulationTask

func (t simulationTasks) Len() int           { return len(t) }
func (t simulationTasks) Less(i, j int) bool { return t[i].due.Before(t[j].due) }
func (t simulationTasks) Swap(i, j int)      { t[i], t[j] = t[j], t[i] }
func (t *simulationTasks) Push(x any)        { *t = append(*t, x.(simulationTask)) }
func (t *simulationTasks) Pop() any {
	last := len(*t) - 1
	item := (*t)[last]
	(*t)[last] = simulationTask{}
	*t = (*t)[:last]
	return item
}

func (r simulationRate) delay() time.Duration {
	span := (r.Max - r.Min) * 1000
	return time.Duration(r.Min*1000+random.IntN(span+1)) * time.Millisecond
}

func (r simulationRate) firstDelay() time.Duration {
	delay := r.delay()
	if r.Min == r.Max {
		// Preserve the chosen repeat period but spread fixed-rate actors across the first second.
		delay += time.Duration(random.IntN(1000)) * time.Millisecond
	}
	return delay
}

type simulationSchedule struct {
	cfg          simulationConfig
	tasks        simulationTasks
	participants map[string]bool
	viewed       map[string]string
}

func newSimulationSchedule(cfg simulationConfig) *simulationSchedule {
	if len(cfg.Texts) == 0 {
		cfg.Texts = simulationDefaultTexts
	}
	return &simulationSchedule{cfg: cfg, participants: make(map[string]bool, cfg.Count), viewed: make(map[string]string)}
}

func (schedule *simulationSchedule) add(actor *simulationActor) {
	schedule.participants[actor.user.ID] = true
	now := time.Now()
	for _, kind := range simulationActions {
		if kind == "presence" {
			continue
		}
		if rate, ok := schedule.cfg.Actions[kind]; ok {
			heap.Push(&schedule.tasks, simulationTask{actor, kind, rate, now.Add(rate.firstDelay())})
		}
	}
}

func (s *Server) stepSimulationTask(ctx context.Context, run *simulationRun, schedule *simulationSchedule, preparing bool) bool {
	if len(schedule.tasks) == 0 || schedule.tasks[0].due.After(time.Now()) {
		return false
	}
	task := heap.Pop(&schedule.tasks).(simulationTask)
	actor := task.actor
	if actor.retired {
		return false
	}
	if actor.conn != nil {
		select {
		case <-actor.conn.closed:
			actor.retired = true
			delete(schedule.participants, actor.user.ID)
			return false
		default:
		}
	}
	switch {
	case task.kind == "presence":
		if actor.conn != nil {
			s.simulationDisconnect(actor.conn)
			actor.conn = nil
			delete(schedule.viewed, actor.user.ID)
		} else if actor.conn = s.simulationConnect(actor.user); actor.conn == nil {
			actor.retired = true
			delete(schedule.participants, actor.user.ID)
			return false
		}
		s.recordSimulation(run, task.kind, nil)
	case actor.conn != nil:
		err := s.simulationStep(ctx, schedule.cfg, actor, task.kind, schedule.viewed, schedule.participants)
		if !preparing || !errors.Is(err, errSimulationNoTarget) {
			s.recordSimulation(run, task.kind, err)
		}
	}
	task.due = time.Now().Add(task.rate.delay())
	heap.Push(&schedule.tasks, task)
	return true
}

func (s *Server) scheduleSimulation(ctx context.Context, run *simulationRun, schedule *simulationSchedule, end time.Time) {
	timer := time.NewTimer(0)
	<-timer.C
	defer timer.Stop()
	for ctx.Err() == nil {
		when := end
		if len(schedule.tasks) > 0 && schedule.tasks[0].due.Before(when) {
			when = schedule.tasks[0].due
		}
		timer.Reset(time.Until(when))
		select {
		case <-ctx.Done():
			return
		case <-s.shuttingDown:
			return
		case <-timer.C:
		}
		if when.Equal(end) || !time.Now().Before(end) {
			return
		}
		s.stepSimulationTask(ctx, run, schedule, false)
	}
}

func (s *Server) simulationStep(ctx context.Context, cfg simulationConfig, a *simulationActor, kind string, viewed map[string]string, participants map[string]bool) error {
	text := cfg.Texts[random.IntN(len(cfg.Texts))]
	var friendIDs []string
	if kind == "dm" || kind == "comment" || kind == "like" || kind == "friend_remove" {
		ids, err := s.st.FriendIDs(a.user.ID)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if participants[id] {
				friendIDs = append(friendIDs, id)
			}
		}
		if len(friendIDs) == 0 {
			return errSimulationNoTarget
		}
	}
	switch kind {
	case "dm", "friend_remove":
		peer, err := s.st.UserByID(friendIDs[random.IntN(len(friendIDs))])
		if err != nil {
			return err
		}
		if kind == "friend_remove" {
			_, err := simulationCall[struct{}](ctx, a, protocol.OpFriendRemove, protocol.FriendUserReq{Username: peer.Username})
			return err
		}
		cv, err := simulationCall[protocol.Conversation](ctx, a, protocol.OpDMOpen, protocol.DMOpenReq{Username: peer.Username})
		if err != nil {
			return err
		}
		_, err = simulationCall[protocol.MessageEvent](ctx, a, protocol.OpMessageSend, protocol.SendMessageReq{ConversationID: cv.ID, Body: text})
		return err
	case "channel_message", "focus":
		list, err := s.st.JoinedChannelIDs(a.user.ID)
		if err != nil {
			return err
		}
		var ids []string
		for _, id := range list {
			if kind != "focus" || viewed[a.user.ID] != id {
				ids = append(ids, id)
			}
		}
		if len(ids) == 0 {
			return errSimulationNoTarget
		}
		id := ids[random.IntN(len(ids))]
		if kind == "focus" {
			_, err := simulationCall[protocol.PongRes](ctx, a, protocol.OpPresencePing, protocol.PingReq{ViewedChannel: &id})
			if err == nil {
				viewed[a.user.ID] = id
			}
			return err
		}
		_, err = simulationCall[protocol.MessageEvent](ctx, a, protocol.OpMessageSend, protocol.SendMessageReq{ConversationID: id, Body: text})
		return err
	case "post":
		_, err := simulationCall[protocol.Moment](ctx, a, protocol.OpMomentPost, protocol.MomentPostReq{Body: text})
		return err
	case "comment", "like":
		page, err := s.st.MomentsBy(friendIDs, a.user.ID, storage.MomentCursor{}, 20)
		if err != nil {
			return err
		}
		if len(page.Moments) == 0 {
			return errSimulationNoTarget
		}
		if kind == "like" {
			ids := make([]string, 0, len(page.Moments))
			for _, moment := range page.Moments {
				ids = append(ids, moment.ID)
			}
			previews, err := s.st.LikePreviews(a.user.ID, ids, 1)
			if err != nil {
				return err
			}
			var remaining []string
			for _, id := range ids {
				if !previews[id].Liked {
					remaining = append(remaining, id)
				}
			}
			if len(remaining) == 0 {
				return errSimulationNoTarget
			}
			_, err = simulationCall[protocol.Moment](ctx, a, protocol.OpMomentLike, protocol.MomentLikeReq{
				MomentID: remaining[random.IntN(len(remaining))], Like: true,
			})
			return err
		}
		m := page.Moments[random.IntN(len(page.Moments))]
		_, err = simulationCall[protocol.Moment](ctx, a, protocol.OpMomentComment, protocol.MomentCommentReq{MomentID: m.ID, Body: text})
		return err
	}
	return errSimulationNoTarget
}

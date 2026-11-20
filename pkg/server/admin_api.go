package server

import (
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"link-chat/pkg/protocol"
	"link-chat/pkg/storage"
)

// Admin console JSON API (spec admin-console). Routes are registered in
// adminRoutes; each one names the permission it needs, and adminAuth enforces
// it on the server, so hiding a section in the page is never the only guard.

func (s *Server) adminRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /admin/api/login", s.handleAdminLogin)
	mux.HandleFunc("POST /admin/api/logout", s.adminAuth("", s.handleAdminLogout))
	mux.HandleFunc("GET /admin/api/me", s.adminAuth("", s.apiMe))

	mux.HandleFunc("GET /admin/api/stats", s.adminAuth(PermStatsView, s.apiStats))

	mux.HandleFunc("GET /admin/api/users", s.adminAuth(PermUsersModerate, s.apiUsers))
	mux.HandleFunc("GET /admin/api/users/{id}", s.adminAuth(PermUsersModerate, s.apiUser))
	mux.HandleFunc("POST /admin/api/users/{id}/ban", s.adminAuth(PermUsersModerate, s.apiSanction(storage.SanctionBan)))
	mux.HandleFunc("POST /admin/api/users/{id}/mute", s.adminAuth(PermUsersModerate, s.apiSanction(storage.SanctionMute)))
	mux.HandleFunc("POST /admin/api/users/{id}/revoke", s.adminAuth(PermUsersModerate, s.apiRevoke))
	mux.HandleFunc("POST /admin/api/users/{id}/reset-password", s.adminAuth(PermUsersModerate, s.apiResetPassword))
	mux.HandleFunc("POST /admin/api/users/{id}/kick", s.adminAuth(PermUsersModerate, s.apiKick))
	mux.HandleFunc("POST /admin/api/users/{id}/perms", s.adminAuth(PermAccountsManage, s.apiSetPerms))

	mux.HandleFunc("GET /admin/api/accounts", s.adminAuth(PermAccountsManage, s.apiAccounts))
	mux.HandleFunc("POST /admin/api/accounts", s.adminAuth(PermAccountsManage, s.apiCreateAccount))
	mux.HandleFunc("GET /admin/api/audit", s.adminAuth(PermAccountsManage, s.apiAudit))

	mux.HandleFunc("GET /admin/api/conversations", s.adminAuth(PermChatView, s.apiConversations))
	mux.HandleFunc("GET /admin/api/conversations/{id}/messages", s.adminAuth(PermChatView, s.apiMessages))
	mux.HandleFunc("GET /admin/api/search", s.adminAuth(PermChatView, s.apiSearch))

	mux.HandleFunc("GET /admin/api/events", s.adminAuth("", s.apiEvents))
}

// ---------- me and stats ----------

func (s *Server) apiMe(w http.ResponseWriter, r *http.Request, a adminSession) {
	writeJSON(w, http.StatusOK, s.meFor(a.user))
}

func (s *Server) apiStats(w http.ResponseWriter, r *http.Request, a adminSession) {
	res, err := s.buildStats()
	if err != nil {
		s.writeStorageErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// ---------- users ----------

// sanctionRes is a sanction as the console shows it.
type sanctionRes struct {
	storage.Sanction
	CreatedByName string `json:"created_by_name"`
	RevokedByName string `json:"revoked_by_name,omitempty"`
	Active        bool   `json:"active"`
}

type userRowRes struct {
	ID             string       `json:"id"`
	Username       string       `json:"username"`
	CreatedAt      int64        `json:"created_at"`
	Online         bool         `json:"online"`
	ClientVersions []string     `json:"client_versions,omitempty"`
	Admin          bool         `json:"admin"`
	Super          bool         `json:"super"`
	Perms          []string     `json:"perms"`
	Ban            *sanctionRes `json:"ban,omitempty"`
	Mute           *sanctionRes `json:"mute,omitempty"`
	CanRecover     bool         `json:"can_recover"`
}

type sessionRes struct {
	Purpose       string `json:"purpose"`
	Remote        string `json:"remote"`
	Device        string `json:"device"`
	ClientVersion string `json:"client_version"`
	CreatedAt     int64  `json:"created_at"`
	ExpiresAt     int64  `json:"expires_at"`
}

type userDetailRes struct {
	userRowRes
	Sessions  []sessionRes  `json:"sessions"`
	Sanctions []sanctionRes `json:"sanctions"`
}

func (s *Server) sanctionView(sc storage.Sanction, names map[string]string, now int64) *sanctionRes {
	return &sanctionRes{
		Sanction:      sc,
		CreatedByName: nameOr(names, sc.CreatedBy),
		RevokedByName: nameOr(names, sc.RevokedBy),
		Active:        sc.ActiveAt(now),
	}
}

func nameOr(names map[string]string, id string) string {
	if id == "" {
		return ""
	}
	if n, ok := names[id]; ok {
		return n
	}
	return id // "cli", or an account since removed
}

// userRow builds one list row. versions are the user's online client versions.
func (s *Server) userRow(u storage.User, versions []string, names map[string]string, now int64) userRowRes {
	row := userRowRes{
		ID: u.ID, Username: u.Username, CreatedAt: u.CreatedAt,
		Online: len(versions) > 0, ClientVersions: versions,
		Admin: isAdmin(u), Super: u.Super, Perms: permList(effectivePerms(u)),
		CanRecover: u.CanRecover(),
	}
	if sc, ok, _ := s.st.ActiveSanction(u.ID, storage.SanctionBan, now); ok {
		row.Ban = s.sanctionView(sc, names, now)
	}
	if sc, ok, _ := s.st.ActiveSanction(u.ID, storage.SanctionMute, now); ok {
		row.Mute = s.sanctionView(sc, names, now)
	}
	return row
}

// onlineVersions maps user ID to the client versions of their live connections.
func (s *Server) onlineVersions() map[string][]string {
	out := map[string][]string{}
	for _, c := range s.hub.stats().Online {
		v := c.ClientVersion
		if v == "" {
			v = unknownVersion
		}
		out[c.UserID] = append(out[c.UserID], v)
	}
	return out
}

func (s *Server) apiUsers(w http.ResponseWriter, r *http.Request, a adminSession) {
	users, err := s.st.ListUsers()
	if err != nil {
		s.writeStorageErr(w, err)
		return
	}
	names := map[string]string{}
	for _, u := range users {
		names[u.ID] = u.Username
	}
	online := s.onlineVersions()
	now := storage.Now()
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	out := make([]userRowRes, 0, len(users))
	for _, u := range users {
		if q != "" && !strings.Contains(strings.ToLower(u.Username), q) {
			continue
		}
		out = append(out, s.userRow(u, online[u.ID], names, now))
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": out})
}

func (s *Server) apiUser(w http.ResponseWriter, r *http.Request, a adminSession) {
	u, ok := s.loadUser(w, r)
	if !ok {
		return
	}
	names := s.userNames()
	now := storage.Now()
	res := userDetailRes{userRowRes: s.userRow(u, s.onlineVersions()[u.ID], names, now)}

	toks, _ := s.st.ListTokensForUser(u.ID)
	res.Sessions = []sessionRes{}
	for _, t := range toks {
		p := t.EffectivePurpose()
		if p == storage.PurposeReset || t.ExpiresAt < now {
			continue
		}
		res.Sessions = append(res.Sessions, sessionRes{
			Purpose: p, Remote: t.Remote, Device: t.Device, ClientVersion: orUnknown(t.ClientVersion, p),
			CreatedAt: t.CreatedAt, ExpiresAt: t.ExpiresAt,
		})
	}
	scs, _ := s.st.SanctionsForUser(u.ID)
	res.Sanctions = []sanctionRes{}
	for _, sc := range scs {
		res.Sanctions = append(res.Sanctions, *s.sanctionView(sc, names, now))
	}
	writeJSON(w, http.StatusOK, res)
}

// orUnknown shows a missing chat client version as unknown; console sessions
// have none to show.
func orUnknown(v, purpose string) string {
	if v != "" || purpose != storage.PurposeSession {
		return v
	}
	return unknownVersion
}

func (s *Server) loadUser(w http.ResponseWriter, r *http.Request) (storage.User, bool) {
	u, err := s.st.UserByID(r.PathValue("id"))
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeErr(w, http.StatusNotFound, protocol.CodeUserNotFound)
		} else {
			s.writeStorageErr(w, err)
		}
		return storage.User{}, false
	}
	return u, true
}

type sanctionReq struct {
	Reason string `json:"reason"`
	// Minutes is the duration; 0 means permanent.
	Minutes int64 `json:"minutes"`
}

// maxSanctionMinutes keeps a "custom" duration within something a date can
// represent: ten years.
const maxSanctionMinutes = 10 * 365 * 24 * 60

func (s *Server) apiSanction(kind string) adminHandler {
	return func(w http.ResponseWriter, r *http.Request, a adminSession) {
		var req sanctionReq
		if err := decodeBody(w, r, &req); err != nil || req.Minutes < 0 || req.Minutes > maxSanctionMinutes {
			writeErr(w, http.StatusBadRequest, protocol.CodeInvalidArgument)
			return
		}
		if r.PathValue("id") == a.user.ID {
			writeErrMsg(w, http.StatusForbidden, protocol.CodeForbidden, "you cannot sanction yourself")
			return
		}
		sc, err := s.Sanction(a.actor(), r.PathValue("id"), kind, req.Reason, time.Duration(req.Minutes)*time.Minute)
		if err != nil {
			s.writeModerationErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, s.sanctionView(sc, s.userNames(), storage.Now()))
	}
}

type revokeReq struct {
	SanctionID string `json:"sanction_id"`
}

func (s *Server) apiRevoke(w http.ResponseWriter, r *http.Request, a adminSession) {
	var req revokeReq
	if err := decodeBody(w, r, &req); err != nil || req.SanctionID == "" {
		writeErr(w, http.StatusBadRequest, protocol.CodeInvalidArgument)
		return
	}
	// The sanction must belong to the user in the path, so a stale page cannot
	// revoke someone else's by ID.
	scs, err := s.st.SanctionsForUser(r.PathValue("id"))
	if err != nil {
		s.writeStorageErr(w, err)
		return
	}
	found := false
	for _, sc := range scs {
		if sc.ID == req.SanctionID {
			found = true
		}
	}
	if !found {
		writeErrMsg(w, http.StatusNotFound, protocol.CodeInvalidArgument, "no such sanction for this user")
		return
	}
	sc, err := s.RevokeSanction(a.actor(), req.SanctionID)
	if err != nil {
		s.writeModerationErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.sanctionView(sc, s.userNames(), storage.Now()))
}

type passwordReq struct {
	Password string `json:"password"`
}

func (s *Server) apiResetPassword(w http.ResponseWriter, r *http.Request, a adminSession) {
	var req passwordReq
	if err := decodeBody(w, r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, protocol.CodeInvalidArgument)
		return
	}
	if r.PathValue("id") == a.user.ID {
		writeErrMsg(w, http.StatusForbidden, protocol.CodeForbidden, "change your own password from the chat client")
		return
	}
	if err := s.ResetPassword(a.actor(), r.PathValue("id"), req.Password, false); err != nil {
		s.writeModerationErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct{}{})
}

func (s *Server) apiKick(w http.ResponseWriter, r *http.Request, a adminSession) {
	n, err := s.Kick(a.actor(), r.PathValue("id"))
	if err != nil {
		s.writeModerationErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"closed": n})
}

// ---------- accounts and permissions ----------

type permsReq struct {
	Perms []string `json:"perms"`
}

func (s *Server) apiSetPerms(w http.ResponseWriter, r *http.Request, a adminSession) {
	var req permsReq
	if err := decodeBody(w, r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, protocol.CodeInvalidArgument)
		return
	}
	target, ok := s.loadUser(w, r)
	if !ok {
		return
	}
	if err := canGrant(a.user, target, req.Perms); err != nil {
		s.writeGrantErr(w, err)
		return
	}
	before := permList(effectivePerms(target))
	if err := s.st.SetUserPerms(target.ID, req.Perms); err != nil {
		s.writeStorageErr(w, err)
		return
	}
	updated, _ := s.st.UserByID(target.ID)
	after := permList(effectivePerms(updated))
	s.log.Info("permissions changed", "user", target.Username, "by", a.user.Username, "perms", strings.Join(after, ","))
	s.audit(a.actor(), "set_perms", target.ID, map[string]string{
		"username": target.Username,
		"before":   strings.Join(before, ","),
		"after":    strings.Join(after, ","),
	})
	if len(after) == 0 {
		// No permissions left: their console streams end now, not at the next
		// heartbeat.
		s.closeAdminStreams(target.ID)
	} else {
		s.refreshAdminStreams(target.ID)
	}
	writeJSON(w, http.StatusOK, s.userRow(updated, s.onlineVersions()[updated.ID], s.userNames(), storage.Now()))
}

func (s *Server) apiAccounts(w http.ResponseWriter, r *http.Request, a adminSession) {
	users, err := s.st.ListUsers()
	if err != nil {
		s.writeStorageErr(w, err)
		return
	}
	names := map[string]string{}
	for _, u := range users {
		names[u.ID] = u.Username
	}
	online := s.onlineVersions()
	now := storage.Now()
	out := make([]userRowRes, 0, len(users))
	for _, u := range users {
		out = append(out, s.userRow(u, online[u.ID], names, now))
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": out})
}

type createAccountReq struct {
	Username string   `json:"username"`
	Password string   `json:"password"`
	Perms    []string `json:"perms"`
}

func (s *Server) apiCreateAccount(w http.ResponseWriter, r *http.Request, a adminSession) {
	var req createAccountReq
	if err := decodeBody(w, r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, protocol.CodeInvalidArgument)
		return
	}
	// Granting at creation follows the same rules as granting later; checked
	// before anything is written.
	if len(req.Perms) > 0 {
		if err := canGrant(a.user, storage.User{ID: "new"}, req.Perms); err != nil {
			s.writeGrantErr(w, err)
			return
		}
	}
	u, err := s.CreateAccount(a.actor(), req.Username, req.Password, req.Perms, false)
	if err != nil {
		s.writeAccountErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.userRow(u, nil, s.userNames(), storage.Now()))
}

// CreateAccount creates an account without a security question, so it can only
// be recovered by an administrator (spec account-auth / 管理员创建的账号).
func (s *Server) CreateAccount(a Actor, username, password string, perms []string, super bool) (storage.User, error) {
	name, err := validateUsername(username)
	if err != nil {
		return storage.User{}, err
	}
	if err := validatePassword(password); err != nil {
		return storage.User{}, err
	}
	hash, err := hashPassword(password, defaultArgonParams)
	if err != nil {
		return storage.User{}, err
	}
	u := storage.User{
		ID:           newID("u_"),
		Username:     name,
		PasswordHash: hash,
		CreatedAt:    storage.Now(),
		Super:        super,
	}
	if len(perms) > 0 {
		u.Perms = append([]string(nil), perms...)
		sort.Strings(u.Perms)
	}
	if err := s.st.CreateUser(u); err != nil {
		return storage.User{}, err
	}
	s.log.Info("account created", "user", u.Username, "by", a.Name, "perms", strings.Join(u.Perms, ","), "super", super)
	s.audit(a, "create_account", u.ID, map[string]string{
		"username": u.Username,
		"perms":    strings.Join(u.Perms, ","),
		"super":    strconv.FormatBool(super),
	})
	return u, nil
}

// ---------- audit ----------

type auditRes struct {
	storage.AuditEntry
	TargetName string `json:"target_name,omitempty"`
}

func (s *Server) apiAudit(w http.ResponseWriter, r *http.Request, a adminSession) {
	q := r.URL.Query()
	f := storage.AuditFilter{
		ActorID: q.Get("actor"),
		Action:  q.Get("action"),
		From:    queryInt(q.Get("from")),
		To:      queryInt(q.Get("to")),
		Before:  queryInt(q.Get("before")),
		Limit:   int(queryInt(q.Get("limit"))),
	}
	// Filtering by name is what a person types; resolve it to the ID.
	if name := strings.TrimSpace(q.Get("actor_name")); name != "" && f.ActorID == "" {
		if u, err := s.st.UserByUsernameKey(strings.ToLower(name)); err == nil {
			f.ActorID = u.ID
		} else if name == CLIActor.Name {
			f.ActorID = CLIActor.ID
		} else {
			writeJSON(w, http.StatusOK, map[string]any{"entries": []auditRes{}})
			return
		}
	}
	entries, err := s.st.ListAudit(f)
	if err != nil {
		s.writeStorageErr(w, err)
		return
	}
	names := s.userNames()
	out := make([]auditRes, 0, len(entries))
	for _, e := range entries {
		out = append(out, auditRes{AuditEntry: e, TargetName: s.auditTargetName(e, names)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": out})
}

// auditTargetName labels an entry's target for display.
func (s *Server) auditTargetName(e storage.AuditEntry, names map[string]string) string {
	if e.Target == "" {
		return ""
	}
	if n, ok := names[e.Target]; ok {
		return n
	}
	if cv, err := s.st.ConversationByID(e.Target); err == nil {
		return s.conversationLabel(cv, names)
	}
	return ""
}

func queryInt(v string) int64 {
	n, _ := strconv.ParseInt(v, 10, 64)
	return n
}

// ---------- conversations and search ----------

type adminConvRes struct {
	ID           string   `json:"id"`
	Kind         string   `json:"kind"`
	Label        string   `json:"label"`
	Topic        string   `json:"topic,omitempty"`
	CreatedBy    string   `json:"created_by,omitempty"`
	CreatedAt    int64    `json:"created_at"`
	Members      int      `json:"members"`
	MemberNames  []string `json:"member_names,omitempty"`
	LastAt       int64    `json:"last_at"`
	MessageCount int64    `json:"message_count"`
}

// conversationLabel names a conversation for the console: "#name" for a
// channel, "alice ↔ bob" for a private chat.
func (s *Server) conversationLabel(cv storage.Conversation, names map[string]string) string {
	if cv.Kind == protocol.KindDM {
		parts := make([]string, 0, len(cv.MemberIDs))
		for _, id := range cv.MemberIDs {
			parts = append(parts, nameOr(names, id))
		}
		return strings.Join(parts, " ↔ ")
	}
	return "#" + cv.Name
}

func (s *Server) apiConversations(w http.ResponseWriter, r *http.Request, a adminSession) {
	convs, err := s.st.ListConversations()
	if err != nil {
		s.writeStorageErr(w, err)
		return
	}
	names := s.userNames()
	out := make([]adminConvRes, 0, len(convs))
	for _, cv := range convs {
		row := adminConvRes{
			ID: cv.ID, Kind: cv.Kind, Label: s.conversationLabel(cv, names), Topic: cv.Topic,
			CreatedBy: nameOr(names, cv.CreatedBy), CreatedAt: cv.CreatedAt,
			Members: s.st.MemberCount(cv.ID),
		}
		if cv.Kind == protocol.KindDM {
			for _, id := range cv.MemberIDs {
				row.MemberNames = append(row.MemberNames, nameOr(names, id))
			}
		}
		if last, _, err := s.st.MessagesBefore(cv.ID, 0, 1); err == nil && len(last) == 1 {
			row.LastAt = last[0].CreatedAt
			row.MessageCount = last[0].Seq
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].LastAt != out[j].LastAt {
			return out[i].LastAt > out[j].LastAt
		}
		return out[i].Label < out[j].Label
	})
	writeJSON(w, http.StatusOK, map[string]any{"conversations": out})
}

func (s *Server) apiMessages(w http.ResponseWriter, r *http.Request, a adminSession) {
	cv, err := s.st.ConversationByID(r.PathValue("id"))
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeErr(w, http.StatusNotFound, protocol.CodeConversationNotFound)
		} else {
			s.writeStorageErr(w, err)
		}
		return
	}
	before := queryInt(r.URL.Query().Get("before"))
	limit := int(queryInt(r.URL.Query().Get("limit")))
	if limit <= 0 || limit > 200 {
		limit = s.cfg.HistoryPageSize
	}
	msgs, more, err := s.st.MessagesBefore(cv.ID, before, limit)
	if err != nil {
		s.writeStorageErr(w, err)
		return
	}
	names := s.userNames()
	label := s.conversationLabel(cv, names)
	// Every look at a conversation is recorded, private or not, including each
	// page further back (spec account-moderation / 审计日志).
	detail := map[string]string{"kind": cv.Kind, "label": label}
	if before > 0 {
		detail["before"] = strconv.FormatInt(before, 10)
	}
	s.audit(a.actor(), "view_conversation", cv.ID, detail)

	out := make([]protocol.Message, 0, len(msgs))
	for _, m := range msgs {
		pm := s.toProtocolMessage(m)
		if pm.AuthorName == "" {
			pm.AuthorName = nameOr(names, m.AuthorID)
		}
		out = append(out, pm)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"conversation": adminConvRes{ID: cv.ID, Kind: cv.Kind, Label: label, Topic: cv.Topic, CreatedAt: cv.CreatedAt},
		"messages":     out,
		"has_more":     more,
	})
}

type searchHit struct {
	protocol.Message
	ConversationLabel string `json:"conversation_label"`
	ConversationKind  string `json:"conversation_kind"`
}

func (s *Server) apiSearch(w http.ResponseWriter, r *http.Request, a adminSession) {
	q := r.URL.Query()
	f := storage.SearchFilter{
		Keyword:        q.Get("q"),
		ConversationID: q.Get("conversation"),
		From:           queryInt(q.Get("from")),
		To:             queryInt(q.Get("to")),
		Limit:          int(queryInt(q.Get("limit"))),
	}
	names := s.userNames()
	if author := strings.TrimSpace(q.Get("author")); author != "" {
		u, err := s.st.UserByUsernameKey(strings.ToLower(author))
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"messages": []searchHit{}, "next": nil})
			return
		}
		f.AuthorID = u.ID
	}
	cur := storage.SearchCursor{Conv: int(queryInt(q.Get("cursor_conv"))), Seq: queryInt(q.Get("cursor_seq"))}

	// Search terms are recorded in full (spec admin-console / 跨会话搜索).
	detail := map[string]string{}
	for _, k := range []string{"q", "author", "conversation", "from", "to"} {
		if v := q.Get(k); v != "" {
			detail[k] = v
		}
	}
	if cur != (storage.SearchCursor{}) {
		detail["continued"] = "true"
	}
	s.audit(a.actor(), "search", "", detail)

	res, err := s.st.SearchMessages(f, cur)
	if err != nil {
		s.writeStorageErr(w, err)
		return
	}
	labels := map[string]storage.Conversation{}
	out := make([]searchHit, 0, len(res.Messages))
	for _, m := range res.Messages {
		cv, ok := labels[m.ConversationID]
		if !ok {
			cv, _ = s.st.ConversationByID(m.ConversationID)
			labels[m.ConversationID] = cv
		}
		pm := s.toProtocolMessage(m)
		if pm.AuthorName == "" {
			pm.AuthorName = nameOr(names, m.AuthorID)
		}
		out = append(out, searchHit{Message: pm, ConversationLabel: s.conversationLabel(cv, names), ConversationKind: cv.Kind})
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": out, "next": res.Next})
}

// ---------- error mapping ----------

func (s *Server) writeStorageErr(w http.ResponseWriter, err error) {
	s.log.Error("admin storage error", "err", err)
	if errors.Is(err, storage.ErrUnavailable) {
		writeErr(w, http.StatusServiceUnavailable, protocol.CodeStorageUnavailable)
		return
	}
	writeErr(w, http.StatusInternalServerError, protocol.CodeInternal)
}

func (s *Server) writeModerationErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, storage.ErrNotFound):
		writeErr(w, http.StatusNotFound, protocol.CodeUserNotFound)
	case errors.Is(err, ErrProtectedUser):
		writeErrMsg(w, http.StatusForbidden, protocol.CodeForbidden, err.Error())
	case errors.Is(err, ErrReasonRequired), errors.Is(err, ErrBadDuration):
		writeErrMsg(w, http.StatusBadRequest, protocol.CodeInvalidArgument, err.Error())
	case errors.Is(err, ErrNotActive):
		writeErrMsg(w, http.StatusConflict, protocol.CodeInvalidArgument, err.Error())
	case errors.Is(err, errWeakPassword):
		writeErr(w, http.StatusBadRequest, protocol.CodeWeakPassword)
	default:
		s.writeStorageErr(w, err)
	}
}

func (s *Server) writeGrantErr(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrGrantUnknown) {
		writeErrMsg(w, http.StatusBadRequest, protocol.CodeInvalidArgument, err.Error())
		return
	}
	writeErrMsg(w, http.StatusForbidden, protocol.CodeForbidden, err.Error())
}

func (s *Server) writeAccountErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errInvalidUsername):
		writeErr(w, http.StatusBadRequest, protocol.CodeInvalidUsername)
	case errors.Is(err, errWeakPassword):
		writeErr(w, http.StatusBadRequest, protocol.CodeWeakPassword)
	case errors.Is(err, storage.ErrConflict):
		writeErr(w, http.StatusConflict, protocol.CodeUsernameTaken)
	default:
		s.writeStorageErr(w, err)
	}
}

package server

import (
	"encoding/json"
	"errors"
	"mime"
	"net"
	"net/http"
	"strings"
	"time"

	"link-chat/pkg/protocol"
	"link-chat/pkg/storage"
)

// Admin console authentication (design.md decision 3). The console signs in
// with the same username and password as chat, but holds its own token of
// purpose "admin" in an HttpOnly cookie. Every request re-reads the account,
// so a revoked permission or a ban takes effect on the very next request.

const (
	adminCookie   = "lc_admin"
	adminTokenTTL = 12 * time.Hour
	// adminCSRFHeader must accompany every state-changing request. A cross-site
	// form cannot set it, which together with SameSite=Strict keeps another
	// site from acting with the administrator's cookie.
	adminCSRFHeader = "X-Requested-With"
	adminCSRFValue  = "link-chat-admin"
)

// adminError is the JSON body of a failed console request, using the same codes
// as the chat protocol.
type adminError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	// RetryAfterMS is how long until a rate-limited request may be retried.
	RetryAfterMS int `json:"retry_after_ms,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, adminError{Code: code, Message: protocol.DefaultMessage(code)})
}

func writeErrMsg(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, adminError{Code: code, Message: msg})
}

// adminSession is the signed-in administrator of one request.
type adminSession struct {
	user      storage.User
	perms     map[string]bool
	tokenHash string
}

func (a adminSession) actor() Actor { return Actor{ID: a.user.ID, Name: a.user.Username} }

// adminHandler is a console endpoint that runs only for a signed-in
// administrator holding the endpoint's permission.
type adminHandler func(w http.ResponseWriter, r *http.Request, a adminSession)

// adminAuth wraps h with authentication and, when perm is non-empty, the
// permission check. Writes additionally require the CSRF header and a JSON
// body.
func (s *Server) adminAuth(perm string, h adminHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.checkWriteRequest(w, r) {
			return
		}
		a, ok := s.adminSessionFor(w, r)
		if !ok {
			return
		}
		if perm != "" && !a.perms[perm] {
			writeErr(w, http.StatusForbidden, protocol.CodeForbidden)
			return
		}
		h(w, r, a)
	}
}

// checkWriteRequest enforces the CSRF header and a JSON body on writes.
func (s *Server) checkWriteRequest(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return true
	}
	if r.Header.Get(adminCSRFHeader) != adminCSRFValue {
		writeErr(w, http.StatusForbidden, protocol.CodeForbidden)
		return false
	}
	if r.ContentLength != 0 {
		mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mt != "application/json" {
			writeErr(w, http.StatusUnsupportedMediaType, protocol.CodeInvalidArgument)
			return false
		}
	}
	return true
}

// adminSessionFor resolves the cookie to a current administrator, or writes the
// failure.
func (s *Server) adminSessionFor(w http.ResponseWriter, r *http.Request) (adminSession, bool) {
	ck, err := r.Cookie(adminCookie)
	if err != nil || ck.Value == "" {
		writeErr(w, http.StatusUnauthorized, protocol.CodeUnauthorized)
		return adminSession{}, false
	}
	uid, perr := s.userForToken(ck.Value, storage.PurposeAdmin)
	if perr != nil {
		writeErr(w, http.StatusUnauthorized, perr.Code)
		return adminSession{}, false
	}
	u, err := s.st.UserByID(uid)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, protocol.CodeUnauthorized)
		return adminSession{}, false
	}
	// Re-checked on every request: losing every permission, or being banned,
	// ends console access immediately (spec admin-console / 按请求实时校验权限).
	if s.isBanned(u.ID) {
		_ = s.st.DeleteTokensForUserPurpose(u.ID, storage.PurposeAdmin)
		writeErr(w, http.StatusUnauthorized, protocol.CodeAccountBanned)
		return adminSession{}, false
	}
	perms := effectivePerms(u)
	if len(perms) == 0 {
		writeErr(w, http.StatusForbidden, protocol.CodeForbidden)
		return adminSession{}, false
	}
	return adminSession{user: u, perms: perms, tokenHash: tokenHash(ck.Value)}, true
}

type adminLoginReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// handleAdminLogin signs in to the console. It shares the chat login's rate
// limiter, keyed by source address, so the console is no cheaper a place to
// guess passwords.
func (s *Server) handleAdminLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, protocol.CodeInvalidArgument)
		return
	}
	if !s.checkWriteRequest(w, r) {
		return
	}
	remote := s.clientAddr(r)
	// Keyed by host alone: a browser opens new connections freely, so a key
	// that includes the port would hand every connection a fresh budget.
	key := hostOnly(remote)
	if ok, wait := s.loginLimit.Allow(key); !ok {
		w.Header().Set("Retry-After", itoa(int(wait.Seconds())+1))
		writeJSON(w, http.StatusTooManyRequests, adminError{
			Code:         protocol.CodeRateLimited,
			Message:      protocol.DefaultMessage(protocol.CodeRateLimited),
			RetryAfterMS: retryAfterMS(wait),
		})
		return
	}
	var req adminLoginReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, protocol.CodeInvalidArgument)
		return
	}
	u, ok := s.checkPassword(key, remote, req.Username, req.Password)
	if !ok {
		writeErr(w, http.StatusUnauthorized, protocol.CodeInvalidCredentials)
		return
	}
	s.loginLimit.Succeed(key)
	if s.isBanned(u.ID) {
		writeErr(w, http.StatusForbidden, protocol.CodeAccountBanned)
		return
	}
	if !isAdmin(u) {
		s.log.Info("admin login refused", "user", u.Username, "addr", remote, "reason", "no permissions")
		writeErr(w, http.StatusForbidden, protocol.CodeForbidden)
		return
	}

	raw, err := newToken()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, protocol.CodeInternal)
		return
	}
	expires := time.Now().Add(adminTokenTTL)
	// Purpose admin: this never displaces, and is never displaced by, a chat
	// session (spec login-session / 后台会话不受单会话约束).
	if err := s.st.SaveToken(storage.Token{
		Hash:       tokenHash(raw),
		UserID:     u.ID,
		CreatedAt:  storage.Now(),
		ExpiresAt:  expires.UnixMilli(),
		Purpose:    storage.PurposeAdmin,
		Remote:     remote,
		Device:     "admin console",
		LastSeenAt: storage.Now(),
	}); err != nil {
		writeErr(w, http.StatusServiceUnavailable, protocol.CodeStorageUnavailable)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     adminCookie,
		Value:    raw,
		Path:     "/admin/",
		Expires:  expires,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   s.isHTTPS(r),
	})
	s.log.Info("admin login", "user", u.Username, "addr", remote)
	s.audit(Actor{ID: u.ID, Name: u.Username}, "admin_login", "", map[string]string{"addr": remote})
	writeJSON(w, http.StatusOK, s.meFor(u))
}

// checkPassword verifies a username and password, counting failures against
// the login limiter. Unknown users and wrong passwords are indistinguishable.
func (s *Server) checkPassword(key, remote, username, password string) (storage.User, bool) {
	name, err := validateUsername(username)
	if err != nil {
		s.loginLimit.Fail(key)
		return storage.User{}, false
	}
	u, err := s.st.UserByUsernameKey(strings.ToLower(name))
	if err != nil {
		s.loginLimit.Fail(key)
		return storage.User{}, false
	}
	ok, _, err := verifyPassword(password, u.PasswordHash)
	if err != nil || !ok {
		s.loginLimit.Fail(key)
		s.log.Info("admin login failed", "addr", remote, "user", u.Username)
		return storage.User{}, false
	}
	return u, true
}

// handleAdminLogout ends this console session.
func (s *Server) handleAdminLogout(w http.ResponseWriter, r *http.Request, a adminSession) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, protocol.CodeInvalidArgument)
		return
	}
	_ = s.st.DeleteToken(a.tokenHash)
	http.SetCookie(w, &http.Cookie{
		Name: adminCookie, Value: "", Path: "/admin/", MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: s.isHTTPS(r),
	})
	s.audit(a.actor(), "admin_logout", "", nil)
	writeJSON(w, http.StatusOK, struct{}{})
}

// hostOnly strips the port from an address, leaving it as is when there is none.
func hostOnly(addr string) string {
	if h, _, err := net.SplitHostPort(addr); err == nil {
		return h
	}
	return addr
}

// isHTTPS reports whether the browser reached us over TLS, directly or through
// a trusted proxy, which decides the cookie's Secure flag.
func (s *Server) isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return s.trustedProxy(peerAddress(r.RemoteAddr)) && strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https")
}

// meRes describes the signed-in administrator to the page.
type meRes struct {
	ID         string   `json:"id"`
	Username   string   `json:"username"`
	Super      bool     `json:"super"`
	Perms      []string `json:"perms"`
	All        []string `json:"all_perms"`
	Simulation bool     `json:"simulation,omitempty"`
}

func (s *Server) meFor(u storage.User) meRes {
	return meRes{ID: u.ID, Username: u.Username, Super: u.Super, Perms: permList(effectivePerms(u)), All: AllPerms, Simulation: s.simulation.enabled.Load() && u.Super}
}

var errNoBody = errors.New("empty body")

// decodeBody reads a small JSON request body.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) error {
	if r.ContentLength == 0 {
		return errNoBody
	}
	return json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(v)
}

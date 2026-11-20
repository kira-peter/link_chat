package server

import (
	"errors"
	"strings"
	"time"

	"link-chat/pkg/protocol"
	"link-chat/pkg/storage"
)

// Moderation actions (spec account-moderation). Each one is a single method
// the admin API calls; the method enforces the rules that do not depend on who
// is asking (a reason is required, the super administrator is untouchable) and
// writes the audit entry. Permission checks live in the API layer.

// Actor is who performed an administrative action, for the audit log.
type Actor struct {
	ID   string
	Name string
}

// CLIActor is the actor recorded for actions taken from the server's command
// line, where there is no signed-in account.
var CLIActor = Actor{ID: "cli", Name: "cli"}

// Moderation errors, mapped to HTTP statuses by the admin API.
var (
	ErrReasonRequired = errors.New("a reason is required")
	ErrProtectedUser  = errors.New("the super administrator cannot be moderated")
	ErrNotActive      = errors.New("the sanction is not in force")
	ErrBadDuration    = errors.New("invalid duration")
)

// audit appends one entry, logging rather than failing when the write does not
// land: the action itself has already happened.
func (s *Server) audit(a Actor, action, target string, detail map[string]string) {
	if err := s.st.AppendAudit(storage.AuditEntry{
		At:        storage.Now(),
		ActorID:   a.ID,
		ActorName: a.Name,
		Action:    action,
		Target:    target,
		Detail:    detail,
	}); err != nil {
		s.log.Error("audit write failed", "err", err, "action", action)
	}
}

// moderationTarget loads the user an action applies to and refuses the super
// administrator.
func (s *Server) moderationTarget(uid string) (storage.User, error) {
	u, err := s.st.UserByID(uid)
	if err != nil {
		return storage.User{}, err
	}
	if u.Super {
		return storage.User{}, ErrProtectedUser
	}
	return u, nil
}

// Sanction bans or mutes a user. A zero duration is permanent.
func (s *Server) Sanction(a Actor, uid, kind, reason string, d time.Duration) (storage.Sanction, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return storage.Sanction{}, ErrReasonRequired
	}
	if d < 0 {
		return storage.Sanction{}, ErrBadDuration
	}
	if kind != storage.SanctionBan && kind != storage.SanctionMute {
		return storage.Sanction{}, ErrBadDuration
	}
	u, err := s.moderationTarget(uid)
	if err != nil {
		return storage.Sanction{}, err
	}
	now := storage.Now()
	sc := storage.Sanction{
		UserID:    u.ID,
		Kind:      kind,
		Reason:    reason,
		CreatedBy: a.ID,
		CreatedAt: now,
	}
	if d > 0 {
		sc.ExpiresAt = now + d.Milliseconds()
	}
	sc, err = s.st.AddSanction(sc)
	if err != nil {
		return storage.Sanction{}, err
	}
	info := protocol.SanctionInfo{Reason: sc.Reason, ExpiresAt: sc.ExpiresAt}

	switch kind {
	case storage.SanctionBan:
		// Revoke every credential first, so nothing can resume in the gap
		// before the sockets close.
		if err := s.st.DeleteSessionTokensForUser(u.ID); err != nil {
			s.log.Warn("revoke sessions on ban", "err", err, "user", u.Username)
		}
		if err := s.st.DeleteTokensForUserPurpose(u.ID, storage.PurposeAdmin); err != nil {
			s.log.Warn("revoke admin tokens on ban", "err", err, "user", u.Username)
		}
		closed := s.notifyAndClose(u.ID, nil, protocol.EvAccountBanned, info)
		s.closeAdminStreams(u.ID)
		s.log.Info("user banned", "user", u.Username, "by", a.Name, "until", untilLabel(sc.ExpiresAt), "closed", closed)
	case storage.SanctionMute:
		s.pushToUser(u.ID, protocol.EvAccountMuted, info)
		s.log.Info("user muted", "user", u.Username, "by", a.Name, "until", untilLabel(sc.ExpiresAt))
	}

	s.audit(a, kind, u.ID, map[string]string{
		"username": u.Username,
		"reason":   sc.Reason,
		"until":    untilLabel(sc.ExpiresAt),
	})
	s.adminNotifySanction(sc)
	return sc, nil
}

// RevokeSanction ends a ban or mute early.
func (s *Server) RevokeSanction(a Actor, sanctionID string) (storage.Sanction, error) {
	sc, err := s.st.RevokeSanction(sanctionID, a.ID, storage.Now())
	if err != nil {
		if errors.Is(err, storage.ErrConflict) {
			return storage.Sanction{}, ErrNotActive
		}
		return storage.Sanction{}, err
	}
	if sc.Kind == storage.SanctionMute {
		s.pushToUser(sc.UserID, protocol.EvAccountUnmuted, struct{}{})
	}
	s.log.Info("sanction revoked", "user", s.userName(sc.UserID), "kind", sc.Kind, "by", a.Name)
	s.audit(a, "revoke", sc.UserID, map[string]string{
		"username": s.userName(sc.UserID),
		"kind":     sc.Kind,
		"sanction": sc.ID,
	})
	s.adminNotifySanction(sc)
	return sc, nil
}

// ResetPassword sets a new password for a user and ends all their sessions,
// as a self-service reset does (spec account-moderation / 管理员重置密码).
// The new password is never logged or audited.
func (s *Server) ResetPassword(a Actor, uid, newPassword string, allowSuper bool) error {
	if err := validatePassword(newPassword); err != nil {
		return err
	}
	var (
		u   storage.User
		err error
	)
	if allowSuper {
		u, err = s.st.UserByID(uid)
	} else {
		u, err = s.moderationTarget(uid)
	}
	if err != nil {
		return err
	}
	hash, err := hashPassword(newPassword, defaultArgonParams)
	if err != nil {
		return err
	}
	if err := s.st.UpdateUserPasswordHash(u.ID, hash); err != nil {
		return err
	}
	if err := s.st.DeleteSessionTokensForUser(u.ID); err != nil {
		return err
	}
	if err := s.st.DeleteTokensForUserPurpose(u.ID, storage.PurposeAdmin); err != nil {
		return err
	}
	s.notifyAndClose(u.ID, nil, protocol.EvSessionReplaced, protocol.SessionReplacedEvent{
		Username: u.Username,
		Location: adminLocation,
		At:       storage.Now(),
	})
	s.closeAdminStreams(u.ID)
	s.log.Info("password reset by admin", "user", u.Username, "by", a.Name)
	s.audit(a, "reset_password", u.ID, map[string]string{"username": u.Username})
	return nil
}

// adminLocation is the "where" shown to someone an administrator signed out.
const adminLocation = "管理员 / administrator"

// Kick ends a user's chat sessions without sanctioning them; they can sign in
// again at once (spec account-moderation / 强制下线).
func (s *Server) Kick(a Actor, uid string) (int, error) {
	u, err := s.moderationTarget(uid)
	if err != nil {
		return 0, err
	}
	if err := s.st.DeleteSessionTokensForUser(u.ID); err != nil {
		return 0, err
	}
	n := s.notifyAndClose(u.ID, nil, protocol.EvSessionReplaced, protocol.SessionReplacedEvent{
		Username: u.Username,
		Location: adminLocation,
		At:       storage.Now(),
	})
	s.log.Info("user kicked", "user", u.Username, "by", a.Name, "closed", n)
	s.audit(a, "kick", u.ID, map[string]string{"username": u.Username})
	return n, nil
}

// pushToUser queues an event to every connection of a user.
func (s *Server) pushToUser(uid, name string, data any) {
	f, err := protocol.NewEvent(name, data)
	if err != nil {
		s.log.Error("encode event", "event", name, "err", err)
		return
	}
	s.hub.broadcastToUsers([]string{uid}, f)
}

// untilLabel renders an expiry for logs and the audit log.
func untilLabel(expiresAt int64) string {
	if expiresAt == 0 {
		return "permanent"
	}
	return time.UnixMilli(expiresAt).UTC().Format(time.RFC3339)
}

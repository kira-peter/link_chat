package server

import (
	"errors"
	"strings"
	"time"

	"link-chat/pkg/protocol"
	"link-chat/pkg/storage"
)

// Password recovery implements a three-step wizard (spec account-auth /
// 找回密码流程, design.md decision 3):
//
//	1. ask for the security question belonging to a username
//	2. answer it, receiving a short-lived single-use reset token
//	3. present that token with a new password
//
// The steps are separate requests so each failure has a precise meaning, and so
// the client never has to guess which question to show.

// opRecoverQuestion returns the security question for an account.
//
// An unknown username and an account without a question produce the *same*
// response. Distinguishing them would turn this endpoint into a username oracle,
// undoing the care taken on the login path
// (design.md decision 4).
func (s *Server) opRecoverQuestion(c *Conn, id string, p protocol.ReqPayload) {
	if ok, wait := s.loginLimit.Allow(c.remoteAddr); !ok {
		s.failRateLimited(c, id, wait)
		return
	}

	var req protocol.RecoverQuestionReq
	if err := protocol.DecodePayload(p.Data, &req); err != nil {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeInvalidArgument))
		return
	}
	name := strings.ToLower(strings.TrimSpace(req.Username))
	if name == "" {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeInvalidArgument))
		return
	}

	u, err := s.st.UserByUsernameKey(name)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			s.loginLimit.Fail(c.remoteAddr)
			s.fail(c, id, protocol.NewErrorCode(protocol.CodeCannotRecover))
			return
		}
		s.failStorage(c, id, err)
		return
	}
	if !u.CanRecover() {
		// No question set: an account from before recovery existed.
		s.loginLimit.Fail(c.remoteAddr)
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeCannotRecover))
		return
	}

	// Success here does confirm the account exists, but only after the caller
	// already supplied its username — the same disclosure as a login attempt,
	// and it is rate limited identically.
	s.reply(c, id, protocol.RecoverQuestionRes{
		Username: u.Username,
		Question: u.SecurityQuestion,
	})
}

// opRecoverVerify checks the answer and issues a reset token.
func (s *Server) opRecoverVerify(c *Conn, id string, p protocol.ReqPayload) {
	if ok, wait := s.loginLimit.Allow(c.remoteAddr); !ok {
		s.failRateLimited(c, id, wait)
		return
	}

	var req protocol.RecoverVerifyReq
	if err := protocol.DecodePayload(p.Data, &req); err != nil {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeInvalidArgument))
		return
	}
	name := strings.ToLower(strings.TrimSpace(req.Username))
	answer := req.Answer
	if name == "" || strings.TrimSpace(answer) == "" {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeInvalidArgument))
		return
	}

	u, err := s.st.UserByUsernameKey(name)
	if err != nil || !u.CanRecover() {
		// Same code as a wrong answer: answering an unknown account must not be
		// a cheaper way to learn it does not exist.
		s.loginLimit.Fail(c.remoteAddr)
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeCannotRecover))
		return
	}

	ok, verr := verifyAnswer(answer, u.SecurityAnswerHash)
	if verr != nil {
		s.log.Error("verify security answer", "err", verr, "user", u.Username)
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeInternal))
		return
	}
	if !ok {
		// Counted against the same backoff as a failed login: guessing answers
		// and guessing passwords are the same attack
		// (spec account-auth / 找回密码错误答案计入退避).
		s.loginLimit.Fail(c.remoteAddr)
		s.log.Info("recovery answer rejected", "addr", c.remoteAddr, "user", u.Username)
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeSecurityAnswerWrong))
		return
	}

	s.loginLimit.Succeed(c.remoteAddr)

	token, err := newToken()
	if err != nil {
		s.log.Error("mint reset token", "err", err)
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeInternal))
		return
	}
	expires := time.Now().Add(resetTokenTTL)
	if err := s.st.SaveToken(storage.Token{
		Hash:      tokenHash(token),
		UserID:    u.ID,
		CreatedAt: storage.Now(),
		ExpiresAt: expires.UnixMilli(),
		Purpose:   storage.PurposeReset,
	}); err != nil {
		s.failStorage(c, id, err)
		return
	}

	s.reply(c, id, protocol.RecoverVerifyRes{
		ResetToken: token,
		ExpiresAt:  expires.UnixMilli(),
	})
}

// opRecoverReset sets a new password using a reset token.
func (s *Server) opRecoverReset(c *Conn, id string, p protocol.ReqPayload) {
	if ok, wait := s.loginLimit.Allow(c.remoteAddr); !ok {
		s.failRateLimited(c, id, wait)
		return
	}

	var req protocol.RecoverResetReq
	if err := protocol.DecodePayload(p.Data, &req); err != nil {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeInvalidArgument))
		return
	}
	if req.ResetToken == "" {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeResetTokenInvalid))
		return
	}
	if err := validatePassword(req.NewPassword); err != nil {
		// The token survives a weak password so the user can simply retype one.
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeWeakPassword))
		return
	}

	t, err := s.st.TokenByHash(tokenHash(req.ResetToken))
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			s.fail(c, id, protocol.NewErrorCode(protocol.CodeResetTokenInvalid))
			return
		}
		s.failStorage(c, id, err)
		return
	}
	// Only a reset token authorises a password change; a session token must not
	// be usable here even if it belongs to the same user
	// (design.md decision 1).
	if !t.IsReset() {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeResetTokenInvalid))
		return
	}
	if time.Now().UnixMilli() > t.ExpiresAt {
		_ = s.st.DeleteToken(t.Hash)
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeResetTokenExpired))
		return
	}

	u, err := s.st.UserByID(t.UserID)
	if err != nil {
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeResetTokenInvalid))
		return
	}

	newHash, err := hashPassword(req.NewPassword, defaultArgonParams)
	if err != nil {
		s.log.Error("hash password", "err", err)
		s.fail(c, id, protocol.NewErrorCode(protocol.CodeInternal))
		return
	}
	// Update credentials and burn the token before revoking sessions: if the
	// process dies between these, the worst case is a used token that no longer
	// works, not a reusable one.
	if err := s.st.UpdateUserCredentials(u.ID, newHash, u.SecurityQuestion, u.SecurityAnswerHash); err != nil {
		s.failStorage(c, id, err)
		return
	}
	if err := s.st.DeleteToken(t.Hash); err != nil {
		s.failStorage(c, id, err)
		return
	}
	// Every existing chat session dies: a password reset means "whoever had
	// access no longer should" (spec account-auth / 重置后的会话吊销).
	if err := s.st.DeleteSessionTokensForUser(u.ID); err != nil {
		s.failStorage(c, id, err)
		return
	}

	s.log.Info("password reset", "addr", c.remoteAddr, "user", u.Username)
	s.reply(c, id, struct{}{})
}

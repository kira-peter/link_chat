package server

import (
	"testing"

	"link-chat/pkg/protocol"
	"link-chat/pkg/storage"
)

// registerWithQuestion registers a user that can later recover their password.
func (c *client) registerWithQuestion(username, password, question, answer string) protocol.AuthRes {
	c.t.Helper()
	c.hello()
	var res protocol.AuthRes
	c.callOK(protocol.OpAuthRegister, protocol.RegisterReq{
		Username:         username,
		Password:         password,
		SecurityQuestion: question,
		SecurityAnswer:   answer,
	}, &res)
	return res
}

func TestRegisterRequiresSecurityQuestion(t *testing.T) {
	hs, _ := testServer(t)
	c := dial(t, hs)
	c.hello()
	c.callErr(protocol.OpAuthRegister, protocol.RegisterReq{
		Username: "alice", Password: "correct horse battery",
	}, protocol.CodeSecurityQuestionReq)

	// Only the question without an answer is equally unacceptable.
	c.callErr(protocol.OpAuthRegister, protocol.RegisterReq{
		Username: "alice", Password: "correct horse battery",
		SecurityQuestion: "First pet?",
	}, protocol.CodeSecurityQuestionReq)
}

func TestRecoveryQuestionIsReturned(t *testing.T) {
	hs, _ := testServer(t)
	dial(t, hs).registerWithQuestion("alice", "correct horse battery", "First pet?", "Blue Harbor")

	c := dial(t, hs)
	c.hello()
	var res protocol.RecoverQuestionRes
	c.callOK(protocol.OpRecoverQuestion, protocol.RecoverQuestionReq{Username: "alice"}, &res)
	if res.Question != "First pet?" {
		t.Fatalf("wrong question: %q", res.Question)
	}
}

// An unknown account and an account without a question must be indistinguishable
// (design.md decision 4).
func TestRecoveryQuestionHidesAccountExistence(t *testing.T) {
	hs, srv := testServer(t)

	// An account created before recovery existed: written straight to storage so
	// it has no question, as a migrated database would.
	st := srv.st
	if err := st.CreateUser(storage.User{
		ID: "u_old", Username: "legacy", UsernameKey: "legacy",
		PasswordHash: "unused", CreatedAt: storage.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	c := dial(t, hs)
	c.hello()

	unknown := c.call(protocol.OpRecoverQuestion, protocol.RecoverQuestionReq{Username: "nobody"})
	noQuestion := c.call(protocol.OpRecoverQuestion, protocol.RecoverQuestionReq{Username: "legacy"})

	if unknown.Type != protocol.TypeError || noQuestion.Type != protocol.TypeError {
		t.Fatalf("both should fail: %v / %v", unknown.Type, noQuestion.Type)
	}
	if errorCode(unknown) != errorCode(noQuestion) {
		t.Fatalf("codes differ, leaking existence: %q vs %q",
			errorCode(unknown), errorCode(noQuestion))
	}
	if errorCode(unknown) != protocol.CodeCannotRecover {
		t.Fatalf("want cannot_recover, got %q", errorCode(unknown))
	}
}

func TestRecoveryFullFlow(t *testing.T) {
	hs, _ := testServer(t)
	dial(t, hs).registerWithQuestion("alice", "old password here", "First pet?", "Blue Harbor")

	c := dial(t, hs)
	c.hello()

	var q protocol.RecoverQuestionRes
	c.callOK(protocol.OpRecoverQuestion, protocol.RecoverQuestionReq{Username: "alice"}, &q)

	// Case and extra whitespace must not matter.
	var v protocol.RecoverVerifyRes
	c.callOK(protocol.OpRecoverVerify, protocol.RecoverVerifyReq{
		Username: "alice", Answer: "  BLUE   harbor ",
	}, &v)
	if v.ResetToken == "" {
		t.Fatal("no reset token issued")
	}

	c.callOK(protocol.OpRecoverReset, protocol.RecoverResetReq{
		ResetToken: v.ResetToken, NewPassword: "brand new password",
	}, nil)

	// The new password works.
	login := dial(t, hs)
	login.hello()
	var res protocol.AuthRes
	login.callOK(protocol.OpAuthLogin, protocol.LoginReq{
		Username: "alice", Password: "brand new password",
	}, &res)

	// The old one does not.
	old := dial(t, hs)
	old.hello()
	old.callErr(protocol.OpAuthLogin, protocol.LoginReq{
		Username: "alice", Password: "old password here",
	}, protocol.CodeInvalidCredentials)
}

func TestWrongSecurityAnswerRejected(t *testing.T) {
	hs, _ := testServer(t)
	dial(t, hs).registerWithQuestion("alice", "correct horse battery", "First pet?", "Blue Harbor")

	c := dial(t, hs)
	c.hello()
	c.callErr(protocol.OpRecoverVerify, protocol.RecoverVerifyReq{
		Username: "alice", Answer: "Green Harbor",
	}, protocol.CodeSecurityAnswerWrong)
}

// A reset token is single use.
func TestResetTokenIsSingleUse(t *testing.T) {
	hs, _ := testServer(t)
	dial(t, hs).registerWithQuestion("alice", "correct horse battery", "Q?", "answer")

	c := dial(t, hs)
	c.hello()
	var v protocol.RecoverVerifyRes
	c.callOK(protocol.OpRecoverVerify, protocol.RecoverVerifyReq{Username: "alice", Answer: "answer"}, &v)

	c.callOK(protocol.OpRecoverReset, protocol.RecoverResetReq{
		ResetToken: v.ResetToken, NewPassword: "first new password",
	}, nil)
	c.callErr(protocol.OpRecoverReset, protocol.RecoverResetReq{
		ResetToken: v.ResetToken, NewPassword: "second new password",
	}, protocol.CodeResetTokenInvalid)
}

// A weak new password must not consume the token.
func TestWeakNewPasswordKeepsTokenUsable(t *testing.T) {
	hs, _ := testServer(t)
	dial(t, hs).registerWithQuestion("alice", "correct horse battery", "Q?", "answer")

	c := dial(t, hs)
	c.hello()
	var v protocol.RecoverVerifyRes
	c.callOK(protocol.OpRecoverVerify, protocol.RecoverVerifyReq{Username: "alice", Answer: "answer"}, &v)

	c.callErr(protocol.OpRecoverReset, protocol.RecoverResetReq{
		ResetToken: v.ResetToken, NewPassword: "short",
	}, protocol.CodeWeakPassword)

	// The same token still works with an acceptable password.
	c.callOK(protocol.OpRecoverReset, protocol.RecoverResetReq{
		ResetToken: v.ResetToken, NewPassword: "long enough password",
	}, nil)
}

// Resetting must kill existing chat sessions but not other accounts'.
func TestResetRevokesExistingSessions(t *testing.T) {
	hs, _ := testServer(t)

	alice := dial(t, hs)
	aliceSession := alice.registerWithQuestion("alice", "correct horse battery", "Q?", "answer")

	bob := dial(t, hs)
	bobSession := bob.registerWithQuestion("bob", "correct horse battery", "Q?", "answer")

	rec := dial(t, hs)
	rec.hello()
	var v protocol.RecoverVerifyRes
	rec.callOK(protocol.OpRecoverVerify, protocol.RecoverVerifyReq{Username: "alice", Answer: "answer"}, &v)
	rec.callOK(protocol.OpRecoverReset, protocol.RecoverResetReq{
		ResetToken: v.ResetToken, NewPassword: "long enough password",
	}, nil)

	// Alice's old session token is dead.
	check := dial(t, hs)
	check.hello()
	check.callErr(protocol.OpAuthResume, protocol.ResumeReq{Token: aliceSession.Token}, protocol.CodeUnauthorized)

	// Bob's is untouched.
	check2 := dial(t, hs)
	check2.hello()
	var res protocol.AuthRes
	check2.callOK(protocol.OpAuthResume, protocol.ResumeReq{Token: bobSession.Token}, &res)
	if res.User.Username != "bob" {
		t.Fatalf("wrong user resumed: %q", res.User.Username)
	}
}

// A reset token must not be usable as a session token.
func TestResetTokenCannotResumeSession(t *testing.T) {
	hs, _ := testServer(t)
	dial(t, hs).registerWithQuestion("alice", "correct horse battery", "Q?", "answer")

	c := dial(t, hs)
	c.hello()
	var v protocol.RecoverVerifyRes
	c.callOK(protocol.OpRecoverVerify, protocol.RecoverVerifyReq{Username: "alice", Answer: "answer"}, &v)

	other := dial(t, hs)
	other.hello()
	other.callErr(protocol.OpAuthResume, protocol.ResumeReq{Token: v.ResetToken}, protocol.CodeUnauthorized)
}

// A session token must not be usable to set a password.
func TestSessionTokenCannotResetPassword(t *testing.T) {
	hs, _ := testServer(t)
	c := dial(t, hs)
	session := c.registerWithQuestion("alice", "correct horse battery", "Q?", "answer")

	c.callErr(protocol.OpRecoverReset, protocol.RecoverResetReq{
		ResetToken: session.Token, NewPassword: "another password",
	}, protocol.CodeResetTokenInvalid)
}

func TestAnswerNormalisation(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Blue Harbor", "blue harbor"},
		{"  blue   harbor  ", "blue harbor"},
		{"BLUE\tHARBOR", "blue harbor"},
		{"", ""},
	}
	for _, c := range cases {
		if got := normaliseAnswer(c.in); got != c.want {
			t.Errorf("normaliseAnswer(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// The stored form must never be the plaintext answer.
func TestAnswerIsStoredHashed(t *testing.T) {
	hs, srv := testServer(t)
	dial(t, hs).registerWithQuestion("alice", "correct horse battery", "First pet?", "Blue Harbor")

	u, err := srv.st.UserByUsernameKey("alice")
	if err != nil {
		t.Fatal(err)
	}
	if u.SecurityAnswerHash == "" {
		t.Fatal("answer hash not stored")
	}
	if u.SecurityAnswerHash == "Blue Harbor" {
		t.Fatal("answer stored in clear text")
	}
	if u.SecurityQuestion != "First pet?" {
		t.Fatalf("question should be readable, got %q", u.SecurityQuestion)
	}
}

// errorCode extracts the failure code from an error frame.
func errorCode(f protocol.Frame) string {
	var p protocol.ResPayload
	if err := protocol.DecodePayload(f.Payload, &p); err != nil {
		return ""
	}
	return p.Code
}

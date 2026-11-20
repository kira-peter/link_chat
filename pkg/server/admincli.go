package server

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"link-chat/pkg/storage"
)

// Command-line administration (spec server-runtime / 命令行创建超级管理员,
// 命令行重置密码). These run against the data directory while the server is
// stopped; the caller holds the directory lock. Passwords come from a reader
// function so the terminal prompt can be swapped out in tests.

// PasswordPrompt asks for a password without echoing it.
type PasswordPrompt func(prompt string) (string, error)

// ErrPasswordMismatch is returned when the two entries differ.
var ErrPasswordMismatch = errors.New("the two passwords do not match")

// askNewPassword reads a password twice and checks it.
func askNewPassword(ask PasswordPrompt) (string, error) {
	p1, err := ask("New password: ")
	if err != nil {
		return "", err
	}
	p2, err := ask("Repeat password: ")
	if err != nil {
		return "", err
	}
	if p1 != p2 {
		return "", ErrPasswordMismatch
	}
	if err := validatePassword(p1); err != nil {
		return "", fmt.Errorf("password must be at least 8 characters")
	}
	return p1, nil
}

// cliServer is just enough of a Server to run moderation code against a store
// with nothing listening. Its log is discarded: the command prints its own
// one-line result, and the audit log records what happened.
func cliServer(st storage.Storage) *Server {
	return New(DefaultConfig(), st, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// CreateAdminResult says what CreateAdmin did.
type CreateAdminResult struct {
	Created  bool // a new account was made; otherwise an existing one was promoted
	Username string
}

// CreateAdmin makes username the super administrator: it creates the account
// when it does not exist, asking for its password, and otherwise promotes it
// without touching its password.
func CreateAdmin(st storage.Storage, username string, ask PasswordPrompt) (CreateAdminResult, error) {
	name, err := validateUsername(username)
	if err != nil {
		return CreateAdminResult{}, fmt.Errorf("username %q is not allowed: use 3-32 letters, digits, _ - .", username)
	}
	s := cliServer(st)
	u, err := st.UserByUsernameKey(strings.ToLower(name))
	switch {
	case err == nil:
		if u.Super {
			return CreateAdminResult{Username: u.Username}, nil
		}
		if err := st.SetUserSuper(u.ID, true); err != nil {
			return CreateAdminResult{}, err
		}
		s.audit(CLIActor, "promote_admin", u.ID, map[string]string{"username": u.Username})
		return CreateAdminResult{Username: u.Username}, nil
	case errors.Is(err, storage.ErrNotFound):
		pw, err := askNewPassword(ask)
		if err != nil {
			return CreateAdminResult{}, err
		}
		u, err := s.CreateAccount(CLIActor, name, pw, nil, true)
		if err != nil {
			return CreateAdminResult{}, err
		}
		return CreateAdminResult{Created: true, Username: u.Username}, nil
	default:
		return CreateAdminResult{}, err
	}
}

// ResetPasswordCLI sets a new password for any account, the super
// administrator included, and ends all of its sessions.
func ResetPasswordCLI(st storage.Storage, username string, ask PasswordPrompt) error {
	u, err := st.UserByUsernameKey(strings.ToLower(strings.TrimSpace(username)))
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return fmt.Errorf("no account named %q", username)
		}
		return err
	}
	pw, err := askNewPassword(ask)
	if err != nil {
		return err
	}
	if err := cliServer(st).ResetPassword(CLIActor, u.ID, pw, true); err != nil {
		return err
	}
	// The CLI also drops reset tokens: "every token" is the promise here.
	return st.DeleteTokensForUser(u.ID)
}

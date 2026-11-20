package server

import (
	"errors"
	"sort"

	"link-chat/pkg/storage"
)

// Admin console permissions (spec admin-console / 管理权限位).
const (
	PermStatsView      = "stats.view"
	PermChatView       = "chat.view"
	PermUsersModerate  = "users.moderate"
	PermAccountsManage = "accounts.manage"
)

// AllPerms lists every permission in display order.
var AllPerms = []string{PermStatsView, PermChatView, PermUsersModerate, PermAccountsManage}

func isPerm(p string) bool {
	for _, q := range AllPerms {
		if p == q {
			return true
		}
	}
	return false
}

// effectivePerms is what a user may actually do: the super administrator holds
// every permission whatever is stored; anyone else holds the known ones they
// were granted.
func effectivePerms(u storage.User) map[string]bool {
	out := map[string]bool{}
	if u.Super {
		for _, p := range AllPerms {
			out[p] = true
		}
		return out
	}
	for _, p := range u.Perms {
		if isPerm(p) {
			out[p] = true
		}
	}
	return out
}

func hasPerm(u storage.User, p string) bool { return effectivePerms(u)[p] }

// isAdmin reports whether the user may sign in to the console at all.
func isAdmin(u storage.User) bool { return len(effectivePerms(u)) > 0 }

// permList returns a set as a sorted slice.
func permList(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// Grant errors. All map to forbidden except an unknown permission.
var (
	ErrGrantSelf        = errors.New("you cannot change your own permissions")
	ErrGrantSuper       = errors.New("the super administrator's permissions cannot be changed")
	ErrGrantNotHeld     = errors.New("you can only grant permissions you hold")
	ErrGrantManage      = errors.New("only the super administrator can grant or revoke accounts.manage")
	ErrGrantUnknown     = errors.New("unknown permission")
	ErrGrantNeedsManage = errors.New("changing permissions requires accounts.manage")
)

// canGrant decides whether actor may set target's permissions to newPerms
// (spec admin-console / 分配权限). It is a pure function so every rule can be
// tested exhaustively.
//
// The "only grant what you hold" rule applies to permissions being added; a
// manager may remove a permission they do not hold themselves, because taking
// access away never extends the manager's own reach. accounts.manage is the
// exception: only the super administrator may move it either way.
func canGrant(actor, target storage.User, newPerms []string) error {
	act := effectivePerms(actor)
	if !act[PermAccountsManage] {
		return ErrGrantNeedsManage
	}
	if actor.ID == target.ID {
		return ErrGrantSelf
	}
	if target.Super {
		return ErrGrantSuper
	}
	want := map[string]bool{}
	for _, p := range newPerms {
		if !isPerm(p) {
			return ErrGrantUnknown
		}
		want[p] = true
	}
	had := effectivePerms(target)
	if want[PermAccountsManage] != had[PermAccountsManage] && !actor.Super {
		return ErrGrantManage
	}
	for p := range want {
		if !had[p] && !act[p] {
			return ErrGrantNotHeld
		}
	}
	return nil
}

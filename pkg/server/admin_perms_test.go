package server

import (
	"errors"
	"testing"

	"link-chat/pkg/storage"
)

func TestCanGrant(t *testing.T) {
	super := storage.User{ID: "root", Super: true}
	mgr := storage.User{ID: "mgr", Perms: []string{PermAccountsManage, PermStatsView}}
	viewer := storage.User{ID: "v", Perms: []string{PermStatsView}}
	plain := storage.User{ID: "p"}
	chatty := storage.User{ID: "c", Perms: []string{PermChatView}}
	otherMgr := storage.User{ID: "m2", Perms: []string{PermAccountsManage}}

	cases := []struct {
		name          string
		actor, target storage.User
		perms         []string
		want          error
	}{
		{"super grants anything", super, plain, AllPerms, nil},
		{"super grants manage", super, plain, []string{PermAccountsManage}, nil},
		{"super revokes manage", super, otherMgr, nil, nil},
		{"manager grants what they hold", mgr, plain, []string{PermStatsView}, nil},
		{"manager clears permissions", mgr, viewer, nil, nil},
		{"manager removes a perm they lack", mgr, chatty, nil, nil},
		{"manager keeps a perm they lack", mgr, chatty, []string{PermChatView, PermStatsView}, nil},
		{"self", mgr, mgr, []string{PermStatsView}, ErrGrantSelf},
		{"super self", super, super, nil, ErrGrantSelf},
		{"touch super", mgr, super, nil, ErrGrantSuper},
		{"not held", mgr, plain, []string{PermChatView}, ErrGrantNotHeld},
		{"non-super grants manage", mgr, plain, []string{PermAccountsManage}, ErrGrantManage},
		{"non-super revokes manage", mgr, otherMgr, nil, ErrGrantManage},
		{"without manage", viewer, plain, []string{PermStatsView}, ErrGrantNeedsManage},
		{"unknown perm", super, plain, []string{"root.everything"}, ErrGrantUnknown},
	}
	for _, c := range cases {
		if got := canGrant(c.actor, c.target, c.perms); !errors.Is(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestEffectivePerms(t *testing.T) {
	if !isAdmin(storage.User{Super: true}) || len(effectivePerms(storage.User{Super: true})) != len(AllPerms) {
		t.Fatal("super does not hold everything")
	}
	if isAdmin(storage.User{}) {
		t.Fatal("a plain user is an admin")
	}
	got := effectivePerms(storage.User{Perms: []string{PermChatView, "made.up"}})
	if len(got) != 1 || !got[PermChatView] {
		t.Fatalf("unknown stored perms leak through: %v", got)
	}
}

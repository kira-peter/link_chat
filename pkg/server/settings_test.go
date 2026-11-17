package server

import (
	"encoding/json"
	"testing"
	"time"

	"link-chat/pkg/protocol"
)

func settingsOf(t *testing.T, c *client) protocol.SettingsGetRes {
	t.Helper()
	var res protocol.SettingsGetRes
	c.callOK(protocol.OpSettingsGet, nil, &res)
	return res
}

func TestSettingsNeverSavedThenSaved(t *testing.T) {
	hs, _ := testServer(t)
	a := dial(t, hs)
	a.register("alice", "correct-horse-1")

	if got := settingsOf(t, a); got.Exists || len(got.Settings) != 0 {
		t.Fatalf("a new account has settings: %+v", got)
	}

	var put protocol.SettingsRes
	a.callOK(protocol.OpSettingsPut, protocol.SettingsPutReq{Settings: protocol.Settings{"theme": "paper"}}, &put)
	if put.Settings["theme"] != "paper" || put.Settings["density"] != "comfortable" {
		t.Fatalf("put answered %v, want the complete settings", put.Settings)
	}

	// A partial save keeps the rest.
	a.callOK(protocol.OpSettingsPut, protocol.SettingsPutReq{Settings: protocol.Settings{"time_format": "12h"}}, nil)
	got := settingsOf(t, a)
	if !got.Exists || got.Settings["theme"] != "paper" || got.Settings["time_format"] != "12h" {
		t.Fatalf("after two saves: %+v", got)
	}
}

func TestSettingsNeedSignIn(t *testing.T) {
	hs, _ := testServer(t)
	c := dial(t, hs)
	c.hello()
	c.callErr(protocol.OpSettingsGet, nil, protocol.CodeUnauthorized)
	c.callErr(protocol.OpSettingsPut, protocol.SettingsPutReq{Settings: protocol.Settings{"theme": "paper"}}, protocol.CodeUnauthorized)
}

func TestInvalidSettingsWriteNothing(t *testing.T) {
	hs, _ := testServer(t)
	a := dial(t, hs)
	a.register("alice", "correct-horse-1")
	a.callOK(protocol.OpSettingsPut, protocol.SettingsPutReq{Settings: protocol.Settings{"theme": "paper"}}, nil)

	for _, bad := range []protocol.Settings{
		{"bubble_width": "55", "theme": "ink"},
		{"font_size": "12", "theme": "ink"},
	} {
		a.callErr(protocol.OpSettingsPut, protocol.SettingsPutReq{Settings: bad}, protocol.CodeInvalidArgument)
	}
	if got := settingsOf(t, a); got.Settings["theme"] != "paper" {
		t.Fatalf("a rejected save changed the theme to %q", got.Settings["theme"])
	}
}

func TestSettingsAreEachUsersOwn(t *testing.T) {
	hs, _ := testServer(t)
	a, b := dial(t, hs), dial(t, hs)
	a.register("alice", "correct-horse-1")
	b.register("bob", "correct-horse-2")
	a.callOK(protocol.OpSettingsPut, protocol.SettingsPutReq{Settings: protocol.Settings{"theme": "paper"}}, nil)
	b.callOK(protocol.OpSettingsPut, protocol.SettingsPutReq{Settings: protocol.Settings{"language": "en"}}, nil)

	if got := settingsOf(t, a); got.Settings["language"] != "zh" || got.Settings["theme"] != "paper" {
		t.Fatalf("alice sees %v", got.Settings)
	}
	if got := settingsOf(t, b); got.Settings["theme"] != "ink" || got.Settings["language"] != "en" {
		t.Fatalf("bob sees %v", got.Settings)
	}
}

// bindSecond finds the server side of a connection that has not signed in and
// binds it to uid, as a second live session of the same account would be. The
// single-session rule would otherwise close one of them.
func bindSecond(t *testing.T, srv *Server, uid string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var found *Conn
		srv.hub.do(func(s *hubState) {
			for c := range s.conns {
				if c.uid() == "" {
					found = c
				}
			}
		})
		if found != nil {
			srv.hub.bindUser(found, uid)
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no unauthenticated connection to bind")
}

func TestSavingPushesToTheAccountsOtherConnections(t *testing.T) {
	hs, srv := testServer(t)
	a := dial(t, hs)
	res := a.register("alice", "correct-horse-1")

	other := dial(t, hs)
	other.hello()
	bindSecond(t, srv, res.User.ID)

	a.callOK(protocol.OpSettingsPut, protocol.SettingsPutReq{Settings: protocol.Settings{"msg_border": "hide"}}, nil)

	var got *protocol.SettingsRes
	for _, f := range other.drainEvents() {
		if f.Type != protocol.TypeEvent {
			continue
		}
		var ev protocol.EventPayload
		if json.Unmarshal(f.Payload, &ev) != nil || ev.Name != protocol.EvSettingsUpdated {
			continue
		}
		var s protocol.SettingsRes
		if err := json.Unmarshal(ev.Data, &s); err != nil {
			t.Fatal(err)
		}
		got = &s
	}
	if got == nil || got.Settings["msg_border"] != "hide" || len(got.Settings) != len(protocol.SettingSpecs) {
		t.Fatalf("the other connection got %+v, want the complete settings", got)
	}

	// The saver gets the response only.
	for _, f := range a.drainEvents() {
		var ev protocol.EventPayload
		if f.Type == protocol.TypeEvent && json.Unmarshal(f.Payload, &ev) == nil && ev.Name == protocol.EvSettingsUpdated {
			t.Fatal("the connection that saved received its own update")
		}
	}
}

package protocol

import (
	"encoding/json"
	"testing"
)

func TestSocialErrorCodesPublished(t *testing.T) {
	want := map[string]string{
		CodeAlreadyFriends: "already_friends",
		CodeInvalidProfile: "invalid_profile",
		CodeMomentNotFound: "moment_not_found",
		CodeMomentTooLong:  "moment_too_long",
		CodeNotFound:       "not_found",
	}
	for code, wire := range want {
		if code != wire || !IsKnownCode(code) {
			t.Errorf("%s not published as %q", code, wire)
		}
	}
}

func TestValidAvatar(t *testing.T) {
	for s, ok := range map[string]bool{
		"A": true, "爱": true, "🐱": true, "👍🏽": true, "🇨🇳": true,
		"": false, " ": false, "\t": false, "ab": false, "我爱你": false, "\x01": false,
	} {
		if ValidAvatar(s) != ok {
			t.Errorf("ValidAvatar(%q) = %v, want %v", s, !ok, ok)
		}
	}
}

func TestDefaultAvatarIsStable(t *testing.T) {
	a1, c1 := DefaultAvatar("alice")
	a2, c2 := DefaultAvatar("ALICE")
	if a1 != "A" || a1 != a2 || c1 != c2 || !ValidAvatarColor(c1) {
		t.Fatalf("got %q %q / %q %q", a1, c1, a2, c2)
	}
	seen := map[string]bool{}
	for _, n := range []string{"alice", "bob", "carol", "dave", "erin", "frank", "gina", "hal"} {
		_, c := DefaultAvatar(n)
		seen[c] = true
	}
	if len(seen) < 3 {
		t.Fatalf("default colours barely vary: %v", seen)
	}
}

func TestValidProfileText(t *testing.T) {
	if !ValidProfileText("小爱", MaxNicknameLen) || ValidProfileText("a\nb", MaxBioLen) {
		t.Fatal("line breaks must be refused, plain text accepted")
	}
	long := make([]rune, MaxBioLen+1)
	for i := range long {
		long[i] = '字'
	}
	if ValidProfileText(string(long), MaxBioLen) {
		t.Fatal("over-long bio accepted")
	}
}

func TestMomentRoundTrip(t *testing.T) {
	m := Moment{ID: "m1", Author: User{ID: "u1", Username: "alice", Avatar: "A", Color: "blue"}, Body: "hi",
		Visibility: VisibilityFriends, Likes: []User{{ID: "u2", Username: "bob"}},
		Comments: []Comment{{ID: "c1", Author: User{ID: "u2", Username: "bob"}, ReplyTo: &User{ID: "u1", Username: "alice"}, Body: "ok"}}}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var back Moment
	if err := json.Unmarshal(b, &back); err != nil || back.Comments[0].ReplyTo.Username != "alice" || back.Author.Color != "blue" {
		t.Fatalf("%s -> %+v %v", b, back, err)
	}
}

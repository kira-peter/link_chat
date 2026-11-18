package server

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"link-chat/pkg/protocol"
	"link-chat/pkg/version"
)

// syncBuffer is a bytes.Buffer safe to write from server goroutines while the
// test reads it.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// helloAs sends a hello carrying the given client identification and returns
// the reply.
func (c *client) helloAs(clientTag string) protocol.Frame {
	c.t.Helper()
	c.send(mustFrame(protocol.NewResRequest("h1", protocol.TypeHello, protocol.HelloPayload{
		Version: protocol.Version, Client: clientTag,
	})))
	return c.recvMatching("h1")
}

func withMin(v string) func(*Config) {
	return func(c *Config) { c.MinClientVersion = v }
}

func outdatedData(t *testing.T, f protocol.Frame) protocol.ClientOutdatedData {
	t.Helper()
	if f.Type != protocol.TypeError {
		t.Fatalf("want error, got %+v", f)
	}
	var p protocol.ResPayload
	_ = json.Unmarshal(f.Payload, &p)
	if p.Code != protocol.CodeClientOutdated {
		t.Fatalf("want %s, got %s", protocol.CodeClientOutdated, p.Code)
	}
	var d protocol.ClientOutdatedData
	if err := json.Unmarshal(p.Data, &d); err != nil {
		t.Fatalf("data: %v", err)
	}
	return d
}

func TestHelloAckCarriesServerVersion(t *testing.T) {
	hs, _ := testServer(t)
	f := dial(t, hs).helloAs("link-chat-tui/0.5.0")
	var rp protocol.ResPayload
	var hp protocol.HelloPayload
	if err := json.Unmarshal(f.Payload, &rp); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(rp.Data, &hp); err != nil {
		t.Fatal(err)
	}
	name, v, ok := version.ParseTag(hp.Server)
	if !ok || name != "link-chat" || v != version.Current() {
		t.Fatalf("server tag %q", hp.Server)
	}
}

func TestMinClientVersion(t *testing.T) {
	cases := []struct {
		tag      string
		ok       bool
		reported string
	}{
		{"link-chat-tui/0.2.9", false, "0.2.9"},
		{"link-chat-tui/0.3.0", true, ""},
		{"link-chat-tui/1.0.0", true, ""},
		{"link-chat-tui", false, ""},     // older client: name only
		{"link-chat-tui/dev", false, ""}, // not a version
		{"", false, ""},
	}
	for _, c := range cases {
		t.Run(c.tag, func(t *testing.T) {
			logs := &syncBuffer{}
			hs, _ := testServerWith(t, withMin("0.3.0"), logs)
			f := dial(t, hs).helloAs(c.tag)
			if c.ok {
				if f.Type != protocol.TypeRes {
					t.Fatalf("want accept, got %+v", f)
				}
				return
			}
			d := outdatedData(t, f)
			if d.MinVersion != "0.3.0" || d.ClientVersion != c.reported {
				t.Fatalf("data %+v", d)
			}
			if !strings.Contains(logs.String(), "client outdated") || !strings.Contains(logs.String(), "min=0.3.0") {
				t.Fatalf("rejection not logged: %s", logs.String())
			}
		})
	}
}

// Without a floor, an old client that sends only its name still connects.
func TestNoMinClientVersionAcceptsOldClients(t *testing.T) {
	hs, _ := testServer(t)
	if f := dial(t, hs).helloAs("link-chat-tui"); f.Type != protocol.TypeRes {
		t.Fatalf("want accept, got %+v", f)
	}
}

func TestSessionRecordsClientVersion(t *testing.T) {
	logs := &syncBuffer{}
	hs, srv := testServerWith(t, nil, logs)
	c := dial(t, hs)
	c.helloAs("link-chat-tui/2.3.4")
	var res protocol.AuthRes
	c.callOK(protocol.OpAuthRegister, protocol.RegisterReq{Username: "alice", Password: "correct horse", SecurityQuestion: "Q?", SecurityAnswer: "a"}, &res)

	toks, err := srv.st.ListTokensForUser(res.User.ID)
	if err != nil || len(toks) == 0 {
		t.Fatalf("tokens: %v %v", toks, err)
	}
	if toks[0].ClientVersion != "2.3.4" {
		t.Fatalf("token client version %q", toks[0].ClientVersion)
	}

	// Resuming from a client that reports nothing records it as unknown.
	c2 := dial(t, hs)
	c2.helloAs("link-chat-tui")
	if f := c2.call(protocol.OpAuthResume, protocol.ResumeReq{Token: res.Token}); f.Type != protocol.TypeRes {
		t.Fatalf("resume: %+v", f)
	}
	toks, _ = srv.st.ListTokensForUser(res.User.ID)
	if len(toks) != 1 || toks[0].ClientVersion != "" {
		t.Fatalf("after resume: %+v", toks)
	}

	out := logs.String()
	for _, want := range []string{"client=2.3.4", "client=unknown"} {
		if !strings.Contains(out, want) {
			t.Fatalf("logs missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "session displaced") {
		t.Fatalf("displacement not logged:\n%s", out)
	}
}

func TestConfigMinClientVersion(t *testing.T) {
	cfg := DefaultConfig()
	if err := cfg.Validate(); err != nil || cfg.HasMinClient {
		t.Fatalf("unset: %v %v", err, cfg.HasMinClient)
	}

	cfg = DefaultConfig()
	cfg.MinClientVersion = "0.3.0"
	if err := cfg.Validate(); err != nil || !cfg.HasMinClient || cfg.MinClient.String() != "0.3.0" {
		t.Fatalf("valid: %v %+v", err, cfg)
	}

	cfg = DefaultConfig()
	cfg.MinClientVersion = "v0.3"
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "min_client_version") {
		t.Fatalf("want min_client_version error, got %v", err)
	}
}

func TestConfigMinClientVersionFromEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.json")
	if err := os.WriteFile(path, []byte(`{"listen":":8100","data_dir":"./data","min_client_version":"0.1.0"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LINKCHAT_MIN_CLIENT_VERSION", "0.4.0")
	cfg, err := LoadConfig(path, false)
	if err != nil || cfg.MinClient.String() != "0.4.0" {
		t.Fatalf("got %v %+v", err, cfg.MinClient)
	}
}

func TestDefaultConfigDocumentsMinClientVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.json")
	if _, err := LoadConfig(path, true); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), `"min_client_version"`) || !strings.Contains(string(b), "LINKCHAT_MIN_CLIENT_VERSION") {
		t.Fatalf("default config lacks min_client_version:\n%s", b)
	}
}

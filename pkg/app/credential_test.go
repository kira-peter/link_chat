package app

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// inMemoryBackend stands in for the OS credential store so tests do not touch the
// real keyring of the machine running them.
type inMemoryBackend struct {
	values  map[string]string
	failSet bool
}

func newMemBackend() *inMemoryBackend {
	return &inMemoryBackend{values: map[string]string{}}
}

func (m *inMemoryBackend) name() string { return "memory" }

func (m *inMemoryBackend) get(instance string) (string, error) {
	v, ok := m.values[instance]
	if !ok {
		return "", ErrNoCredential
	}
	return v, nil
}

func (m *inMemoryBackend) set(instance, secret string) error {
	if m.failSet {
		return os.ErrPermission
	}
	m.values[instance] = secret
	return nil
}

func (m *inMemoryBackend) remove(instance string) error {
	delete(m.values, instance)
	return nil
}

func TestTokenStoreSaveLoadClear(t *testing.T) {
	b := newMemBackend()
	s := tokenStore{backend: b, instance: "default"}

	if _, err := s.load(); err != ErrNoCredential {
		t.Fatalf("empty store should report ErrNoCredential, got %v", err)
	}
	if err := s.save("tok-1"); err != nil {
		t.Fatal(err)
	}
	got, err := s.load()
	if err != nil || got != "tok-1" {
		t.Fatalf("load = %q, %v", got, err)
	}
	if err := s.clear(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.load(); err != ErrNoCredential {
		t.Fatal("clear did not remove the token")
	}
}

// Saving an empty token must clear rather than store an empty secret.
func TestTokenStoreSaveEmptyClears(t *testing.T) {
	b := newMemBackend()
	s := tokenStore{backend: b, instance: "default"}
	_ = s.save("tok")
	if err := s.save(""); err != nil {
		t.Fatal(err)
	}
	if _, ok := b.values["default"]; ok {
		t.Fatal("empty save left a value behind")
	}
}

// The token must never appear in the config file.
func TestConfigFileHoldsNoToken(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	cfg := freshConfig()
	cfg.Username = "alice"
	cfg.Token = "should-not-be-written"
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Strip the key that exists purely for migration.
	body := strings.ReplaceAll(string(b), `"token"`, "")
	if strings.Contains(body, "should-not-be-written") {
		t.Fatalf("token written to config file:\n%s", b)
	}
}

// With remembering on, an app loads the credential from the store, not the file.
func TestAppLoadsTokenFromCredentialStore(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	b := newMemBackend()
	_ = b.set("default", "stored-token")

	cfg := freshConfig()
	cfg.Username = "alice"
	cfg.SetRemember(true)
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Token != "" {
		t.Fatalf("config carried a token: %q", loaded.Token)
	}

	scr := newTestScreen(t, 80, 24)
	a := New(scr, loaded, path)
	// Wire the fake backend in place of the real one the constructor picked.
	a.creds = tokenStore{backend: b, instance: "default"}
	tok, err := a.creds.load()
	if err != nil || tok != "stored-token" {
		t.Fatalf("credential not loaded: %q %v", tok, err)
	}
}

// A legacy file with a plaintext token migrates it into the store and scrubs it.
func TestLegacyTokenMigratesOutOfConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{
  "server": "ws://127.0.0.1:8100/ws",
  "token": "legacy-token",
  "username": "alice",
  "remember_session": true
}`), 0o600); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	scr := newTestScreen(t, 80, 24)
	a := New(scr, loaded, path)

	// Give the app a fake store, then re-run the migration it performs on a
	// config that still carries a plaintext token.
	b := newMemBackend()
	a.creds = tokenStore{backend: b, instance: "default"}
	a.cfg.Token = "legacy-token"
	a.cfg.SetRemember(true)
	a.persistToken("legacy-token")

	if got := b.values["default"]; got != "legacy-token" {
		t.Fatalf("token not adopted: %q", got)
	}

	// And the file must no longer mention it.
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "legacy-token") {
		t.Fatalf("plaintext token survived in config:\n%s", body)
	}
}

// Turning remembering off must delete the stored credential immediately.
func TestToggleRememberOffClearsCredential(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	b := newMemBackend()
	_ = b.set("default", "live-token")

	cfg := freshConfig()
	cfg.SetRemember(true)
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatal(err)
	}

	scr := newTestScreen(t, 80, 24)
	a := New(scr, cfg, path)
	a.creds = tokenStore{backend: b, instance: "default"}
	a.st.Token = "live-token"

	a.toggleRememberSession()

	if a.cfg.Remember() {
		t.Fatal("setting did not toggle")
	}
	if _, ok := b.values["default"]; ok {
		t.Fatal("credential survived turning the setting off")
	}
}

// With remembering off, a successful login must not persist anything.
func TestPersistTokenRespectsRememberOff(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	b := newMemBackend()

	cfg := freshConfig()
	cfg.SetRemember(false)
	scr := newTestScreen(t, 80, 24)
	a := New(scr, cfg, path)
	a.creds = tokenStore{backend: b, instance: "default"}

	a.persistToken("secret")
	if _, ok := b.values["default"]; ok {
		t.Fatal("token stored despite remembering being off")
	}
}

// Every app test runs against an isolated store so the suite never touches the
// machine's real credential database.
func TestMain(m *testing.M) {
	// One store per config path: tests that share a credential would otherwise
	// leak their login into the next test, which is exactly the class of
	// cross-test coupling this suite has been bitten by before.
	byPath := map[string]*inMemoryBackend{}
	var mu sync.Mutex
	credentialBackendFor = func(configPath string) credBackend {
		mu.Lock()
		defer mu.Unlock()
		b, ok := byPath[configPath]
		if !ok {
			b = newMemBackend()
			byPath[configPath] = b
		}
		return b
	}
	os.Exit(m.Run())
}

// rememberConfig builds a config that persists credentials for a named instance.
func rememberConfig(server, instance string) Config {
	c := Config{Server: server, Theme: "ink", Language: "zh", Instance: instance}
	c.SetRemember(true)
	return c
}

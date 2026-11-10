package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zalando/go-keyring"
)

// Credential storage.
//
// A session token is a password in everything but name, so it does not belong in
// a plain JSON file next to the server address. It goes into the OS credential
// store instead — Windows Credential Manager, macOS Keychain, or Secret Service
// on Linux — where it is protected by the user's own login session. Copying the
// config file elsewhere then yields nothing usable.
//
// A file fallback exists for headless machines with no credential service. It is
// 0600 and clearly worse, so the caller is told when it is in use.

// keyringService namespaces our entries in the OS credential store.
const keyringService = "link-chat"

// ErrNoCredential means nothing is stored for this instance.
var ErrNoCredential = errors.New("no stored credential")

// credBackend stores and retrieves one secret per instance.
type credBackend interface {
	get(instance string) (string, error)
	set(instance, secret string) error
	remove(instance string) error
	// name describes the backend for the settings screen and diagnostics.
	name() string
}

// keyringBackend talks to the OS credential store.
type keyringBackend struct{}

func (keyringBackend) name() string { return "keyring" }

func (keyringBackend) get(instance string) (string, error) {
	v, err := keyring.Get(keyringService, instance)
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return "", ErrNoCredential
		}
		return "", err
	}
	return v, nil
}

func (keyringBackend) set(instance, secret string) error {
	return keyring.Set(keyringService, instance, secret)
}

func (keyringBackend) remove(instance string) error {
	err := keyring.Delete(keyringService, instance)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}

// fileBackend is the fallback for environments without a credential service.
//
// It is still owner-only (0600) and lives outside the config file, so the token
// is not copied along with settings, but anyone who can read the user's files can
// read it. The UI reports which backend is active.
type fileBackend struct {
	dir string
}

func (f fileBackend) name() string { return "file" }

func (f fileBackend) path(instance string) string {
	return filepath.Join(f.dir, instance+".token")
}

func (f fileBackend) get(instance string) (string, error) {
	b, err := os.ReadFile(f.path(instance))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", ErrNoCredential
		}
		return "", err
	}
	v := strings.TrimSpace(string(b))
	if v == "" {
		return "", ErrNoCredential
	}
	return v, nil
}

func (f fileBackend) set(instance, secret string) error {
	if err := os.MkdirAll(f.dir, 0o700); err != nil {
		return err
	}
	// Write through a temp file so a crash cannot leave a half-written secret.
	tmp := f.path(instance) + ".tmp"
	if err := os.WriteFile(tmp, []byte(secret), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, f.path(instance))
}

func (f fileBackend) remove(instance string) error {
	err := os.Remove(f.path(instance))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// credentialBackendFor lets tests substitute a fake store.
//
// It exists so the test suite never writes to the developer's real Windows
// Credential Manager / Keychain. Left nil in production.
var credentialBackendFor func(configPath string) credBackend

// credentials chooses a backend once per process.
//
// The keyring is probed rather than assumed: on a headless Linux box the Secret
// Service is simply absent, and failing every login attempt because of that would
// be worse than the fallback.
func credentials(configPath string) credBackend {
	if credentialBackendFor != nil {
		return credentialBackendFor(configPath)
	}
	probe := keyringBackend{}
	if err := probe.set("__probe__", "1"); err == nil {
		_ = probe.remove("__probe__")
		return probe
	}
	return fileBackend{dir: filepath.Join(filepath.Dir(configPath), "credentials")}
}

// tokenStore is the app's handle on the chosen backend for this instance.
type tokenStore struct {
	backend  credBackend
	instance string
}

// load returns the stored token, or ErrNoCredential.
func (t tokenStore) load() (string, error) {
	if t.backend == nil || t.instance == "" {
		return "", ErrNoCredential
	}
	return t.backend.get(t.instance)
}

// save persists a token.
func (t tokenStore) save(token string) error {
	if t.backend == nil || t.instance == "" {
		return fmt.Errorf("no credential store configured")
	}
	if token == "" {
		return t.clear()
	}
	return t.backend.set(t.instance, token)
}

// clear removes the stored token.
func (t tokenStore) clear() error {
	if t.backend == nil || t.instance == "" {
		return nil
	}
	return t.backend.remove(t.instance)
}

// describe names the active backend for display.
func (t tokenStore) describe() string {
	if t.backend == nil {
		return "none"
	}
	return t.backend.name()
}

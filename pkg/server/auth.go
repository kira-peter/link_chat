package server

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"
)

// argonParams are the Argon2id cost parameters. They are embedded in every
// stored hash (PHC format), which is what makes the "rehash on login when the
// parameters change" requirement implementable (spec account-auth / 密码存储).
type argonParams struct {
	memory  uint32 // KiB
	time    uint32 // iterations
	threads uint8
	keyLen  uint32
}

// defaultArgonParams are deliberately conservative; a public server may be
// hammered and each login costs this much memory and time by design.
var defaultArgonParams = argonParams{
	memory:  64 * 1024,
	time:    3,
	threads: 2,
	keyLen:  32,
}

// hashPassword produces a PHC-format Argon2id string:
//
//	$argon2id$v=19$m=65536,t=3,p=2$<salt>$<hash>
func hashPassword(password string, p argonParams) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: read salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, p.time, p.memory, p.threads, p.keyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.memory, p.time, p.threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// verifyPassword checks a password and reports whether the stored hash used
// weaker parameters than the current policy, which the caller uses to rehash.
func verifyPassword(password, encoded string) (ok bool, needsRehash bool, err error) {
	parts := strings.Split(encoded, "$")
	// Leading empty element from the leading '$'.
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return false, false, errors.New("auth: unrecognised hash format")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return false, false, errors.New("auth: bad hash version")
	}
	if version != argon2.Version {
		// An older or newer algorithm version cannot be verified here; the
		// account must log in another way (out of scope) or be reset.
		return false, false, errors.New("auth: unsupported hash version")
	}

	var p argonParams
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.memory, &p.time, &p.threads); err != nil {
		return false, false, errors.New("auth: bad hash params")
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, false, errors.New("auth: bad salt encoding")
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, false, errors.New("auth: bad hash encoding")
	}

	p.keyLen = uint32(len(want))
	got := argon2.IDKey([]byte(password), salt, p.time, p.memory, p.threads, p.keyLen)
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return false, false, nil
	}

	cur := defaultArgonParams
	needsRehash = p.memory < cur.memory || p.time < cur.time || p.threads < cur.threads
	return true, needsRehash, nil
}

// tokenTTL is how long a session token stays valid.
const tokenTTL = 30 * 24 * time.Hour

// resetTokenTTL is deliberately short: a reset token is a one-shot key to the
// account, and the user is standing right in front of the wizard that issued it
// (spec account-auth / 找回密码流程).
const resetTokenTTL = 5 * time.Minute

// normaliseAnswer prepares a security answer for hashing and comparison.
//
// The answer is a human recollection, so "Blue Harbor" and "  blue   harbor "
// are treated as the same answer: case is folded and runs of whitespace collapse
// to one space. The transformation happens before hashing, since a hash cannot
// be normalised afterwards (design.md decision 2).
//
// Passwords are never passed through this; they are compared exactly.
func normaliseAnswer(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

// hashAnswer produces the stored form of a security answer.
func hashAnswer(answer string, p argonParams) (string, error) {
	return hashPassword(normaliseAnswer(answer), p)
}

// verifyAnswer checks an answer against its stored hash.
func verifyAnswer(answer, encoded string) (bool, error) {
	ok, _, err := verifyPassword(normaliseAnswer(answer), encoded)
	return ok, err
}

// newToken returns a fresh opaque credential. It is random rather than a
// signed blob so the server can revoke one instantly (spec account-auth /
// 登录与令牌签发).
func newToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("auth: read token entropy: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// tokenHash is what actually goes to disk: a stolen data file yields no usable
// sessions.
func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// newUserID and newConversationID produce random identifiers so IDs disclose
// neither ordering nor volume.
func newID(prefix string) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("server: entropy source failed: " + err.Error())
	}
	return prefix + hex.EncodeToString(b[:])
}

// validateUsername enforces the character set and length the spec promises,
// and returns the canonical stored form.
func validateUsername(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if len([]rune(name)) < 3 || len([]rune(name)) > 32 {
		return "", errInvalidUsername
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '_' || r == '-' || r == '.':
		default:
			return "", errInvalidUsername
		}
	}
	return name, nil
}

// validateChannelName matches the same rules as usernames; channels are
// referenced by name so the set must be unambiguous and URL-safe.
func validateChannelName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if len([]rune(name)) < 1 || len([]rune(name)) > 48 {
		return "", errInvalidChannelName
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '_' || r == '-' || r == '.':
		default:
			return "", errInvalidChannelName
		}
	}
	return name, nil
}

// validatePassword enforces a minimum length. Length is the only rule: complexity
// masks add friction without adding much, and length is what actually matters.
func validatePassword(pw string) error {
	if len([]rune(pw)) < 8 {
		return errWeakPassword
	}
	if len(pw) > 1024 {
		return errWeakPassword
	}
	return nil
}

var (
	errInvalidUsername    = errors.New("invalid username")
	errInvalidChannelName = errors.New("invalid channel name")
	errWeakPassword       = errors.New("weak password")
)

// rateLimiter is a sliding-window counter plus exponential backoff for repeated
// failures (spec account-auth / 认证滥用防护).
type rateLimiter struct {
	window   time.Duration
	limit    int
	backoff  time.Duration
	maxDelay time.Duration
	now      func() time.Time

	mu      sync.Mutex
	entries map[string]*limitEntry
}

type limitEntry struct {
	hits       []time.Time
	failures   int
	blockUntil time.Time
}

// Allow records one attempt from key and reports whether it may proceed.
// retryAfter is how long the caller should wait when it may not.
func (l *rateLimiter) Allow(key string) (allowed bool, retryAfter time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	e := l.entries[key]
	if e == nil {
		e = &limitEntry{}
		l.entries[key] = e
	}

	if now.Before(e.blockUntil) {
		return false, e.blockUntil.Sub(now)
	}

	// Drop hits outside the window.
	cutoff := now.Add(-l.window)
	kept := e.hits[:0]
	for _, t := range e.hits {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	e.hits = kept

	if len(e.hits) >= l.limit {
		return false, l.window
	}
	e.hits = append(e.hits, now)
	return true, 0
}

// Fail records an authentication failure and arms backoff once the threshold is
// crossed.
func (l *rateLimiter) Fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	e := l.entries[key]
	if e == nil {
		e = &limitEntry{}
		l.entries[key] = e
	}
	e.failures++
	if e.failures < 5 {
		return
	}
	// 1s, 2s, 4s, ... capped.
	delay := l.backoff
	for i := 0; i < e.failures-5 && delay < l.maxDelay; i++ {
		delay *= 2
	}
	if delay > l.maxDelay {
		delay = l.maxDelay
	}
	e.blockUntil = l.now().Add(delay)
}

// Succeed clears the failure counter so a legitimate user is not punished after
// a successful login (spec account-auth / 退避后恢复).
func (l *rateLimiter) Succeed(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if e := l.entries[key]; e != nil {
		e.failures = 0
		e.blockUntil = time.Time{}
		e.hits = nil
	}
}

func newRateLimiter(limit int, window, backoff, maxDelay time.Duration) *rateLimiter {
	return &rateLimiter{
		window:   window,
		limit:    limit,
		backoff:  backoff,
		maxDelay: maxDelay,
		now:      time.Now,
		entries:  map[string]*limitEntry{},
	}
}

// parseRetryAfterMS formats a duration the way the protocol carries it.
func retryAfterMS(d time.Duration) int {
	ms := int(d / time.Millisecond)
	if ms < 1 {
		ms = 1
	}
	return ms
}

// parseInt64 is a small helper used by pagination parsing.
func parseInt64(s string) int64 {
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return v
}

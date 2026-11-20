package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Login backoff (spec account-auth / 认证滥用防护).
//
// The wait between wrong passwords climbs like an iPhone's: nothing for the
// first three attempts, then 1m, 5m, 15m, 1h, 3h, 8h, and from the tenth
// attempt one day, doubling each further failure. There is no wipe and no
// permanent lock — waiting long enough always reopens the door.
//
// The count is per source address and account, so one person guessing cannot
// lock out someone else's account, and everyone behind one NAT address does not
// share a counter. It outlives a restart in data/login_failures.json.

// loginFailureFile is where the counters live inside the data directory.
const loginFailureFile = "login_failures.json"

// backoffMax is the longest wait. Doubling forever would eventually overflow a
// time.Duration into a negative value, which would read as "no wait".
const backoffMax = 365 * 24 * time.Hour

// backoffEntryTTL is how long an entry idle past its wait is kept. Long enough
// that a returning attacker resumes where they left off, short enough that the
// file does not grow without bound.
const backoffEntryTTL = 30 * 24 * time.Hour

// backoffTable holds the waits for the fourth through ninth failure. The first
// three failures are free, and the tenth starts the daily doubling.
var backoffTable = [...]time.Duration{
	1 * time.Minute,  // 4th
	5 * time.Minute,  // 5th
	15 * time.Minute, // 6th
	1 * time.Hour,    // 7th
	3 * time.Hour,    // 8th
	8 * time.Hour,    // 9th
}

// backoffDelay is how long to wait after the given number of consecutive
// failures. Zero means the next attempt may go ahead at once.
func backoffDelay(failures int) time.Duration {
	switch {
	case failures <= 3:
		return 0
	case failures <= 3+len(backoffTable):
		return backoffTable[failures-4]
	}
	// 10th failure: one day, then doubling.
	d := 24 * time.Hour
	n := failures - 10
	for i := 0; i < n && d < backoffMax; i++ {
		d *= 2
	}
	if d > backoffMax {
		d = backoffMax
	}
	return d
}

// loginFailureKey is the counter a login belongs to: one address and one
// account. The account is lower-cased because usernames are matched that way.
func loginFailureKey(addr, username string) string {
	return hostOnly(addr) + "\x00" + strings.ToLower(strings.TrimSpace(username))
}

// backoffEntry is one stored counter.
type backoffEntry struct {
	Failures   int   `json:"failures"`
	BlockUntil int64 `json:"block_until,omitempty"`
	UpdatedAt  int64 `json:"updated_at"`
}

// loginBackoff remembers failures per key and persists them.
type loginBackoff struct {
	path string
	log  *slog.Logger
	now  func() time.Time

	mu      sync.Mutex
	entries map[string]backoffEntry
}

// newLoginBackoff loads the counters under dir. A missing file starts empty; an
// unreadable one is a warning and also starts empty, so a corrupt file cannot
// keep everyone out.
func newLoginBackoff(dir string, log *slog.Logger, now func() time.Time) *loginBackoff {
	if now == nil {
		now = time.Now
	}
	b := &loginBackoff{
		path:    filepath.Join(dir, loginFailureFile),
		log:     log,
		now:     now,
		entries: map[string]backoffEntry{},
	}
	b.load()
	return b
}

func (b *loginBackoff) load() {
	data, err := os.ReadFile(b.path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) && b.log != nil {
			b.log.Warn("login failures unreadable, starting empty", "err", err)
		}
		return
	}
	var doc struct {
		Entries map[string]backoffEntry `json:"entries"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		if b.log != nil {
			b.log.Warn("login failures malformed, starting empty", "err", err)
		}
		return
	}
	cutoff := b.now().Add(-backoffEntryTTL).UnixMilli()
	for k, e := range doc.Entries {
		if e.BlockUntil < cutoff && e.UpdatedAt < cutoff {
			continue
		}
		b.entries[k] = e
	}
}

// save writes the counters out. The caller holds b.mu. A failed write is
// logged and otherwise ignored: the in-memory counter still applies, and a
// restart losing it is the same as before this file existed.
func (b *loginBackoff) save() {
	doc := struct {
		Entries map[string]backoffEntry `json:"entries"`
	}{Entries: b.entries}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		b.log.Warn("encode login failures", "err", err)
		return
	}
	if err := writeFileAtomic(b.path, data); err != nil {
		b.log.Warn("save login failures", "err", err)
	}
}

// Check reports whether a login for key may proceed. When it may not, wait is
// the time left until it can.
func (b *loginBackoff) Check(key string) (wait time.Duration, blocked bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	e, ok := b.entries[key]
	if !ok {
		return 0, false
	}
	left := time.UnixMilli(e.BlockUntil).Sub(b.now())
	if left <= 0 {
		return 0, false
	}
	return left, true
}

// RecordFailure counts one wrong password and arms the next wait.
func (b *loginBackoff) RecordFailure(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	e := b.entries[key]
	e.Failures++
	now := b.now()
	e.UpdatedAt = now.UnixMilli()
	if d := backoffDelay(e.Failures); d > 0 {
		e.BlockUntil = now.Add(d).UnixMilli()
	}
	b.entries[key] = e
	b.save()
}

// RecordSuccess clears a key: a correct password resets the climb
// (spec account-auth / 退避后恢复).
func (b *loginBackoff) RecordSuccess(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.entries[key]; !ok {
		return
	}
	delete(b.entries, key)
	b.save()
}

// writeFileAtomic replaces path with data through a temp file and a rename, so
// a crash mid-write cannot leave a half-written counter file.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return fmt.Errorf("login backoff: temp file: %w", err)
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return fmt.Errorf("login backoff: write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return fmt.Errorf("login backoff: close: %w", err)
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return fmt.Errorf("login backoff: rename: %w", err)
	}
	return nil
}

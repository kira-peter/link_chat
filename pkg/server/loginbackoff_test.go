package server

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBackoffDelay(t *testing.T) {
	cases := []struct {
		failures int
		want     time.Duration
	}{
		{0, 0},
		{1, 0},
		{3, 0},
		{4, time.Minute},
		{5, 5 * time.Minute},
		{6, 15 * time.Minute},
		{7, time.Hour},
		{8, 3 * time.Hour},
		{9, 8 * time.Hour},
		{10, 24 * time.Hour},
		{11, 48 * time.Hour},
		{12, 96 * time.Hour},
		{13, 192 * time.Hour},
		{14, 384 * time.Hour}, // 16 days
		{60, backoffMax},      // doubling stops at the cap
		{1000, backoffMax},    // and never goes negative
	}
	for _, c := range cases {
		if got := backoffDelay(c.failures); got != c.want {
			t.Errorf("backoffDelay(%d) = %v, want %v", c.failures, got, c.want)
		}
	}
}

// fakeClock is a movable now for the backoff tests.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func TestLoginBackoffClimbsAndClears(t *testing.T) {
	dir := t.TempDir()
	clk := &fakeClock{t: time.Unix(1_800_000_000, 0)}
	b := newLoginBackoff(dir, testLogger(), clk.now)
	key := loginFailureKey("10.0.0.1:5555", "Alice")

	for i := 1; i <= 4; i++ {
		if wait, blocked := b.Check(key); blocked {
			t.Fatalf("attempt %d blocked before any failure: %v", i, wait)
		}
		b.RecordFailure(key)
		if i < 4 {
			if _, blocked := b.Check(key); blocked {
				t.Fatalf("failure %d should not block", i)
			}
			continue
		}
		wait, blocked := b.Check(key)
		if !blocked || wait != time.Minute {
			t.Fatalf("4th failure: wait=%v blocked=%v, want 1m", wait, blocked)
		}
	}

	// The port does not matter: another connection from the same host and the
	// same account sees the same wait.
	if _, blocked := b.Check(loginFailureKey("10.0.0.1:9999", "alice")); !blocked {
		t.Fatal("same host and account with another port is not blocked")
	}
	// Another account or another host is untouched.
	if _, blocked := b.Check(loginFailureKey("10.0.0.1:5555", "bob")); blocked {
		t.Fatal("another account is blocked")
	}
	if _, blocked := b.Check(loginFailureKey("10.0.0.2:5555", "alice")); blocked {
		t.Fatal("another host is blocked")
	}

	clk.advance(time.Minute)
	if _, blocked := b.Check(key); blocked {
		t.Fatal("still blocked after the wait elapsed")
	}
	b.RecordFailure(key) // 5th
	if wait, blocked := b.Check(key); !blocked || wait != 5*time.Minute {
		t.Fatalf("5th failure: wait=%v blocked=%v, want 5m", wait, blocked)
	}

	b.RecordSuccess(key)
	if _, blocked := b.Check(key); blocked {
		t.Fatal("success did not clear the wait")
	}
	b.RecordFailure(key)
	if _, blocked := b.Check(key); blocked {
		t.Fatal("the count did not reset: one failure after success should not block")
	}
}

func TestLoginBackoffPersists(t *testing.T) {
	dir := t.TempDir()
	clk := &fakeClock{t: time.Unix(1_800_000_000, 0)}
	key := loginFailureKey("10.0.0.1:5555", "alice")

	b := newLoginBackoff(dir, testLogger(), clk.now)
	for i := 0; i < 4; i++ {
		b.RecordFailure(key)
	}
	if _, err := os.Stat(filepath.Join(dir, loginFailureFile)); err != nil {
		t.Fatalf("counter file not written: %v", err)
	}

	// A restart reads the same counters.
	b2 := newLoginBackoff(dir, testLogger(), clk.now)
	if wait, blocked := b2.Check(key); !blocked || wait != time.Minute {
		t.Fatalf("after reload: wait=%v blocked=%v, want 1m", wait, blocked)
	}
	b2.RecordSuccess(key)

	// Success persisted too.
	b3 := newLoginBackoff(dir, testLogger(), clk.now)
	if _, blocked := b3.Check(key); blocked {
		t.Fatal("cleared counter came back after a reload")
	}
}

func TestLoginBackoffDropsStaleAndSurvivesCorruption(t *testing.T) {
	dir := t.TempDir()
	clk := &fakeClock{t: time.Unix(1_800_000_000, 0)}
	path := filepath.Join(dir, loginFailureFile)

	stale := `{"entries":{"old\u0000user":{"failures":9,"block_until":1,"updated_at":1}}}`
	if err := os.WriteFile(path, []byte(stale), 0o600); err != nil {
		t.Fatal(err)
	}
	b := newLoginBackoff(dir, testLogger(), clk.now)
	b.mu.Lock()
	n := len(b.entries)
	b.mu.Unlock()
	if n != 0 {
		t.Fatalf("stale entry kept: %d entries", n)
	}

	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	b = newLoginBackoff(dir, testLogger(), clk.now)
	if _, blocked := b.Check(loginFailureKey("1.2.3.4:1", "x")); blocked {
		t.Fatal("corrupt file blocked a login")
	}
	// And it can still record and save.
	b.RecordFailure(loginFailureKey("1.2.3.4:1", "x"))
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("could not save over a corrupt file: %v", err)
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

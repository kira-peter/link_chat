// Package dirlock holds an exclusive lock on a data directory, so the server
// and its command-line tools never write the same store at once
// (spec server-runtime / 数据目录独占).
//
// The lock is an operating-system file lock on <dir>/server.lock. The OS
// releases it when the process exits for any reason, so a crash never leaves
// a stale lock behind.
package dirlock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrLocked means another process holds the directory.
var ErrLocked = errors.New("data directory is in use by another process")

// FileName is the lock file inside the data directory.
const FileName = "server.lock"

// Lock is a held directory lock.
type Lock struct{ f *os.File }

// Acquire takes the lock on dir without waiting.
func Acquire(dir string) (*Lock, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, FileName), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("dirlock: open: %w", err)
	}
	if err := lockFile(f); err != nil {
		f.Close()
		return nil, err
	}
	// Leave the holder's PID in the file to help a person find it. The lock,
	// not this content, is what counts.
	_ = f.Truncate(0)
	_, _ = f.WriteAt([]byte(fmt.Sprintf("%d\n", os.Getpid())), 0)
	return &Lock{f: f}, nil
}

// Release gives the lock up.
func (l *Lock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	err := unlockFile(l.f)
	if cerr := l.f.Close(); err == nil {
		err = cerr
	}
	l.f = nil
	return err
}

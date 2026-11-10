package storage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// writeFileAtomic replaces path with data without ever leaving a partially
// written file visible to a reader.
//
// The sequence is: write a sibling temp file, fsync it, then rename over the
// target. Rename within one directory is atomic on both POSIX and Windows, so a
// crash at any point leaves either the old file or the new one — never a mix
// (spec server-runtime / 数据持久化语义).
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("storage: create %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return fmt.Errorf("storage: temp file for %s: %w", path, err)
	}
	tmpName := tmp.Name()

	// Best effort: remove the temp file if we bail out before the rename.
	cleanup := func() {
		tmp.Close()
		os.Remove(tmpName)
	}

	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("storage: write %s: %w", path, err)
	}
	// Durability matters more than speed here: an ack is only sent after this
	// returns, so the bytes must reach the device before we claim the write.
	if err := tmp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("storage: sync %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("storage: close temp for %s: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("storage: rename onto %s: %w", path, err)
	}
	return nil
}

// appendLine durably appends one JSON line to path.
//
// Appending is how messages avoid rewriting an entire conversation file on every
// send (design.md, write amplification). A crash mid-append can leave a partial
// trailing line; loadJSONLines skips it, so the file stays readable and the
// acknowledged messages before it are intact.
func appendLine(path string, line []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("storage: create %s: %w", dir, err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("storage: open %s: %w", path, err)
	}
	defer f.Close()

	buf := make([]byte, 0, len(line)+1)
	buf = append(buf, line...)
	buf = append(buf, '\n')
	if _, err := f.Write(buf); err != nil {
		return fmt.Errorf("storage: append %s: %w", path, err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("storage: sync %s: %w", path, err)
	}
	return nil
}

// splitJSONLines returns the non-empty lines of b, dropping a trailing partial
// line left by an interrupted append.
func splitJSONLines(b []byte) [][]byte {
	var out [][]byte
	start := 0
	for i := 0; i < len(b); i++ {
		if b[i] != '\n' {
			continue
		}
		line := strings.TrimSpace(string(b[start:i]))
		if line != "" {
			out = append(out, []byte(line))
		}
		start = i + 1
	}
	// Anything after the last newline is either empty or a torn write.
	return out
}

// wrapErr converts a raw filesystem error into one of the sentinel errors the
// server knows how to map, so paths never leak past this package.
func wrapErr(err error) error {
	if err == nil {
		return nil
	}
	var pe *os.PathError
	if errors.As(err, &pe) {
		if errors.Is(pe.Err, os.ErrPermission) || errors.Is(pe.Err, os.ErrClosed) {
			return fmt.Errorf("%w: %v", ErrUnavailable, pe.Err)
		}
	}
	if errors.Is(err, os.ErrPermission) {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return err
}

// isWriteFailure reports whether err means "the disk refused us", which the
// server turns into storage_unavailable rather than a generic internal error.
func isWriteFailure(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrUnavailable) {
		return true
	}
	var pe *os.PathError
	if errors.As(err, &pe) {
		return true
	}
	return false
}

// errNotExist marks "nothing to remove", so removeIfExists can stay silent.
var errNotExist = os.ErrNotExist

func removeFile(path string) error {
	return os.Remove(path)
}

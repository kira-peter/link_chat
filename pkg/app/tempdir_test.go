package app

import (
	"os"
	"testing"
	"time"
)

// appTempDir is t.TempDir for tests that run a real App.
//
// An App writes its config from background goroutines (a login reply, a list
// refresh) that can still be finishing when a test returns. On Windows a file
// being written cannot be removed, so t.TempDir's cleanup fails the test over a
// race that has nothing to do with what it checks. This directory is removed with
// a few retries instead, giving those writes a moment to land.
func appTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "linkchat-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		var err error
		for i := 0; i < 20; i++ {
			if err = os.RemoveAll(dir); err == nil {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Logf("could not remove %s: %v", dir, err)
	})
	return dir
}

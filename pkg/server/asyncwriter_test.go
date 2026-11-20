package server

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"
)

// blockingWriter stands in for a paused console: every write waits until
// released.
type blockingWriter struct {
	release chan struct{}
	mu      sync.Mutex
	buf     bytes.Buffer
}

func (w *blockingWriter) Write(p []byte) (int, error) {
	<-w.release
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *blockingWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

// A stalled terminal must never block a caller that logs.
func TestAsyncWriterNeverBlocks(t *testing.T) {
	bw := &blockingWriter{release: make(chan struct{})}
	a := NewAsyncWriter(bw)

	done := make(chan struct{})
	go func() {
		for i := 0; i < AsyncWriterDepth*3; i++ {
			_, _ = a.Write([]byte("line\n"))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Write blocked on a stalled writer")
	}

	close(bw.release)
	a.Close(2 * time.Second)
	out := bw.String()
	if !strings.Contains(out, "log lines dropped while output was blocked") {
		t.Fatalf("drops not reported: %q", out[:min(len(out), 200)])
	}
	// Writes after Close are ignored, not a panic.
	_, _ = a.Write([]byte("late\n"))
}

func TestAsyncWriterDeliversInOrder(t *testing.T) {
	var b bytes.Buffer
	a := NewAsyncWriter(&b)
	for _, s := range []string{"a\n", "b\n", "c\n"} {
		_, _ = a.Write([]byte(s))
	}
	a.Close(2 * time.Second)
	if b.String() != "a\nb\nc\n" {
		t.Fatalf("got %q", b.String())
	}
}

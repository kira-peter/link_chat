package server

import (
	"io"
	"sync"
	"sync/atomic"
	"time"
)

// AsyncWriter decouples log output from request handling. Writes are queued
// and a single goroutine copies them to the underlying writer, so a terminal
// that stops reading — a Windows console paused by QuickEdit selection, a
// full pipe — can never stall a request that only wanted to log a line.
// When the queue is full the oldest queued line is dropped to make room; the
// number dropped is reported once output resumes.
type AsyncWriter struct {
	w       io.Writer
	q       chan []byte
	dropped atomic.Int64
	done    chan struct{}
	mu      sync.RWMutex // guards closed against a Write racing Close
	closed  bool
}

// AsyncWriterDepth is how many lines may wait for a stalled writer.
const AsyncWriterDepth = 1024

// NewAsyncWriter starts copying to w in the background.
func NewAsyncWriter(w io.Writer) *AsyncWriter {
	a := &AsyncWriter{w: w, q: make(chan []byte, AsyncWriterDepth), done: make(chan struct{})}
	go a.run()
	return a
}

// Write queues p and never blocks. After Close it writes nothing.
func (a *AsyncWriter) Write(p []byte) (int, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.closed {
		return len(p), nil
	}
	b := append([]byte(nil), p...)
	for {
		select {
		case a.q <- b:
			return len(p), nil
		default:
		}
		// Full: drop the oldest line and try again.
		select {
		case <-a.q:
			a.dropped.Add(1)
		default:
		}
	}
}

func (a *AsyncWriter) run() {
	defer close(a.done)
	for b := range a.q {
		if n := a.dropped.Swap(0); n > 0 {
			_, _ = io.WriteString(a.w, "-- "+itoa(int(n))+" log lines dropped while output was blocked --\n")
		}
		_, _ = a.w.Write(b)
	}
}

// Close flushes what is queued, waiting at most timeout for a stalled writer.
func (a *AsyncWriter) Close(timeout time.Duration) {
	a.mu.Lock()
	if !a.closed {
		a.closed = true
		close(a.q)
	}
	a.mu.Unlock()
	select {
	case <-a.done:
	case <-time.After(timeout):
	}
}

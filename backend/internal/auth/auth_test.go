package auth_test

import (
	"bytes"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// testClock is a manually advanced clock, so TTLs and lockout windows are
// exercised without a single sleep.
type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *testClock {
	return &testClock{t: time.Date(2025, 8, 17, 14, 32, 1, 0, time.UTC)}
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// logBuffer captures everything logged at every level, for the "a token is
// never written to a log" acceptance test.
type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func newLogBuffer() *logBuffer { return &logBuffer{} }

func (b *logBuffer) logger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(b, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *logBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *logBuffer) contains(s string) bool {
	return strings.Contains(b.String(), s)
}

func mustNotBeEmpty(t *testing.T, what, s string) {
	t.Helper()
	if s == "" {
		t.Fatalf("%s is empty", what)
	}
}

package collect

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"xrayscope/internal/model"
)

type memSink struct {
	mu    sync.Mutex
	recs  map[string]int
	pos   Position
	lines int64
}

func newMemSink() *memSink { return &memSink{recs: map[string]int{}} }

func (s *memSink) Flush(recs []model.Rec, pos Position, lines int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range recs {
		s.recs[fmt.Sprintf("%s|%s|%s", r.U, r.IP, r.D)] += r.N
	}
	s.pos = pos
	s.lines += lines
	return nil
}

func (s *memSink) total() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, v := range s.recs {
		n += v
	}
	return n
}

func line(ts time.Time, user, host string) string {
	return fmt.Sprintf("%s from 1.2.3.4:5555 accepted tcp:%s:443 [in >> direct] email: %s\n", ts.Format("2006/01/02 15:04:05.000000"), host, user)
}

func startPipeline(t *testing.T, path string, sink Sink, start Position) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = Run(ctx, PipelineConfig{
			Tailer:     Tailer{Path: path, Loc: time.UTC, Interval: 20 * time.Millisecond, Start: start},
			Sink:       sink,
			FlushEvery: 30 * time.Millisecond,
		})
	}()
	return func() { cancel(); <-done }
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

func TestTailFollowsRotationAndResumes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "access.log")
	now := time.Now().UTC()

	f, _ := os.Create(path)
	for i := 0; i < 100; i++ {
		f.WriteString(line(now, "alice", "example.com"))
	}
	f.WriteString("2024/01/01 00:00:00 from 1.2.3.4:1 rejected garbage\n")
	f.WriteString(line(now, "alice", "exam")[:30]) // partial line, no newline yet
	f.Close()

	sink := newMemSink()
	stop := startPipeline(t, path, sink, Position{})
	waitFor(t, "initial lines", func() bool { return sink.total() == 100 })

	// complete the partial line and append more
	f, _ = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString(line(now, "alice", "exam")[30:])
	f.WriteString(line(now, "bob", "example.com"))
	f.Close()
	waitFor(t, "appended lines", func() bool { return sink.total() == 102 })

	// rotate: rename old file and create a new one (Windows cannot rename an
	// open file, so there the log is truncated in place, like copytruncate)
	if runtime.GOOS == "windows" {
		if err := os.Truncate(path, 0); err != nil {
			t.Fatal(err)
		}
		f, _ = os.OpenFile(path, os.O_WRONLY, 0o600)
	} else {
		if err := os.Rename(path, path+".1"); err != nil {
			t.Fatal(err)
		}
		f, _ = os.Create(path)
	}
	for i := 0; i < 5; i++ {
		f.WriteString(line(now, "carol", "new.example.com"))
	}
	f.Close()
	waitFor(t, "rotated file", func() bool { return sink.total() == 107 })
	stop()

	sink.mu.Lock()
	pos := sink.pos
	sink.mu.Unlock()
	if pos.Offset == 0 || pos.FP == "" {
		t.Fatalf("position not recorded: %+v", pos)
	}

	// restart from the saved position: nothing may be counted twice
	f, _ = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString(line(now, "carol", "new.example.com"))
	f.Close()
	sink2 := newMemSink()
	stop = startPipeline(t, path, sink2, pos)
	waitFor(t, "resume", func() bool { return sink2.total() == 1 })
	time.Sleep(150 * time.Millisecond)
	stop()
	if sink2.total() != 1 {
		t.Fatalf("resume re-read old data: %d", sink2.total())
	}

	// a different file with the same name must not be resumed mid-way
	os.Remove(path)
	f, _ = os.Create(path)
	for i := 0; i < 3; i++ {
		f.WriteString(line(now.Add(time.Hour), "dave", "other.com"))
	}
	f.Close()
	sink3 := newMemSink()
	stop = startPipeline(t, path, sink3, pos)
	waitFor(t, "replaced file", func() bool { return sink3.total() == 3 })
	stop()
}

func TestTailWaitsForMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "later.log")
	sink := newMemSink()
	stop := startPipeline(t, path, sink, Position{})
	defer stop()
	time.Sleep(80 * time.Millisecond)
	os.WriteFile(path, []byte(line(time.Now().UTC(), "x", "a.com")), 0o600)
	waitFor(t, "late file", func() bool { return sink.total() == 1 })
}

func BenchmarkAggregator(b *testing.B) {
	a := NewAggregator()
	e := mustEntry()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		a.Add(e)
	}
}

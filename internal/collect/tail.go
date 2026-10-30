// Package collect follows the Xray access log, aggregates it in memory and
// hands compact records to a sink.
package collect

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"time"
	"unsafe"

	"xrayscope/internal/parser"
)

// Position identifies how far a log file has been consumed. The fingerprint
// of the file head detects rotation even when the file name stays the same.
type Position struct {
	Offset int64  `json:"off"`
	FPLen  int    `json:"fplen"`
	FP     string `json:"fp"`
}

const (
	fingerprintBytes = 256
	readBufferSize   = 1 << 20
)

// Tailer follows a growing log file, surviving rotation and truncation.
type Tailer struct {
	Path     string
	Loc      *time.Location
	Interval time.Duration
	Start    Position

	// Handle receives every parsed entry. Its strings are only valid during
	// the call.
	Handle func(parser.Entry)
	// Commit is called after each burst of reading (and on idle ticks) with
	// the position just after the last fully processed line. lines is the
	// number of lines read since the previous Commit.
	Commit func(pos Position, lines int64) error
	// Warn receives non-fatal problems (file missing, unreadable).
	Warn func(err error)
}

// Run blocks until ctx is cancelled or Commit fails.
func (t *Tailer) Run(ctx context.Context) error {
	if t.Interval <= 0 {
		t.Interval = time.Second
	}
	if t.Loc == nil {
		t.Loc = time.Local
	}
	buf := make([]byte, readBufferSize)
	var (
		f       *os.File
		fst     os.FileInfo
		off     int64 // offset of the first unprocessed byte (start of the carried partial line)
		have    int   // bytes of partial line carried in buf
		fresh   = true
		lines   int64
		pending = t.Start
	)
	defer func() {
		if f != nil {
			f.Close()
		}
	}()
	warn := func(err error) {
		if t.Warn != nil {
			t.Warn(err)
		}
	}
	sleep := func() bool {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(t.Interval):
			return true
		}
	}

	for ctx.Err() == nil {
		if f == nil {
			var err error
			f, err = os.Open(t.Path)
			if err != nil {
				warn(err)
				if !sleep() {
					break
				}
				continue
			}
			if fst, err = f.Stat(); err != nil {
				f.Close()
				f = nil
				warn(err)
				if !sleep() {
					break
				}
				continue
			}
			off, have = 0, 0
			if fresh {
				off = t.resume(f, fst.Size())
				fresh = false
			}
			if _, err := f.Seek(off, io.SeekStart); err != nil {
				return err
			}
		}

		// Has the path been replaced (rotation) or truncated?
		moved := false
		if st, err := os.Stat(t.Path); err == nil && !os.SameFile(fst, st) {
			moved = true
		} else if err == nil && st.Size() < off+int64(have) {
			// truncated in place (copytruncate): start over
			if _, err := f.Seek(0, io.SeekStart); err != nil {
				return err
			}
			off, have = 0, 0
		}

		// Read everything that is available. Commit after every chunk so a
		// large backlog is flushed in small batches instead of piling up.
		commit := func() error {
			pending = Position{Offset: off}
			pending.FPLen, pending.FP = fingerprint(f)
			err := t.Commit(pending, lines)
			lines = 0
			return err
		}
		for ctx.Err() == nil {
			n, err := f.Read(buf[have:])
			if n > 0 {
				data := buf[:have+n]
				consumed := t.process(data, &lines)
				off += int64(consumed)
				have = copy(buf, data[consumed:])
				if have == len(buf) { // absurdly long line without newline: drop it
					off += int64(have)
					have = 0
				}
				if err := commit(); err != nil {
					return err
				}
			}
			if err != nil {
				if !errors.Is(err, io.EOF) {
					warn(err)
				}
				break
			}
			if n == 0 {
				break
			}
		}
		if err := commit(); err != nil {
			return err
		}

		if moved {
			f.Close()
			f = nil
			continue
		}
		if !sleep() {
			break
		}
	}
	return nil
}

// resume decides where to continue reading in a freshly opened file.
func (t *Tailer) resume(f *os.File, size int64) int64 {
	p := t.Start
	if p.Offset <= 0 || p.FPLen <= 0 || p.Offset > size {
		return 0
	}
	n, fp := fingerprintN(f, p.FPLen)
	if n != p.FPLen || fp != p.FP {
		return 0
	}
	return p.Offset
}

// process parses all complete lines in data and returns the bytes consumed.
func (t *Tailer) process(data []byte, lines *int64) int {
	consumed := 0
	for {
		i := bytes.IndexByte(data[consumed:], '\n')
		if i < 0 {
			return consumed
		}
		line := data[consumed : consumed+i]
		consumed += i + 1
		*lines++
		if n := len(line); n > 0 && line[n-1] == '\r' {
			line = line[:n-1]
		}
		if len(line) == 0 {
			continue
		}
		s := unsafe.String(&line[0], len(line))
		if e, ok := parser.ParseLine(s, t.Loc); ok {
			t.Handle(e)
		}
	}
}

func fingerprint(f *os.File) (int, string) {
	return fingerprintN(f, fingerprintBytes)
}

// fingerprintN hashes the first n bytes of f (fewer if the file is shorter).
func fingerprintN(f *os.File, n int) (int, string) {
	b := make([]byte, n)
	got, _ := f.ReadAt(b, 0)
	if got <= 0 {
		return 0, ""
	}
	sum := sha1.Sum(b[:got])
	return got, hex.EncodeToString(sum[:8])
}

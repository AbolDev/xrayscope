package collect

import (
	"context"
	"encoding/json"
	"time"

	"xrayscope/internal/model"
	"xrayscope/internal/parser"
)

// Sink receives drained records together with the log position that is
// consistent with them. A returned error stops the pipeline.
type Sink interface {
	Flush(recs []model.Rec, pos Position, lines int64) error
}

// PipelineConfig configures a Pipeline.
type PipelineConfig struct {
	Tailer     Tailer
	Sink       Sink
	FlushEvery time.Duration // default 5s
	MaxKeys    int           // flush early above this many keys (default 50000)
	// OnEntry, when set, sees (and may adjust) every entry before it is
	// counted. The entry's strings point into a reusable buffer.
	OnEntry func(e *parser.Entry)
}

// Run tails the log and flushes aggregates to the sink until ctx ends.
func Run(ctx context.Context, cfg PipelineConfig) error {
	if cfg.FlushEvery <= 0 {
		cfg.FlushEvery = 5 * time.Second
	}
	if cfg.MaxKeys <= 0 {
		cfg.MaxKeys = 50000
	}
	agg := NewAggregator()
	last := time.Now()
	var lines int64
	var lastPos Position
	dirty := false

	t := cfg.Tailer
	t.Handle = agg.Add
	if cfg.OnEntry != nil {
		t.Handle = func(e parser.Entry) {
			cfg.OnEntry(&e)
			agg.Add(e)
		}
	}
	t.Commit = func(pos Position, n int64) error {
		lines += n
		if n > 0 {
			dirty = true
		}
		lastPos = pos
		if !dirty || (time.Since(last) < cfg.FlushEvery && agg.Len() < cfg.MaxKeys) {
			return nil
		}
		if err := cfg.Sink.Flush(agg.Drain(), pos, lines); err != nil {
			return err
		}
		last, lines, dirty = time.Now(), 0, false
		return nil
	}
	err := t.Run(ctx)
	// final flush so a clean shutdown loses nothing
	if dirty {
		if ferr := cfg.Sink.Flush(agg.Drain(), lastPos, lines); ferr != nil && err == nil {
			err = ferr
		}
	}
	return err
}

// EncodePosition serialises a position for storage.
func EncodePosition(p Position) string {
	b, _ := json.Marshal(p)
	return string(b)
}

// DecodePosition parses a stored position; the zero value means "from start".
func DecodePosition(s string) Position {
	var p Position
	_ = json.Unmarshal([]byte(s), &p)
	return p
}

package collect

import (
	"strings"

	"xrayscope/internal/model"
	"xrayscope/internal/parser"
)

type recKey struct {
	m        int64
	user, ip string
	host     string
}

// Aggregator folds log entries into per-minute counters. It is not safe for
// concurrent use; the tailer goroutine owns it.
type Aggregator struct {
	idx    map[recKey]int // key -> position in counts
	counts []int
}

// NewAggregator creates an empty aggregator.
func NewAggregator() *Aggregator {
	return &Aggregator{idx: make(map[recKey]int, 4096)}
}

// Add counts one entry. The entry's strings may point into a reusable read
// buffer, so they are copied when a new key is created. An existing key must
// never be assigned again: Go replaces the stored key on assignment, which
// would make it point into the buffer.
func (a *Aggregator) Add(e parser.Entry) {
	k := recKey{m: e.Time.Unix() / 60, user: e.Email, ip: e.SrcIP, host: e.Host}
	if i, ok := a.idx[k]; ok {
		a.counts[i]++
		return
	}
	k.user, k.ip, k.host = strings.Clone(k.user), strings.Clone(k.ip), strings.Clone(k.host)
	a.idx[k] = len(a.counts)
	a.counts = append(a.counts, 1)
}

// Len is the number of distinct keys currently held.
func (a *Aggregator) Len() int { return len(a.idx) }

// Drain returns the accumulated records and resets the aggregator.
func (a *Aggregator) Drain() []model.Rec {
	out := make([]model.Rec, 0, len(a.idx))
	for k, i := range a.idx {
		out = append(out, model.Rec{M: k.m, U: k.user, IP: k.ip, D: k.host, N: a.counts[i]})
	}
	clear(a.idx)
	a.counts = a.counts[:0]
	return out
}

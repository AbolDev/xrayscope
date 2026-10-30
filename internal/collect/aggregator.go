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
	m     map[recKey]int
	lines int64
}

// NewAggregator creates an empty aggregator.
func NewAggregator() *Aggregator { return &Aggregator{m: make(map[recKey]int, 4096)} }

// Add counts one entry. The entry's strings may point into a reusable
// buffer; they are copied only when a new key is created.
func (a *Aggregator) Add(e parser.Entry) {
	k := recKey{m: e.Time.Unix() / 60, user: e.Email, ip: e.SrcIP, host: e.Host}
	if n, ok := a.m[k]; ok {
		a.m[k] = n + 1
		return
	}
	k.user, k.ip, k.host = strings.Clone(k.user), strings.Clone(k.ip), strings.Clone(k.host)
	a.m[k] = 1
}

// Len is the number of distinct keys currently held.
func (a *Aggregator) Len() int { return len(a.m) }

// Drain returns the accumulated records and resets the aggregator.
func (a *Aggregator) Drain() []model.Rec {
	out := make([]model.Rec, 0, len(a.m))
	for k, n := range a.m {
		out = append(out, model.Rec{M: k.m, U: k.user, IP: k.ip, D: k.host, N: n})
	}
	clear(a.m)
	return out
}

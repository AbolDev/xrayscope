package xraystats

import (
	"context"
	"sort"
	"sync"
	"time"
)

// Rate is a transfer speed in bytes per second.
type Rate struct {
	Up   int64 `json:"up"`
	Down int64 `json:"down"`
}

// Point is one sample of a user's speed history.
type Point struct {
	T    int64 `json:"t"` // unix seconds
	Up   int64 `json:"up"`
	Down int64 `json:"down"`
}

const (
	historyLen  = 300 // samples kept per user (five minutes at one second)
	idleSamples = 60  // drop a user's history after this many silent samples

	// ActiveMinBytes is how much a user must transfer within one minute for
	// that minute to count as real use rather than keep-alive chatter.
	ActiveMinBytes = 30 << 10

	// HeavyMinBytes marks a minute of sustained transfer, such as a video.
	HeavyMinBytes = 1 << 20
)

// ActiveMark is the set of minutes of one hour with real traffic.
type ActiveMark struct {
	Email string
	Hour  int64
	Mask  int64 // minutes with at least ActiveMinBytes
	Heavy int64 // minutes with at least HeavyMinBytes
}

type minuteMarks struct{ active, heavy int64 }

// Tracker samples a Querier and derives per-user speeds from the counter
// deltas. It is safe for concurrent use.
type Tracker struct {
	q Querier

	mu      sync.RWMutex
	prev    map[string][2]int64
	prevAt  time.Time
	rates   map[string]Rate
	hist    map[string][]Point
	quiet   map[string]int
	updated time.Time
	err     error

	activeMin, heavyMin int64 // thresholds in bytes per minute (0 = default)

	minute int64            // the minute being accumulated (unix minutes)
	bytes  map[string]int64 // bytes moved so far in that minute
	marks  map[string]map[int64]minuteMarks
}

// NewTracker creates a tracker around a querier.
func NewTracker(q Querier) *Tracker {
	return &Tracker{q: q, rates: map[string]Rate{}, hist: map[string][]Point{}, quiet: map[string]int{},
		bytes: map[string]int64{}, marks: map[string]map[int64]minuteMarks{}}
}

// SetThresholds changes how much data within a minute makes it a minute of
// real use and of sustained transfer. Zero keeps the default.
func (t *Tracker) SetThresholds(active, heavy int64) {
	t.mu.Lock()
	t.activeMin, t.heavyMin = active, heavy
	t.mu.Unlock()
}

// Thresholds returns the active and heavy minute thresholds in bytes.
func (t *Tracker) Thresholds() (active, heavy int64) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	active, heavy = ActiveMinBytes, HeavyMinBytes
	if t.activeMin > 0 {
		active = t.activeMin
	}
	if t.heavyMin > 0 {
		heavy = t.heavyMin
	}
	return active, heavy
}

// account adds one sample's transfer to the current minute and closes the
// previous minute when a new one starts. The caller holds t.mu.
func (t *Tracker) account(now time.Time, moved map[string]int64) {
	activeMin, heavyMin := int64(ActiveMinBytes), int64(HeavyMinBytes)
	if t.activeMin > 0 {
		activeMin = t.activeMin
	}
	if t.heavyMin > 0 {
		heavyMin = t.heavyMin
	}
	min := now.Unix() / 60
	if t.minute != 0 && min != t.minute {
		for email, b := range t.bytes {
			if b >= activeMin {
				m := t.marks[email]
				if m == nil {
					m = map[int64]minuteMarks{}
					t.marks[email] = m
				}
				bit := int64(1) << uint(t.minute%60)
				mm := m[t.minute/60]
				mm.active |= bit
				if b >= heavyMin {
					mm.heavy |= bit
				}
				m[t.minute/60] = mm
			}
		}
		t.bytes = map[string]int64{}
	}
	t.minute = min
	for email, b := range moved {
		t.bytes[email] += b
	}
}

// DrainActive returns the minutes finished since the last call and forgets
// them. The minute still in progress is kept for the next call.
func (t *Tracker) DrainActive() []ActiveMark {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []ActiveMark
	for email, hours := range t.marks {
		for h, m := range hours {
			out = append(out, ActiveMark{Email: email, Hour: h, Mask: m.active, Heavy: m.heavy})
		}
	}
	t.marks = map[string]map[int64]minuteMarks{}
	return out
}

// Run samples every interval until ctx ends.
func (t *Tracker) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		t.Sample(ctx, time.Now())
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// Sample takes one reading at the given time.
func (t *Tracker) Sample(ctx context.Context, now time.Time) {
	cur, err := t.q.Query(ctx)
	t.mu.Lock()
	defer t.mu.Unlock()
	if err != nil {
		t.err = err
		t.rates = map[string]Rate{}
		t.prev = nil
		return
	}
	t.err = nil
	t.updated = now
	rates := make(map[string]Rate, len(cur))
	moved := map[string]int64{}
	if t.prev != nil {
		dt := now.Sub(t.prevAt).Seconds()
		if dt > 0 {
			for email, c := range cur {
				p, seen := t.prev[email]
				if !seen {
					continue
				}
				up, down := c[0]-p[0], c[1]-p[1]
				if up < 0 || down < 0 {
					continue // Xray restarted and its counters began again
				}
				if up == 0 && down == 0 {
					continue
				}
				rates[email] = Rate{Up: int64(float64(up) / dt), Down: int64(float64(down) / dt)}
				moved[email] = up + down
			}
		}
	}
	t.account(now, moved)
	t.rates = rates
	t.prev, t.prevAt = cur, now

	// history for users that are (or were just) active
	for email, r := range rates {
		h := append(t.hist[email], Point{T: now.Unix(), Up: r.Up, Down: r.Down})
		if len(h) > historyLen {
			h = h[len(h)-historyLen:]
		}
		t.hist[email] = h
		t.quiet[email] = 0
	}
	for email := range t.hist {
		if _, active := rates[email]; active {
			continue
		}
		t.quiet[email]++
		h := append(t.hist[email], Point{T: now.Unix()})
		if len(h) > historyLen {
			h = h[len(h)-historyLen:]
		}
		t.hist[email] = h
		if t.quiet[email] > idleSamples && allZero(h[len(h)-idleSamples:]) {
			delete(t.hist, email)
			delete(t.quiet, email)
		}
	}
}

func allZero(ps []Point) bool {
	for _, p := range ps {
		if p.Up != 0 || p.Down != 0 {
			return false
		}
	}
	return true
}

// Rates returns the latest speed of every user that moved data.
func (t *Tracker) Rates() map[string]Rate {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make(map[string]Rate, len(t.rates))
	for k, v := range t.rates {
		out[k] = v
	}
	return out
}

// Total is the sum of all current speeds.
func (t *Tracker) Total() Rate {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var s Rate
	for _, r := range t.rates {
		s.Up += r.Up
		s.Down += r.Down
	}
	return s
}

// History returns the recent speed samples of a user, oldest first.
func (t *Tracker) History(email string) []Point {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return append([]Point(nil), t.hist[email]...)
}

// Status reports when the last successful reading happened and the last error.
func (t *Tracker) Status() (updated time.Time, err error) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.updated, t.err
}

// Top returns the n fastest users by combined speed.
func (t *Tracker) Top(n int) []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	emails := make([]string, 0, len(t.rates))
	for e := range t.rates {
		emails = append(emails, e)
	}
	sort.Slice(emails, func(i, j int) bool {
		a, b := t.rates[emails[i]], t.rates[emails[j]]
		return a.Up+a.Down > b.Up+b.Down
	})
	if len(emails) > n {
		emails = emails[:n]
	}
	return emails
}

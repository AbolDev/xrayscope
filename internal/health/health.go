// Package health watches the access log for configuration problems that
// silently make the analysis wrong: inbounds without sniffing (no domain
// names), inbounds behind a CDN without the real client address, and a log
// written in a different time zone than XrayScope reads it in.
package health

import (
	"math"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"xrayscope/internal/classify"
	"xrayscope/internal/parser"
)

// Inbound is what one inbound tag looked like recently.
type Inbound struct {
	Tag      string `json:"tag"`
	Requests int64  `json:"requests"`
	IPDest   int64  `json:"ip_dest"`   // destinations without a domain name
	CDNSrc   int64  `json:"cdn_src"`   // client addresses that are CDN edges
	Users    int    `json:"users"`     // distinct users
	LastSeen int64  `json:"last_seen"` // unix seconds
	// QUIC (UDP 443) connections, and those of them without a domain name:
	// a high share means "quic" is missing from the sniffing destOverride.
	Quic   int64 `json:"quic"`
	QuicIP int64 `json:"quic_ip"`
}

type inbound struct {
	Inbound
	users map[string]struct{}
}

// Monitor collects the statistics. It is safe for concurrent use.
type Monitor struct {
	mu      sync.Mutex
	cur     map[string]*inbound // the current period
	prev    map[string]*inbound // the period before, so a reset never empties the view
	started time.Time

	clock Clock
}

// Period is how long counts are kept before they roll over.
const Period = 6 * time.Hour

// New creates a monitor.
func New() *Monitor {
	return &Monitor{cur: map[string]*inbound{}, prev: map[string]*inbound{}, started: time.Now()}
}

// Observe records one log entry. now is the wall clock at the time the line
// was read. The entry's strings may point into a reusable buffer.
func (m *Monitor) Observe(e parser.Entry, now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if now.Sub(m.started) > Period {
		m.prev, m.cur, m.started = m.cur, map[string]*inbound{}, now
	}
	in := m.cur[e.InTag]
	if in == nil {
		tag := strings.Clone(e.InTag)
		in = &inbound{Inbound: Inbound{Tag: tag}, users: map[string]struct{}{}}
		m.cur[tag] = in
	}
	in.Requests++
	_, err := netip.ParseAddr(e.Host)
	bare := err == nil
	if bare {
		in.IPDest++
	}
	if e.Network == "udp" && e.Port == 443 {
		in.Quic++
		if bare {
			in.QuicIP++
		}
	}
	if classify.CDN(e.SrcIP) != "" {
		in.CDNSrc++
	}
	if _, ok := in.users[e.Email]; !ok && len(in.users) < 100000 {
		in.users[strings.Clone(e.Email)] = struct{}{}
	}
	if t := e.Time.Unix(); t > in.LastSeen {
		in.LastSeen = t
	}
	m.clock.observe(e.Time, now)
}

// Inbounds returns the recent statistics per inbound, busiest first.
func (m *Monitor) Inbounds() []Inbound {
	m.mu.Lock()
	defer m.mu.Unlock()
	merged := map[string]*Inbound{}
	users := map[string]map[string]struct{}{}
	for _, src := range []map[string]*inbound{m.prev, m.cur} {
		for tag, in := range src {
			x := merged[tag]
			if x == nil {
				x = &Inbound{Tag: tag}
				merged[tag] = x
				users[tag] = map[string]struct{}{}
			}
			x.Requests += in.Requests
			x.IPDest += in.IPDest
			x.CDNSrc += in.CDNSrc
			x.Quic += in.Quic
			x.QuicIP += in.QuicIP
			if in.LastSeen > x.LastSeen {
				x.LastSeen = in.LastSeen
			}
			for u := range in.users {
				users[tag][u] = struct{}{}
			}
		}
	}
	out := make([]Inbound, 0, len(merged))
	for tag, x := range merged {
		x.Users = len(users[tag])
		out = append(out, *x)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Requests > out[j].Requests })
	return out
}

// ClockFixer moves log timestamps by the detected time zone error. It starts
// from a remembered shift and follows the monitor once it is sure.
type ClockFixer struct {
	Monitor *Monitor
	// Save is called whenever the applied shift changes (minutes).
	Save func(min int)

	shift   atomic.Int64 // minutes subtracted from log times
	checked time.Time
	since   int // entries since the last check
}

// NewClockFixer starts with a remembered shift.
func NewClockFixer(m *Monitor, remembered int, save func(int)) *ClockFixer {
	f := &ClockFixer{Monitor: m, Save: save}
	f.shift.Store(int64(remembered))
	return f
}

// Fix observes an entry (with its original time) and corrects its time. It
// is called from the single goroutine that reads the log.
func (f *ClockFixer) Fix(e *parser.Entry, now time.Time) {
	f.Monitor.Observe(*e, now)
	f.since++
	if f.since >= 256 || now.Sub(f.checked) > time.Minute {
		f.checked, f.since = now, 0
		if st := f.Monitor.Clock(); st.Sure && int64(st.OffsetMin) != f.shift.Load() {
			f.shift.Store(int64(st.OffsetMin))
			if f.Save != nil {
				f.Save(st.OffsetMin)
			}
		}
	}
	if s := f.shift.Load(); s != 0 {
		e.Time = e.Time.Add(-time.Duration(s) * time.Minute)
	}
}

// Shift returns the correction in minutes currently applied.
func (f *ClockFixer) Shift() int { return int(f.shift.Load()) }

// Clock returns the detected time offset of the log.
func (m *Monitor) Clock() ClockState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.clock.state()
}

// ---------------------------------------------------------------- clock

// ClockState describes how the log's timestamps relate to the real time.
type ClockState struct {
	// Samples is how many recent lines were compared.
	Samples int `json:"samples"`
	// OffsetMin is the detected shift in minutes (log time minus real time),
	// rounded to a quarter hour. Zero means the time zone is right.
	OffsetMin int `json:"offset_min"`
	// Sure is true once the shift has been the same for long enough to act on.
	Sure bool `json:"sure"`
}

// A line read within this long of being written tells the true offset.
const (
	clockWindow  = 10 * time.Minute
	clockSamples = 200
	clockSpread  = 3 * time.Minute
	clockSpan    = 2 * time.Minute
)

type clockSample struct {
	at  time.Time
	lag time.Duration // log time - read time
}

// Clock compares log timestamps with the moment the line is read. While the
// collector keeps up, every line is read within seconds of being written, so
// the difference is the time zone error. While it catches up on a backlog
// the differences spread out and nothing is concluded.
type Clock struct {
	samples []clockSample
}

func (c *Clock) observe(logTime, now time.Time) {
	c.samples = append(c.samples, clockSample{now, logTime.Sub(now)})
	cut := now.Add(-clockWindow)
	i := 0
	for i < len(c.samples) && c.samples[i].at.Before(cut) {
		i++
	}
	if i > 0 {
		c.samples = append(c.samples[:0], c.samples[i:]...)
	}
	if len(c.samples) > 4*clockSamples {
		c.samples = append(c.samples[:0], c.samples[len(c.samples)-2*clockSamples:]...)
	}
}

func (c *Clock) state() ClockState {
	st := ClockState{Samples: len(c.samples)}
	if len(c.samples) < clockSamples {
		return st
	}
	// a backlog is read in seconds; only lines read over minutes of steady
	// following tell the time zone (old lines read late would look shifted)
	if c.samples[len(c.samples)-1].at.Sub(c.samples[0].at) < clockSpan {
		return st
	}
	// the most recent lines are the freshest; compare their typical lag
	lags := make([]time.Duration, len(c.samples))
	for i, s := range c.samples {
		lags[i] = s.lag
	}
	sort.Slice(lags, func(i, j int) bool { return lags[i] < lags[j] })
	lo, hi := lags[len(lags)/10], lags[len(lags)*9/10]
	if hi-lo > clockSpread {
		return st // a backlog or an irregular writer: not conclusive
	}
	med := lags[len(lags)/2]
	st.OffsetMin = int(math.Round(float64(med)/float64(15*time.Minute))) * 15
	st.Sure = true
	return st
}

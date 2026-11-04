package analytics

import (
	"sort"

	"xrayscope/internal/store"
)

// Session is a continuous stretch of activity. Times are unix minutes.
type Session struct {
	Start  int64 `json:"start"`
	End    int64 `json:"end"`
	Active int   `json:"active"` // minutes with traffic inside the session
	IPID   int64 `json:"ip_id,omitempty"`
}

// Sessions groups sorted unique active minutes into sessions: a new session
// starts when more than gap minutes pass without traffic.
func Sessions(mins []int64, gap int64) []Session {
	var out []Session
	for _, m := range mins {
		if n := len(out); n > 0 && m-out[n-1].End <= gap {
			out[n-1].End = m
			out[n-1].Active++
			continue
		}
		out = append(out, Session{Start: m, End: m, Active: 1})
	}
	return out
}

// UserSessions returns the sessions of a user across all of its IPs.
func UserSessions(rows []store.ActRow, gap int64) []Session {
	return Sessions(UnionMinutes(rows, Presence), gap)
}

// IPSessions returns sessions per client IP, sorted by start time.
func IPSessions(rows []store.ActRow, gap int64) []Session {
	by := map[int64][]store.ActRow{}
	for _, r := range rows {
		by[r.IPID] = append(by[r.IPID], r)
	}
	var out []Session
	for ip, rs := range by {
		for _, s := range Sessions(UnionMinutes(rs, Presence), gap) {
			s.IPID = ip
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Start != out[j].Start {
			return out[i].Start < out[j].Start
		}
		return out[i].IPID < out[j].IPID
	})
	return out
}

// Run is a continuous stretch of activity of a single IP.
type Run struct {
	IPID  int64
	Start int64
	End   int64
	// Dev groups runs that are the same device: a phone whose address
	// changed (mobile networks do this often) keeps one Dev. Zero means the
	// run is its own device.
	Dev int64
}

func (r Run) device() int64 {
	if r.Dev != 0 {
		return r.Dev
	}
	return r.IPID
}

// HopSlack is how much two addresses of one device may overlap when it
// switches from one to the other, in minutes.
const HopSlack = 2

// MergeHops marks runs that continue another run on a new address of the
// same network as the same device. sameNet reports whether two IP ids
// belong to one operator (same ASN, or the same address block).
func MergeHops(runs []Run, sameNet func(a, b int64) bool) []Run {
	out := append([]Run(nil), runs...)
	sort.Slice(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	for i := range out {
		b := &out[i]
		for j := i - 1; j >= 0; j-- {
			a := out[j]
			if b.Start-a.Start > 7*24*60 {
				break // runs are sorted by start; nothing older can still be open
			}
			if a.IPID == b.IPID {
				continue
			}
			// b takes over from a: it starts as a ends and outlives it
			if b.Start >= a.End-HopSlack && b.Start <= a.End+HopSlack && b.End > a.End && sameNet(a.IPID, b.IPID) {
				b.Dev = a.device()
				break
			}
		}
	}
	return out
}

// IPRuns merges the active minutes of each IP into runs separated by more
// than gap minutes.
func IPRuns(rows []store.ActRow, gap int64) []Run {
	var out []Run
	for _, s := range IPSessions(rows, gap) {
		out = append(out, Run{IPID: s.IPID, Start: s.Start, End: s.End})
	}
	return out
}

// Overlap is a period where several client IPs of one user were active at
// once, which usually means several devices (or a device switching networks).
type Overlap struct {
	Start int64   `json:"start"`
	End   int64   `json:"end"`
	Peak  int     `json:"peak"`
	IPs   []int64 `json:"ips"`
}

// Overlaps finds periods with two or more simultaneously active IPs lasting
// at least minLen minutes.
func Overlaps(runs []Run, minLen int64) []Overlap {
	type ev struct {
		t     int64
		delta int
		dev   int64
		ip    int64
	}
	evs := make([]ev, 0, len(runs)*2)
	for _, r := range runs {
		evs = append(evs, ev{r.Start, +1, r.device(), r.IPID}, ev{r.End + 1, -1, r.device(), r.IPID})
	}
	sort.Slice(evs, func(i, j int) bool {
		if evs[i].t != evs[j].t {
			return evs[i].t < evs[j].t
		}
		return evs[i].delta < evs[j].delta // ends before starts
	})

	active := map[int64]int{}   // device -> open runs
	activeIP := map[int64]int{} // ip -> open runs
	var out []Overlap
	var cur *Overlap
	seen := map[int64]bool{}
	flush := func() {
		if cur == nil {
			return
		}
		if cur.End-cur.Start+1 >= minLen {
			for ip := range seen {
				cur.IPs = append(cur.IPs, ip)
			}
			sort.Slice(cur.IPs, func(i, j int) bool { return cur.IPs[i] < cur.IPs[j] })
			out = append(out, *cur)
		}
		cur = nil
		seen = map[int64]bool{}
	}
	for i := 0; i < len(evs); {
		t := evs[i].t
		for ; i < len(evs) && evs[i].t == t; i++ {
			active[evs[i].dev] += evs[i].delta
			if active[evs[i].dev] == 0 {
				delete(active, evs[i].dev)
			}
			activeIP[evs[i].ip] += evs[i].delta
			if activeIP[evs[i].ip] == 0 {
				delete(activeIP, evs[i].ip)
			}
		}
		if i >= len(evs) {
			flush()
			break
		}
		next := evs[i].t
		if len(active) >= 2 {
			if cur == nil {
				cur = &Overlap{Start: t}
			}
			cur.End = next - 1
			if len(active) > cur.Peak {
				cur.Peak = len(active)
			}
			for ip := range activeIP {
				seen[ip] = true
			}
		} else {
			flush()
		}
	}
	return out
}

// MaxConcurrent returns the highest number of simultaneously active IPs.
func MaxConcurrent(runs []Run) int {
	peak := 0
	for _, o := range Overlaps(runs, 1) {
		if o.Peak > peak {
			peak = o.Peak
		}
	}
	if peak == 0 && len(runs) > 0 {
		return 1
	}
	return peak
}

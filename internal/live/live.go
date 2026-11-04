// Package live keeps an in-memory view of who is connected right now.
package live

import (
	"sort"
	"sync"
	"time"

	"xrayscope/internal/store"
)

// Conn is one client IP of an online user.
type Conn struct {
	IP       string `json:"ip"`
	LastSeen int64  `json:"last_seen"` // unix seconds
	// Replaced is set when a newer address of the same operator took over
	// right as this one went quiet: the same device on a new address.
	Replaced bool `json:"replaced,omitempty"`
}

// Online is a user with recent traffic.
type Online struct {
	UserID   int64  `json:"user_id"`
	Email    string `json:"email"`
	LastSeen int64  `json:"last_seen"`
	Conns    []Conn `json:"conns"`
	// Devices is the number of addresses in use at the same time, not
	// counting addresses a device left behind when its address changed.
	Devices int `json:"devices"`
}

// hopSlack is how much a device's old and new address may overlap, minutes.
const hopSlack = 2

type seen struct{ first, last int64 } // unix minutes

type userState struct {
	email string
	ips   map[string]*seen
}

// Tracker records the most recent activity per user and IP.
type Tracker struct {
	mu    sync.RWMutex
	users map[int64]*userState

	// SameNet reports whether two addresses belong to one operator. When
	// nil, address changes are never merged.
	SameNet func(a, b string) bool
}

// New creates an empty tracker.
func New() *Tracker { return &Tracker{users: map[int64]*userState{}} }

// Observe records activity reported by the store.
func (t *Tracker) Observe(obs []store.Obs) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, o := range obs {
		u := t.users[o.UserID]
		if u == nil {
			u = &userState{email: o.Email, ips: map[string]*seen{}}
			t.users[o.UserID] = u
		}
		s := u.ips[o.IP]
		if s == nil {
			u.ips[o.IP] = &seen{first: o.Minute, last: o.Minute}
			continue
		}
		if o.Minute > s.last {
			s.last = o.Minute
		}
		if o.Minute < s.first {
			s.first = o.Minute
		}
	}
}

// Prune forgets IPs not seen for longer than keep.
func (t *Tracker) Prune(keep time.Duration) {
	cut := time.Now().Add(-keep).Unix() / 60
	t.mu.Lock()
	defer t.mu.Unlock()
	for id, u := range t.users {
		for ip, s := range u.ips {
			if s.last < cut {
				delete(u.ips, ip)
			}
		}
		if len(u.ips) == 0 {
			delete(t.users, id)
		}
	}
}

// Online returns users with traffic in the last window, most recent first.
// A minute that is still running counts as active.
func (t *Tracker) Online(window time.Duration) []Online {
	cut := time.Now().Add(-window).Unix() / 60
	t.mu.RLock()
	defer t.mu.RUnlock()
	var out []Online
	for id, u := range t.users {
		o := Online{UserID: id, Email: u.email}
		for ip, s := range u.ips {
			if s.last < cut {
				continue
			}
			c := Conn{IP: ip, LastSeen: s.last * 60}
			if t.SameNet != nil {
				for ip2, s2 := range u.ips {
					// a newer address of the same operator started as this
					// one stopped: the device moved
					if ip2 != ip && s2.last > s.last && s2.first >= s.last-hopSlack && s2.first <= s.last+hopSlack && t.SameNet(ip, ip2) {
						c.Replaced = true
						break
					}
				}
			}
			if !c.Replaced {
				o.Devices++
			}
			o.Conns = append(o.Conns, c)
			if c.LastSeen > o.LastSeen {
				o.LastSeen = c.LastSeen
			}
		}
		if len(o.Conns) == 0 {
			continue
		}
		sort.Slice(o.Conns, func(i, j int) bool { return o.Conns[i].LastSeen > o.Conns[j].LastSeen })
		out = append(out, o)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].LastSeen != out[j].LastSeen {
			return out[i].LastSeen > out[j].LastSeen
		}
		return out[i].Email < out[j].Email
	})
	return out
}

// OnlineUser returns the live state of one user.
func (t *Tracker) OnlineUser(id int64, window time.Duration) (Online, bool) {
	for _, o := range t.Online(window) {
		if o.UserID == id {
			return o, true
		}
	}
	return Online{}, false
}

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
}

// Online is a user with recent traffic.
type Online struct {
	UserID   int64  `json:"user_id"`
	Email    string `json:"email"`
	LastSeen int64  `json:"last_seen"`
	Conns    []Conn `json:"conns"`
}

type userState struct {
	email string
	ips   map[string]int64 // ip -> last unix minute
}

// Tracker records the most recent activity per user and IP.
type Tracker struct {
	mu    sync.RWMutex
	users map[int64]*userState
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
			u = &userState{email: o.Email, ips: map[string]int64{}}
			t.users[o.UserID] = u
		}
		if o.Minute > u.ips[o.IP] {
			u.ips[o.IP] = o.Minute
		}
	}
}

// Prune forgets IPs not seen for longer than keep.
func (t *Tracker) Prune(keep time.Duration) {
	cut := time.Now().Add(-keep).Unix() / 60
	t.mu.Lock()
	defer t.mu.Unlock()
	for id, u := range t.users {
		for ip, m := range u.ips {
			if m < cut {
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
		for ip, m := range u.ips {
			if m >= cut {
				o.Conns = append(o.Conns, Conn{IP: ip, LastSeen: m * 60})
				if m*60 > o.LastSeen {
					o.LastSeen = m * 60
				}
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

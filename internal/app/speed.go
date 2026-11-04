package app

import (
	"context"
	"sort"
	"sync"
	"time"

	"xrayscope/internal/model"
	"xrayscope/internal/panel"
	"xrayscope/internal/xraystats"
)

// apiFinder asks the panel adapter where Xray's statistics API listens.
func (a *App) apiFinder() (string, bool) {
	if p, ok := a.Panel.(panel.APIProvider); ok {
		return p.XrayAPI()
	}
	return "", false
}

// SpeedBook merges the live speeds of this server and of remote nodes.
type SpeedBook struct {
	mu     sync.Mutex
	remote map[string]remoteSpeed
}

type remoteSpeed struct {
	at    time.Time
	rates map[string]xraystats.Rate
}

// NewSpeedBook creates an empty book.
func NewSpeedBook() *SpeedBook { return &SpeedBook{remote: map[string]remoteSpeed{}} }

// Set records the latest speeds reported by a remote source.
func (b *SpeedBook) Set(source string, rates map[string]xraystats.Rate) {
	b.mu.Lock()
	b.remote[source] = remoteSpeed{time.Now(), rates}
	b.mu.Unlock()
}

// Drop forgets a source.
func (b *SpeedBook) Drop(source string) {
	b.mu.Lock()
	delete(b.remote, source)
	b.mu.Unlock()
}

const speedFresh = 20 * time.Second

// Merge adds the fresh remote speeds to local.
func (b *SpeedBook) Merge(local map[string]xraystats.Rate) map[string]xraystats.Rate {
	out := make(map[string]xraystats.Rate, len(local))
	for k, v := range local {
		out[k] = v
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for src, rs := range b.remote {
		if time.Since(rs.at) > speedFresh {
			delete(b.remote, src)
			continue
		}
		for email, r := range rs.rates {
			c := out[email]
			c.Up += r.Up
			c.Down += r.Down
			out[email] = c
		}
	}
	return out
}

// SpeedRow is one user's current speed.
type SpeedRow struct {
	UserID int64  `json:"user_id"`
	Email  string `json:"email"`
	Up     int64  `json:"up"`
	Down   int64  `json:"down"`
}

// SpeedReport is the live speed of every user and their sum.
type SpeedReport struct {
	Available bool       `json:"available"`
	Error     string     `json:"error,omitempty"`
	Total     SpeedRate  `json:"total"`
	Users     []SpeedRow `json:"users"`
}

// SpeedRate is a pair of transfer rates in bytes per second.
type SpeedRate struct {
	Up   int64 `json:"up"`
	Down int64 `json:"down"`
}

// Speeds returns the live speeds of this server plus fresh remote reports.
func (a *App) Speeds(ctx context.Context, limit int) SpeedReport {
	var rep SpeedReport
	rep.Users = []SpeedRow{}
	local := a.Speed.Rates()
	updated, err := a.Speed.Status()
	rep.Available = !updated.IsZero() && err == nil
	if err != nil {
		rep.Error = err.Error()
	}
	all := a.SpeedBook.Merge(local)
	if len(all) > 0 {
		rep.Available = true
	}
	ids := map[string]int64{}
	if users, e := a.Store.Users(ctx); e == nil {
		for _, u := range users {
			ids[u.Email] = u.ID
		}
	}
	for email, r := range all {
		rep.Total.Up += r.Up
		rep.Total.Down += r.Down
		rep.Users = append(rep.Users, SpeedRow{UserID: ids[email], Email: email, Up: r.Up, Down: r.Down})
	}
	sort.Slice(rep.Users, func(i, j int) bool {
		return rep.Users[i].Up+rep.Users[i].Down > rep.Users[j].Up+rep.Users[j].Down
	})
	if limit > 0 && len(rep.Users) > limit {
		rep.Users = rep.Users[:limit]
	}
	return rep
}

// runSpeed keeps the local speed tracker sampling.
func (a *App) runSpeed(ctx context.Context) {
	if a.Panel == nil && a.Cfg.XrayAPI == "" {
		return
	}
	go a.Speed.Run(ctx, time.Second)
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		marks := a.Speed.DrainActive()
		if len(marks) == 0 {
			continue
		}
		recs := make([]model.ActiveRec, 0, len(marks))
		for _, m := range marks {
			recs = append(recs, model.ActiveRec{U: m.Email, Hour: m.Hour, Mask: m.Mask, Heavy: m.Heavy})
		}
		if err := a.Store.ApplyActive(0, recs); err != nil {
			a.Log.Warn("store traffic minutes", "err", err)
		}
		a.RecordOutbox(model.Batch{Active: recs})
	}
}

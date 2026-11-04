package app

import (
	"context"
	"sort"
	"strings"
	"time"

	"xrayscope/internal/store"
)

const (
	onlineEvery   = 5 * time.Second
	onlineMaxJobs = 100 // users asked per round
)

// runOnline asks Xray itself who is connected. A connection that stays open
// (a video, a download) writes one log line when it opens and then nothing, so
// the log alone lets such users drop off the online list; Xray knows better.
func (a *App) runOnline(ctx context.Context) {
	if a.Panel == nil && a.Cfg.XrayAPI == "" {
		return
	}
	tick := time.NewTicker(onlineEvery)
	defer tick.Stop()
	ids := map[string]int64{}
	var idsAt, pausedUntil time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		if time.Now().Before(pausedUntil) {
			continue
		}
		if time.Since(idsAt) > time.Minute {
			idsAt = time.Now()
			if users, err := a.Store.Users(ctx); err == nil {
				ids = make(map[string]int64, len(users))
				for _, u := range users {
					ids[u.Email] = u.ID
				}
			}
		}
		// users that moved data, then users already shown as online
		seen := map[string]bool{}
		var emails []string
		for e := range a.Speed.Rates() {
			if !seen[e] {
				seen[e] = true
				emails = append(emails, e)
			}
		}
		for _, o := range a.Live.Online(a.Env().OnlineWin) {
			if !seen[o.Email] {
				seen[o.Email] = true
				emails = append(emails, o.Email)
			}
		}
		sort.Strings(emails)
		if len(emails) > onlineMaxJobs {
			emails = emails[:onlineMaxJobs]
		}
		var obs []store.Obs
		for _, e := range emails {
			qctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			list, err := a.SpeedSrc.OnlineIPs(qctx, e)
			cancel()
			if err != nil {
				if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "online") {
					// "statsUserOnline" is off in Xray's policy; try again much later
					pausedUntil = time.Now().Add(10 * time.Minute)
					a.Log.Info("xray does not report online users (policy statsUserOnline is off); using the access log only")
				}
				break
			}
			uid := ids[e]
			if uid == 0 {
				continue
			}
			for ip, ts := range list {
				if ts <= 0 {
					ts = time.Now().Unix()
				}
				obs = append(obs, store.Obs{UserID: uid, Email: e, IP: ip, Minute: ts / 60})
			}
		}
		if len(obs) > 0 {
			a.Live.Observe(obs)
		}
	}
}

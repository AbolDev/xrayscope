package app

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"xrayscope/internal/store"
	"xrayscope/internal/telegram"
)

type alertState struct {
	mu       sync.Mutex
	cooldown map[string]time.Time
	ipSince  map[int64]time.Time // user -> when the IP limit was first exceeded
	nodeDown map[int64]bool
	logStale bool
	notedNew map[string]bool // "uid|ip" and "uid|country" already announced
	batches  map[int64]*idRing
}

func newAlertState() alertState {
	return alertState{
		cooldown: map[string]time.Time{},
		ipSince:  map[int64]time.Time{},
		nodeDown: map[int64]bool{},
		notedNew: map[string]bool{},
	}
}

var tagRe = regexp.MustCompile(`<[^>]*>`)

func stripTags(s string) string {
	s = tagRe.ReplaceAllString(s, "")
	return strings.NewReplacer("&lt;", "<", "&gt;", ">", "&amp;", "&").Replace(s)
}

// notify records an event and sends it to Telegram, at most once per
// cooldown for the same key.
func (a *App) notify(ctx context.Context, kind string, userID, nodeID int64, key string, cooldown time.Duration, html string) {
	st := &a.alerts
	st.mu.Lock()
	if until, ok := st.cooldown[key]; ok && time.Now().Before(until) {
		st.mu.Unlock()
		return
	}
	st.cooldown[key] = time.Now().Add(cooldown)
	if len(st.cooldown) > 20000 {
		for k, v := range st.cooldown {
			if time.Now().After(v) {
				delete(st.cooldown, k)
			}
		}
	}
	st.mu.Unlock()

	_ = a.Store.AddEvent(kind, userID, nodeID, stripTags(html))
	if a.Bot.Enabled() {
		sctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		if err := a.Bot.Send(sctx, html); err != nil {
			a.Log.Warn("telegram send failed", "err", err)
		}
	}
}

// onObs reacts to fresh activity: new IPs and new countries.
func (a *App) onObs(obs []store.Obs) {
	s := a.Settings.Get().Alerts
	if !s.NewIP.Enabled && !s.NewCountry.Enabled {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	now := time.Now().Unix()
	for _, o := range obs {
		if o.Minute*60 < now-15*60 { // history import, not a live event
			continue
		}
		u, err := a.Store.UserByID(ctx, o.UserID)
		if err != nil || u.FirstSeen == 0 || now-u.FirstSeen < 2*3600 {
			continue // brand-new account: nothing to compare with
		}
		ip, err := a.Store.IPByAddr(ctx, o.IP)
		if err != nil {
			continue
		}
		if s.NewIP.Enabled {
			key := fmt.Sprintf("%d|%s", o.UserID, o.IP)
			if h, err := a.Store.UserIPFirstHour(ctx, o.UserID, o.IP); err == nil && h*3600 >= now-90*60 && a.firstTime(key) {
				where := ""
				if ip.Country != "" {
					where = "(" + ip.Country + ")"
				}
				a.notify(ctx, "new_ip", o.UserID, 0, "newip:"+key, time.Hour, a.msg("alert.new_ip", telegram.Escape(o.Email), telegram.Escape(o.IP), where))
			}
		}
		if s.NewCountry.Enabled && ip.Country != "" {
			key := fmt.Sprintf("%d|%s", o.UserID, ip.Country)
			if h, err := a.Store.UserCountryFirstHour(ctx, o.UserID, ip.Country); err == nil && h*3600 >= now-90*60 && a.firstTime(key) {
				a.notify(ctx, "new_country", o.UserID, 0, "newcountry:"+key, time.Hour, a.msg("alert.new_country", telegram.Escape(o.Email), telegram.Escape(ip.Country), telegram.Escape(o.IP)))
			}
		}
	}
}

// firstBatch reports whether a node batch id is new. The last few thousand
// ids per node are remembered.
func (a *App) firstBatch(node int64, id string) bool {
	st := &a.alerts
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.batches == nil {
		st.batches = map[int64]*idRing{}
	}
	r := st.batches[node]
	if r == nil {
		r = &idRing{seen: map[string]bool{}}
		st.batches[node] = r
	}
	return r.add(id)
}

type idRing struct {
	order []string
	seen  map[string]bool
}

func (r *idRing) add(id string) bool {
	if r.seen[id] {
		return false
	}
	r.seen[id] = true
	r.order = append(r.order, id)
	if len(r.order) > 2000 {
		delete(r.seen, r.order[0])
		r.order = r.order[1:]
	}
	return true
}

// firstTime returns true once per key (per process).
func (a *App) firstTime(key string) bool {
	st := &a.alerts
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.notedNew[key] {
		return false
	}
	if len(st.notedNew) > 50000 {
		st.notedNew = map[string]bool{}
	}
	st.notedNew[key] = true
	return true
}

func minutes(n int) time.Duration { return time.Duration(n) * time.Minute }

// evaluateAlerts runs the periodic rules.
func (a *App) evaluateAlerts(ctx context.Context) error {
	cfg := a.Settings.Get()
	al := cfg.Alerts
	now := time.Now()

	users, err := a.Store.Users(ctx)
	if err != nil {
		return err
	}
	byID := make(map[int64]store.User, len(users))
	for _, u := range users {
		byID[u.ID] = u
	}

	// simultaneous IPs
	if al.MaxIPs.Enabled {
		seen := map[int64]bool{}
		for _, o := range a.Live.Online(time.Duration(cfg.OnlineWindowMin) * time.Minute) {
			u := byID[o.UserID]
			limit := al.MaxIPs.Value
			if limit <= 0 {
				limit = u.LimitIP
			}
			if limit <= 0 || o.Devices <= limit {
				continue
			}
			seen[o.UserID] = true
			a.alerts.mu.Lock()
			since, ok := a.alerts.ipSince[o.UserID]
			if !ok {
				a.alerts.ipSince[o.UserID] = now
				since = now
			}
			a.alerts.mu.Unlock()
			if now.Sub(since) < minutes(al.MinOverlapMin) {
				continue
			}
			var ips []string
			for _, c := range o.Conns {
				if !c.Replaced {
					ips = append(ips, "<code>"+telegram.Escape(c.IP)+"</code>")
				}
			}
			a.notify(ctx, "max_ips", o.UserID, 0, fmt.Sprintf("maxips:%d", o.UserID), minutes(al.MaxIPs.Cooldown),
				a.msg("alert.max_ips", telegram.Escape(o.Email), o.Devices, limit, strings.Join(ips, "\n")))
		}
		a.alerts.mu.Lock()
		for id := range a.alerts.ipSince {
			if !seen[id] {
				delete(a.alerts.ipSince, id)
			}
		}
		a.alerts.mu.Unlock()
	}

	// traffic in the current hour
	if al.TrafficSpike.Enabled && al.TrafficSpike.Value > 0 {
		h := now.Unix() / 3600
		pts, err := a.Store.TrafficHourly(ctx, 0, h, h+1)
		if err != nil {
			return err
		}
		limit := int64(al.TrafficSpike.Value) << 20
		for _, p := range pts {
			if p.Up+p.Down >= limit {
				u := byID[p.UserID]
				a.notify(ctx, "traffic_spike", p.UserID, 0, fmt.Sprintf("spike:%d", p.UserID), minutes(al.TrafficSpike.Cooldown),
					a.msg("alert.spike", telegram.Escape(u.Email), humanBytes(p.Up+p.Down)))
			}
		}
	}

	// quota and expiry
	for _, u := range users {
		if !u.Enabled {
			continue
		}
		if al.QuotaWarn.Enabled && u.Quota > 0 {
			used := u.UpTotal + u.DownTotal
			if pct := int(used * 100 / u.Quota); pct >= al.QuotaWarn.Value {
				a.notify(ctx, "quota", u.ID, 0, fmt.Sprintf("quota:%d", u.ID), minutes(al.QuotaWarn.Cooldown),
					a.msg("alert.quota", telegram.Escape(u.Email), pct, humanBytes(used), humanBytes(u.Quota)))
			}
		}
		if al.ExpiryWarn.Enabled && u.Expiry > 0 {
			left := u.Expiry - now.Unix()
			switch {
			case left <= 0:
				a.notify(ctx, "expiry", u.ID, 0, fmt.Sprintf("expired:%d", u.ID), minutes(al.ExpiryWarn.Cooldown),
					a.msg("alert.expired", telegram.Escape(u.Email)))
			case left <= int64(al.ExpiryWarn.Value)*86400:
				a.notify(ctx, "expiry", u.ID, 0, fmt.Sprintf("expiry:%d", u.ID), minutes(al.ExpiryWarn.Cooldown),
					a.msg("alert.expiry", telegram.Escape(u.Email), int((left+86399)/86400)))
			}
		}
	}

	// nodes
	if al.NodeOffline.Enabled {
		nodes, err := a.Store.Nodes(ctx)
		if err != nil {
			return err
		}
		for _, n := range nodes {
			if n.LastSeen == 0 {
				continue
			}
			silent := now.Sub(time.Unix(n.LastSeen, 0))
			a.alerts.mu.Lock()
			down := a.alerts.nodeDown[n.ID]
			a.alerts.mu.Unlock()
			switch {
			case silent > minutes(al.NodeOffline.Value):
				a.alerts.mu.Lock()
				a.alerts.nodeDown[n.ID] = true
				a.alerts.mu.Unlock()
				a.notify(ctx, "node_offline", 0, n.ID, fmt.Sprintf("node:%d", n.ID), minutes(al.NodeOffline.Cooldown),
					a.msg("alert.node_offline", telegram.Escape(n.Name), int(silent.Minutes())))
			case down:
				a.alerts.mu.Lock()
				delete(a.alerts.nodeDown, n.ID)
				a.alerts.mu.Unlock()
				a.notify(ctx, "node_online", 0, n.ID, fmt.Sprintf("nodeup:%d", n.ID), time.Minute, a.msg("alert.node_online", telegram.Escape(n.Name)))
			}
		}
	}

	// log collection stalled
	if al.LogStale.Enabled && a.LogState().State == "ok" {
		ref := a.Started
		if t := a.LastLineAt(); t.After(ref) {
			ref = t
		}
		if silent := now.Sub(ref); silent > minutes(al.LogStale.Value) {
			a.notify(ctx, "log_stale", 0, 0, "logstale", minutes(al.LogStale.Cooldown), a.msg("alert.log_stale", int(silent.Minutes())))
		}
	}
	return nil
}

// digestIfDue sends the daily summary once per day at the configured hour.
func (a *App) digestIfDue(ctx context.Context) error {
	cfg := a.Settings.Get()
	d := cfg.Alerts.DailyDigest
	if !d.Enabled || !a.Bot.Enabled() {
		return nil
	}
	env := a.Env()
	now := time.Now().In(env.Loc)
	if now.Hour() != d.Value {
		return nil
	}
	today := now.Format("2006-01-02")
	if last, _, _ := a.Store.KV("digest:last"); last == today {
		return nil
	}
	if err := a.Store.SetKV("digest:last", today); err != nil {
		return err
	}
	o, err := env.Overview(ctx, rangeLast(24*time.Hour))
	if err != nil {
		return err
	}
	var top []string
	for i, u := range o.TopTraffic {
		if i >= 5 {
			break
		}
		top = append(top, fmt.Sprintf("%d. %s — %s", i+1, telegram.Escape(u.Email), humanBytes(u.Up+u.Down)))
	}
	text := a.msg("digest.title", today) + "\n" +
		a.msg("digest.line", o.Totals.UsersActive, o.Totals.UsersTotal, humanBytes(o.Totals.Up), humanBytes(o.Totals.Down), strings.Join(top, "\n"))
	return a.Bot.Send(ctx, text)
}

// sortedEmails is a small helper for stable output.
func sortedEmails(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

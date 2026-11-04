package report

import (
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"

	"xrayscope/internal/analytics"
	"xrayscope/internal/classify"
	"xrayscope/internal/live"
	"xrayscope/internal/store"
)

// UserReport is the full analysis of one user over a range.
type UserReport struct {
	User        store.User                      `json:"user"`
	Online      *live.Online                    `json:"online,omitempty"`
	Range       Range                           `json:"range"`
	Traffic     TrafficSeries                   `json:"traffic"`
	Activity    ActivitySummary                 `json:"activity"`
	ByCategory  []analytics.TimeStat            `json:"by_category"`
	ByService   []analytics.TimeStat            `json:"by_service"`
	Favorites   map[string][]analytics.TimeStat `json:"favorites"`
	TopSites    []analytics.SiteStat            `json:"top_sites"`
	IPs         []IPInfo                        `json:"ips"`
	Concurrency Concurrency                     `json:"concurrency"`
	Devices     []DeviceSummary                 `json:"devices"`
	Sleep       analytics.SleepSummary          `json:"sleep"`
	SleepWindow int                             `json:"sleep_window_days"`
}

// ActivitySummary describes presence in the range.
type ActivitySummary struct {
	OnlineMinutes      int64                   `json:"online_minutes"`
	InteractiveMinutes int64                   `json:"interactive_minutes"`
	Sessions           int                     `json:"sessions"`
	AvgSessionMin      int64                   `json:"avg_session_min"`
	LongestSessionMin  int64                   `json:"longest_session_min"`
	ActiveDays         int                     `json:"active_days"`
	Daily              []analytics.DayStat     `json:"daily"`
	Hourly             []analytics.HourlyPoint `json:"hourly,omitempty"`
	Heatmap            analytics.Heat          `json:"heatmap"`
	Reqs               int64                   `json:"reqs"`
}

// IPInfo is one client address used by the user.
type IPInfo struct {
	IP       string             `json:"ip"`
	Country  string             `json:"country"`
	ASN      int                `json:"asn"`
	Org      string             `json:"org"`
	First    int64              `json:"first"` // unix seconds
	Last     int64              `json:"last"`
	Minutes  int64              `json:"minutes"`
	Sessions int                `json:"sessions"`
	Online   bool               `json:"online"`
	CDN      string             `json:"cdn,omitempty"` // set when this is a CDN edge, not the client
	Devices  []analytics.Device `json:"devices"`
}

// Concurrency lists periods where several IPs were active together.
type Concurrency struct {
	Limit    int           `json:"limit"` // panel limit, 0 = none
	Max      int           `json:"max"`
	Overlaps []OverlapView `json:"overlaps"`
}

// OverlapView is an analytics.Overlap with resolved addresses.
type OverlapView struct {
	Start int64    `json:"start"` // unix seconds
	End   int64    `json:"end"`
	Peak  int      `json:"peak"`
	IPs   []string `json:"ips"`
}

// DeviceSummary counts how often a guessed device type shows up.
type DeviceSummary struct {
	OS         string  `json:"os"`
	Vendor     string  `json:"vendor,omitempty"`
	Browser    string  `json:"browser,omitempty"`
	IPs        int     `json:"ips"`
	Confidence float64 `json:"confidence"`
}

var favoriteCategories = map[string]bool{
	"ai": true, "social": true, "video": true, "messaging": true, "music": true, "gaming": true,
	"shopping": true, "news": true, "dev": true, "work": true, "education": true, "finance": true,
	"travel": true, "food": true, "sports": true, "health": true, "books": true, "jobs": true,
	"search": true, "email": true, "cloud": true, "tools": true, "adult": true,
}

// ErrNotFound is returned for unknown users.
var ErrNotFound = errors.New("not found")

// User builds the report of one user.
func (e *Env) User(ctx ctxT, userID int64, rg Range) (*UserReport, error) {
	u, err := e.Store.UserByID(ctx, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	r := &UserReport{User: u, Range: rg, Favorites: map[string][]analytics.TimeStat{}, IPs: []IPInfo{}, Devices: []DeviceSummary{}, Concurrency: Concurrency{Overlaps: []OverlapView{}}}
	if e.Live != nil {
		if o, ok := e.Live.OnlineUser(userID, e.OnlineWin); ok {
			r.Online = &o
		}
	}
	rows, err := e.Store.Act(ctx, userID, rg.fromHour(), rg.toHour())
	if err != nil {
		return nil, err
	}
	rows = clipAct(rows, rg)

	if r.Traffic, err = e.trafficSeries(ctx, userID, rg); err != nil {
		return nil, err
	}

	// presence
	pres := analytics.UnionMinutes(rows, analytics.Presence)
	inter := analytics.UnionMinutes(rows, analytics.Interactive)
	a := &r.Activity
	a.OnlineMinutes, a.InteractiveMinutes = int64(len(pres)), int64(len(inter))
	sessions := analytics.Sessions(pres, e.GapMin)
	a.Sessions = len(sessions)
	var total int64
	for _, s := range sessions {
		d := s.End - s.Start + 1
		total += d
		if d > a.LongestSessionMin {
			a.LongestSessionMin = d
		}
	}
	if len(sessions) > 0 {
		a.AvgSessionMin = total / int64(len(sessions))
	}
	a.Daily = analytics.Daily(rows, e.Loc)
	a.ActiveDays = len(a.Daily)
	for _, d := range a.Daily {
		a.Reqs += d.Reqs
	}
	a.Heatmap = analytics.Heatmap(rows, e.Loc, false)
	if rg.To-rg.From <= 7*86400 {
		a.Hourly = analytics.HourlySeries(rows)
	}

	// time spent and favourites
	svcRows, err := e.Store.Svc(ctx, userID, rg.fromHour(), rg.toHour())
	if err != nil {
		return nil, err
	}
	services, err := e.Store.Services(ctx)
	if err != nil {
		return nil, err
	}
	r.ByCategory, r.ByService = analytics.TimeSpent(clipSvc(svcRows, rg), services)
	for _, s := range r.ByService {
		if favoriteCategories[s.Category] && len(r.Favorites[s.Category]) < 3 {
			r.Favorites[s.Category] = append(r.Favorites[s.Category], s)
		}
	}

	// sites
	visits, err := e.Store.Visits(ctx, userID, rg.fromHour(), rg.toHour())
	if err != nil {
		return nil, err
	}
	domIDs := make([]int64, 0, len(visits))
	seen := map[int64]bool{}
	for _, v := range visits {
		if !seen[v.DomainID] {
			seen[v.DomainID] = true
			domIDs = append(domIDs, v.DomainID)
		}
	}
	domains, err := e.Store.Domains(ctx, domIDs)
	if err != nil {
		return nil, err
	}
	r.TopSites = head(analytics.Sites(visits, domains, services, rg.fromMin(), rg.toMin()), 100)

	// IPs and devices
	if err := e.fillIPs(ctx, r, rows, rg); err != nil {
		return nil, err
	}

	// concurrency
	runs := analytics.IPRuns(rows, e.GapMin)
	r.Concurrency.Limit = u.LimitIP
	r.Concurrency.Max = analytics.MaxConcurrent(runs)
	ipAddr := map[int64]string{}
	ipIDs := make([]int64, 0)
	idSeen := map[int64]bool{}
	for _, x := range runs {
		if !idSeen[x.IPID] {
			idSeen[x.IPID] = true
			ipIDs = append(ipIDs, x.IPID)
		}
	}
	ipMap, err := e.Store.IPsByID(ctx, ipIDs)
	if err != nil {
		return nil, err
	}
	for id, ip := range ipMap {
		ipAddr[id] = ip.IP
	}
	for _, o := range analytics.Overlaps(runs, 2) {
		v := OverlapView{Start: o.Start * 60, End: o.End*60 + 59, Peak: o.Peak}
		for _, id := range o.IPs {
			v.IPs = append(v.IPs, ipAddr[id])
		}
		r.Concurrency.Overlaps = append(r.Concurrency.Overlaps, v)
	}
	sort.Slice(r.Concurrency.Overlaps, func(i, j int) bool { return r.Concurrency.Overlaps[i].Start > r.Concurrency.Overlaps[j].Start })
	r.Concurrency.Overlaps = head(r.Concurrency.Overlaps, 200)

	// sleep
	if err := e.fillSleep(ctx, r, rg); err != nil {
		return nil, err
	}
	return r, nil
}

func (e *Env) fillIPs(ctx ctxT, r *UserReport, rows []store.ActRow, rg Range) error {
	byIP := map[int64][]store.ActRow{}
	for _, x := range rows {
		byIP[x.IPID] = append(byIP[x.IPID], x)
	}
	ids := make([]int64, 0, len(byIP))
	for id := range byIP {
		ids = append(ids, id)
	}
	ips, err := e.Store.IPsByID(ctx, ids)
	if err != nil {
		return err
	}
	fp, err := e.Store.FP(ctx, r.User.ID, rg.fromMin()/1440, rg.toMin()/1440+1)
	if err != nil {
		return err
	}
	guess := analytics.GuessDevices(fp)
	online := map[string]bool{}
	if r.Online != nil {
		for _, c := range r.Online.Conns {
			online[c.IP] = true
		}
	}
	type devKey struct{ os, vendor, browser string }
	devIPs := map[devKey]int{}
	devConf := map[devKey]float64{}
	for id, rs := range byIP {
		mins := analytics.UnionMinutes(rs, analytics.Presence)
		if len(mins) == 0 {
			continue
		}
		ip := ips[id]
		devs := guess[id]
		if devs == nil {
			devs = []analytics.Device{}
		}
		info := IPInfo{
			IP: ip.IP, Country: ip.Country, ASN: ip.ASN, Org: ip.Org,
			First: mins[0] * 60, Last: mins[len(mins)-1]*60 + 59,
			Minutes:  int64(len(mins)),
			Sessions: len(analytics.Sessions(mins, e.GapMin)),
			Online:   online[ip.IP],
			CDN:      classify.CDN(ip.IP),
			Devices:  devs,
		}
		for _, d := range info.Devices {
			k := devKey{d.OS, d.Vendor, d.Browser}
			devIPs[k]++
			if d.Confidence > devConf[k] {
				devConf[k] = d.Confidence
			}
		}
		r.IPs = append(r.IPs, info)
	}
	sort.Slice(r.IPs, func(i, j int) bool { return r.IPs[i].Last > r.IPs[j].Last })
	for k, n := range devIPs {
		r.Devices = append(r.Devices, DeviceSummary{OS: k.os, Vendor: k.vendor, Browser: k.browser, IPs: n, Confidence: devConf[k]})
	}
	sort.Slice(r.Devices, func(i, j int) bool {
		if r.Devices[i].IPs != r.Devices[j].IPs {
			return r.Devices[i].IPs > r.Devices[j].IPs
		}
		return r.Devices[i].Confidence > r.Devices[j].Confidence
	})
	return nil
}

// fillSleep analyses at least the last 14 days before the end of the range.
func (e *Env) fillSleep(ctx ctxT, r *UserReport, rg Range) error {
	days := int((rg.To - rg.From) / 86400)
	if days < 14 {
		days = 14
	}
	if days > 90 {
		days = 90
	}
	r.SleepWindow = days
	end := rg.To
	if now := time.Now().Unix(); end > now {
		end = now
	}
	wr := Range{From: end - int64(days)*86400, To: end}
	rows, err := e.Store.Act(ctx, r.User.ID, wr.fromHour(), wr.toHour())
	if err != nil {
		return err
	}
	rows = clipAct(rows, wr)
	pres := analytics.UnionMinutes(rows, analytics.Presence)
	inter := analytics.UnionMinutes(rows, analytics.Interactive)
	toDay := analytics.LocalDay(e.Loc, end/60)
	for toDay > 0 && analytics.LocalNoon(e.Loc, toDay) > end/60 {
		toDay-- // that night is not over yet
	}
	fromDay := analytics.LocalDay(e.Loc, wr.From/60) + 1
	r.Sleep = analytics.AnalyzeSleep(pres, inter, fromDay, toDay, e.Loc, analytics.DefaultSleepOptions())
	return nil
}

// SiteList is a filtered, paged list of sites.
type SiteList struct {
	Total int                  `json:"total"`
	Sites []analytics.SiteStat `json:"sites"`
}

// Sites lists the sites a user visited in a range.
func (e *Env) Sites(ctx ctxT, userID int64, rg Range, q, category string, limit, offset int) (*SiteList, error) {
	visits, err := e.Store.Visits(ctx, userID, rg.fromHour(), rg.toHour())
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(visits))
	seen := map[int64]bool{}
	for _, v := range visits {
		if !seen[v.DomainID] {
			seen[v.DomainID] = true
			ids = append(ids, v.DomainID)
		}
	}
	domains, err := e.Store.Domains(ctx, ids)
	if err != nil {
		return nil, err
	}
	services, err := e.Store.Services(ctx)
	if err != nil {
		return nil, err
	}
	all := analytics.Sites(visits, domains, services, rg.fromMin(), rg.toMin())
	q = strings.ToLower(strings.TrimSpace(q))
	out := all[:0]
	for _, s := range all {
		if category != "" && s.Category != category {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(s.Domain), q) && !strings.Contains(strings.ToLower(s.Service), q) {
			continue
		}
		out = append(out, s)
	}
	res := &SiteList{Total: len(out)}
	if offset > len(out) {
		offset = len(out)
	}
	end := offset + limit
	if limit <= 0 || end > len(out) {
		end = len(out)
	}
	res.Sites = out[offset:end]
	return res, nil
}

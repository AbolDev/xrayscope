package report

import (
	"sort"

	"xrayscope/internal/analytics"
	"xrayscope/internal/live"
	"xrayscope/internal/store"
)

// Overview is the dashboard dataset.
type Overview struct {
	Range      Range                `json:"range"`
	Totals     OverviewTotals       `json:"totals"`
	Traffic    TrafficSeries        `json:"traffic"`
	Activity   []ActivityPoint      `json:"activity"`
	TopTraffic []UserTraffic        `json:"top_traffic"`
	TopActive  []UserActive         `json:"top_active"`
	Categories []analytics.TimeStat `json:"categories"`
	Services   []analytics.TimeStat `json:"services"`
	Heatmap    analytics.Heat       `json:"heatmap"`
	Countries  []CountryStat        `json:"countries"`
	Online     []live.Online        `json:"online"`
	// Prev holds the same headline figures for the period of equal length
	// just before the range, so changes can be shown against it.
	Prev PrevTotals `json:"prev"`
}

// PrevTotals are the headline figures of the preceding period.
type PrevTotals struct {
	UsersActive int   `json:"users_active"`
	Up          int64 `json:"up"`
	Down        int64 `json:"down"`
	Minutes     int64 `json:"minutes"`
}

// OverviewTotals are the headline numbers.
type OverviewTotals struct {
	UsersTotal  int   `json:"users_total"`
	UsersActive int   `json:"users_active"`
	OnlineNow   int   `json:"online_now"`
	IPsNow      int   `json:"ips_now"`
	Up          int64 `json:"up"`
	Down        int64 `json:"down"`
	Reqs        int64 `json:"reqs"`
	Minutes     int64 `json:"minutes"`
}

// ActivityPoint is activity in one bucket.
type ActivityPoint struct {
	T           int64 `json:"t"`
	Users       int   `json:"users"`
	Interactive int64 `json:"interactive"` // user-minutes of interactive traffic
}

// UserTraffic ranks a user by transferred bytes.
type UserTraffic struct {
	UserID int64  `json:"user_id"`
	Email  string `json:"email"`
	Up     int64  `json:"up"`
	Down   int64  `json:"down"`
}

// UserActive ranks a user by active minutes.
type UserActive struct {
	UserID      int64  `json:"user_id"`
	Email       string `json:"email"`
	Minutes     int64  `json:"minutes"`
	Interactive int64  `json:"interactive"`
}

// CountryStat counts users and minutes per client country.
type CountryStat struct {
	Country string `json:"country"`
	Users   int    `json:"users"`
	Minutes int64  `json:"minutes"`
}

// Overview computes the dashboard for a range.
func (e *Env) Overview(ctx ctxT, rg Range) (*Overview, error) {
	o := &Overview{Range: rg, Activity: []ActivityPoint{}, TopTraffic: []UserTraffic{}, TopActive: []UserActive{}, Countries: []CountryStat{}, Online: []live.Online{}}
	users, err := e.Store.Users(ctx)
	if err != nil {
		return nil, err
	}
	email := make(map[int64]string, len(users))
	for _, u := range users {
		email[u.ID] = u.Email
	}
	o.Totals.UsersTotal = len(users)

	rows, err := e.Store.Act(ctx, 0, rg.fromHour(), rg.toHour())
	if err != nil {
		return nil, err
	}
	rows = clipAct(rows, rg)

	// per user / hour union
	type uh struct{ user, hour int64 }
	pres := map[uh]int64{}
	inter := map[uh]int64{}
	reqs := map[int64]int64{}
	ipIDs := map[int64]bool{}
	for _, r := range rows {
		k := uh{r.UserID, r.Hour}
		pres[k] |= r.Mask
		inter[k] |= r.IMask
		reqs[r.UserID] += r.Reqs
		o.Totals.Reqs += r.Reqs
		ipIDs[r.IPID] = true
	}
	userMin := map[int64]int64{}
	userInter := map[int64]int64{}
	for k, m := range pres {
		userMin[k.user] += int64(analytics.PopCount(m))
		userInter[k.user] += int64(analytics.PopCount(inter[k]))
		o.Totals.Minutes += int64(analytics.PopCount(m))
	}
	o.Totals.UsersActive = len(userMin)

	// activity series
	bucket := int64(3600)
	if rg.To-rg.From > 4*86400 {
		bucket = 86400
	}
	usersIn := map[int64]map[int64]bool{}
	interIn := map[int64]int64{}
	for k := range pres {
		var t int64
		if bucket == 3600 {
			t = k.hour * 3600
		} else {
			t = analytics.DayStart(e.Loc, localDayOfHour(e.Loc, k.hour)) * 60
		}
		if usersIn[t] == nil {
			usersIn[t] = map[int64]bool{}
		}
		usersIn[t][k.user] = true
		interIn[t] += int64(analytics.PopCount(inter[k]))
	}
	for t, us := range usersIn {
		o.Activity = append(o.Activity, ActivityPoint{T: t, Users: len(us), Interactive: interIn[t]})
	}
	sort.Slice(o.Activity, func(i, j int) bool { return o.Activity[i].T < o.Activity[j].T })

	// traffic
	if o.Traffic, err = e.trafficSeries(ctx, 0, rg); err != nil {
		return nil, err
	}
	o.Totals.Up, o.Totals.Down = o.Traffic.Up, o.Traffic.Down
	tr, err := e.Store.TrafficHourly(ctx, 0, rg.fromHour(), rg.toHour())
	if err != nil {
		return nil, err
	}
	perUser := map[int64]*UserTraffic{}
	for _, p := range tr {
		u := perUser[p.UserID]
		if u == nil {
			u = &UserTraffic{UserID: p.UserID, Email: email[p.UserID]}
			perUser[p.UserID] = u
		}
		u.Up += p.Up
		u.Down += p.Down
	}
	for _, u := range perUser {
		o.TopTraffic = append(o.TopTraffic, *u)
	}
	sort.Slice(o.TopTraffic, func(i, j int) bool {
		return o.TopTraffic[i].Up+o.TopTraffic[i].Down > o.TopTraffic[j].Up+o.TopTraffic[j].Down
	})
	o.TopTraffic = head(o.TopTraffic, 10)

	for id, m := range userMin {
		o.TopActive = append(o.TopActive, UserActive{UserID: id, Email: email[id], Minutes: m, Interactive: userInter[id]})
	}
	sort.Slice(o.TopActive, func(i, j int) bool { return o.TopActive[i].Minutes > o.TopActive[j].Minutes })
	o.TopActive = head(o.TopActive, 10)

	// categories and services
	svcRows, err := e.Store.Svc(ctx, 0, rg.fromHour(), rg.toHour())
	if err != nil {
		return nil, err
	}
	services, err := e.Store.Services(ctx)
	if err != nil {
		return nil, err
	}
	tact, err := e.Store.TAct(ctx, 0, rg.fromHour(), rg.toHour())
	if err != nil {
		return nil, err
	}
	o.Categories, o.Services = analytics.TimeSpent(clipSvc(svcRows, rg), services, tact)
	o.Services = head(o.Services, 15)

	o.Heatmap = analytics.Heatmap(rows, e.Loc, true)

	// the period before this one, for "vs previous" figures
	span := rg.To - rg.From
	prev := Range{From: rg.From - span, To: rg.From}
	if o.Prev.Up, o.Prev.Down, err = e.TotalTraffic(ctx, 0, prev); err != nil {
		return nil, err
	}
	prevRows, err := e.Store.Act(ctx, 0, prev.fromHour(), prev.toHour())
	if err != nil {
		return nil, err
	}
	prevUsers := map[int64]bool{}
	pm := map[uh]int64{}
	for _, r := range clipAct(prevRows, prev) {
		prevUsers[r.UserID] = true
		pm[uh{r.UserID, r.Hour}] |= r.Mask
	}
	for _, m := range pm {
		o.Prev.Minutes += int64(analytics.PopCount(m))
	}
	o.Prev.UsersActive = len(prevUsers)

	// countries
	ids := make([]int64, 0, len(ipIDs))
	for id := range ipIDs {
		ids = append(ids, id)
	}
	ips, err := e.Store.IPsByID(ctx, ids)
	if err != nil {
		return nil, err
	}
	cUsers := map[string]map[int64]bool{}
	cMin := map[string]int64{}
	for _, r := range rows {
		c := ips[r.IPID].Country
		if c == "" {
			continue
		}
		if cUsers[c] == nil {
			cUsers[c] = map[int64]bool{}
		}
		cUsers[c][r.UserID] = true
		cMin[c] += int64(analytics.PopCount(r.Mask))
	}
	for c, us := range cUsers {
		o.Countries = append(o.Countries, CountryStat{Country: c, Users: len(us), Minutes: cMin[c]})
	}
	sort.Slice(o.Countries, func(i, j int) bool { return o.Countries[i].Minutes > o.Countries[j].Minutes })

	// live
	if e.Live != nil {
		online := e.Live.Online(e.OnlineWin)
		o.Totals.OnlineNow = len(online)
		for _, u := range online {
			o.Totals.IPsNow += len(u.Conns)
		}
		if len(online) > 0 {
			o.Online = head(online, 50)
		}
	}
	return o, nil
}

func head[T any](s []T, n int) []T {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// UserRow is one line of the users table.
type UserRow struct {
	store.User
	Online      bool   `json:"online"`
	OnlineIPs   int    `json:"online_ips"`
	Up          int64  `json:"up"`
	Down        int64  `json:"down"`
	Minutes     int64  `json:"minutes"`
	Interactive int64  `json:"interactive"`
	IPs         int    `json:"ips"`
	MaxIPs      int    `json:"max_ips"`
	TopCategory string `json:"top_category"`
	TopService  string `json:"top_service"`
	// Spark is the presence strip: active minutes per equal slice of the
	// window (24 hourly slices for short ranges, up to 30 daily ones).
	Spark []int `json:"spark"`
}

// sparkBins returns the strip window and slice count for a range.
func sparkBins(rg Range) (start int64, n int) {
	span := rg.To - rg.From
	if span <= 2*86400 {
		return rg.To - 24*3600, 24
	}
	n = int((span + 86399) / 86400)
	if n > 30 {
		n = 30
	}
	return rg.From, n
}

// Users builds the users table for a range.
func (e *Env) Users(ctx ctxT, rg Range) ([]UserRow, error) {
	users, err := e.Store.Users(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := e.Store.Act(ctx, 0, rg.fromHour(), rg.toHour())
	if err != nil {
		return nil, err
	}
	rows = clipAct(rows, rg)
	byUser := map[int64][]store.ActRow{}
	for _, r := range rows {
		byUser[r.UserID] = append(byUser[r.UserID], r)
	}
	tr, err := e.Store.TrafficHourly(ctx, 0, rg.fromHour(), rg.toHour())
	if err != nil {
		return nil, err
	}
	up, down := map[int64]int64{}, map[int64]int64{}
	for _, p := range tr {
		up[p.UserID] += p.Up
		down[p.UserID] += p.Down
	}
	svcRows, err := e.Store.Svc(ctx, 0, rg.fromHour(), rg.toHour())
	if err != nil {
		return nil, err
	}
	services, err := e.Store.Services(ctx)
	if err != nil {
		return nil, err
	}
	tactAll, err := e.Store.TAct(ctx, 0, rg.fromHour(), rg.toHour())
	if err != nil {
		return nil, err
	}
	svcBy := map[int64][]store.SvcRow{}
	for _, r := range clipSvc(svcRows, rg) {
		svcBy[r.UserID] = append(svcBy[r.UserID], r)
	}
	online := map[int64]live.Online{}
	if e.Live != nil {
		for _, o := range e.Live.Online(e.OnlineWin) {
			online[o.UserID] = o
		}
	}
	allIPs := map[int64]bool{}
	for _, r := range rows {
		allIPs[r.IPID] = true
	}
	ipIDs := make([]int64, 0, len(allIPs))
	for id := range allIPs {
		ipIDs = append(ipIDs, id)
	}
	ipInfo, err := e.Store.IPsByID(ctx, ipIDs)
	if err != nil {
		return nil, err
	}
	sameNet := e.sameNetwork(ipInfo)
	sparkStart, sparkN := sparkBins(rg)
	out := make([]UserRow, 0, len(users))
	for _, u := range users {
		r := UserRow{User: u, Up: up[u.ID], Down: down[u.ID]}
		if o, ok := online[u.ID]; ok {
			r.Online, r.OnlineIPs = true, o.Devices
		}
		r.Spark = make([]int, sparkN)
		if rs := byUser[u.ID]; len(rs) > 0 {
			pres := analytics.UnionMinutes(rs, analytics.Presence)
			r.Minutes = int64(len(pres))
			width := float64(rg.To-sparkStart) / float64(sparkN)
			for _, m := range pres {
				if i := int(float64(m*60-sparkStart) / width); i >= 0 && i < sparkN {
					r.Spark[i]++
				}
			}
			r.Interactive = int64(len(analytics.UnionMinutes(rs, analytics.Interactive)))
			ipset := map[int64]bool{}
			for _, x := range rs {
				ipset[x.IPID] = true
			}
			r.IPs = len(ipset)
			r.MaxIPs = analytics.MaxConcurrent(analytics.MergeHops(analytics.IPRuns(rs, e.GapMin), sameNet))
		}
		if sr := svcBy[u.ID]; len(sr) > 0 {
			cats, svcs := analytics.TimeSpent(sr, services, tactAll)
			for _, c := range cats {
				if !isBackgroundCategory(c.Category) {
					r.TopCategory = c.Category
					break
				}
			}
			for _, s := range svcs {
				if s.Category == r.TopCategory {
					r.TopService = s.Service
					break
				}
			}
		}
		out = append(out, r)
	}
	return out, nil
}

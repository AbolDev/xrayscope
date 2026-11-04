package report

import (
	"database/sql"
	"errors"
	"sort"

	"xrayscope/internal/analytics"
	"xrayscope/internal/store"
)

// Timeline is the minute-by-minute view of one local day of one user.
type Timeline struct {
	User     store.User          `json:"user"`
	Date     string              `json:"date"`
	Start    int64               `json:"start"` // unix seconds of local midnight
	Minutes  int                 `json:"minutes"`
	Strip    string              `json:"strip"` // one char per minute: 0 none, 1 background, 2 interactive
	Sessions []SessionView       `json:"sessions"`
	Blocks   []HourBlock         `json:"blocks"`
	Traffic  TrafficSeries       `json:"traffic"`
	Sleep    *analytics.SleepDay `json:"sleep,omitempty"`
}

// SessionView is a session of one IP.
type SessionView struct {
	IP      string `json:"ip"`
	Country string `json:"country"`
	Start   int64  `json:"start"` // unix seconds
	End     int64  `json:"end"`
	Active  int    `json:"active"`
}

// HourBlock lists what was visited during one hour slot.
type HourBlock struct {
	Hour    int64       `json:"hour"` // unix seconds of the slot start
	Minutes int         `json:"minutes"`
	Reqs    int64       `json:"reqs"`
	Sites   []BlockSite `json:"sites"`
}

// BlockSite is one site inside an hour block.
type BlockSite struct {
	Domain   string `json:"domain"`
	Category string `json:"category"`
	Service  string `json:"service"`
	Minutes  int    `json:"minutes"`
	Reqs     int64  `json:"reqs"`
}

// Timeline builds the day view. date is YYYY-MM-DD in the display zone.
func (e *Env) Timeline(ctx ctxT, userID int64, date string) (*Timeline, error) {
	day, err := analytics.ParseDay(date)
	if err != nil {
		return nil, errors.New("invalid date")
	}
	u, err := e.Store.UserByID(ctx, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	startMin := analytics.DayStart(e.Loc, day)
	endMin := analytics.DayStart(e.Loc, day+1)
	rg := Range{From: startMin * 60, To: endMin * 60}
	t := &Timeline{User: u, Date: date, Start: rg.From, Minutes: int(endMin - startMin), Sessions: []SessionView{}, Blocks: []HourBlock{}}

	rows, err := e.Store.Act(ctx, userID, rg.fromHour(), rg.toHour())
	if err != nil {
		return nil, err
	}
	rows = clipAct(rows, rg)
	act, err := e.refineAct(ctx, userID, rg, rows)
	if err != nil {
		return nil, err
	}

	strip := make([]byte, t.Minutes)
	for i := range strip {
		strip[i] = '0'
	}
	for _, m := range analytics.UnionMinutes(act, analytics.Presence) {
		if i := m - startMin; i >= 0 && i < int64(len(strip)) {
			strip[i] = '1'
		}
	}
	for _, m := range analytics.UnionMinutes(act, analytics.Interactive) {
		if i := m - startMin; i >= 0 && i < int64(len(strip)) {
			strip[i] = '2'
		}
	}
	t.Strip = string(strip)

	ids := map[int64]bool{}
	for _, r := range rows {
		ids[r.IPID] = true
	}
	idList := make([]int64, 0, len(ids))
	for id := range ids {
		idList = append(idList, id)
	}
	ips, err := e.Store.IPsByID(ctx, idList)
	if err != nil {
		return nil, err
	}
	for _, s := range analytics.IPSessions(rows, e.GapMin) {
		ip := ips[s.IPID]
		t.Sessions = append(t.Sessions, SessionView{IP: ip.IP, Country: ip.Country, Start: s.Start * 60, End: s.End*60 + 59, Active: s.Active})
	}

	// what was visited, hour by hour
	visits, err := e.Store.Visits(ctx, userID, rg.fromHour(), rg.toHour())
	if err != nil {
		return nil, err
	}
	var hourly []store.VisitRow
	domSet := map[int64]bool{}
	for _, v := range visits {
		if v.Grain != 0 {
			continue
		}
		v.Mask = ClipMask(v.Slot, v.Mask, rg.fromMin(), rg.toMin())
		if v.Mask == 0 {
			continue
		}
		hourly = append(hourly, v)
		domSet[v.DomainID] = true
	}
	domIDs := make([]int64, 0, len(domSet))
	for id := range domSet {
		domIDs = append(domIDs, id)
	}
	domains, err := e.Store.Domains(ctx, domIDs)
	if err != nil {
		return nil, err
	}
	services, err := e.Store.Services(ctx)
	if err != nil {
		return nil, err
	}
	blocks := map[int64]*HourBlock{}
	hourMask := map[int64]int64{}
	for _, v := range hourly {
		b := blocks[v.Slot]
		if b == nil {
			b = &HourBlock{Hour: v.Slot * 3600}
			blocks[v.Slot] = b
		}
		d := domains[v.DomainID]
		b.Reqs += v.Reqs
		hourMask[v.Slot] |= v.Mask
		b.Sites = append(b.Sites, BlockSite{Domain: d.Name, Category: d.Category, Service: services[d.ServiceID].Name, Minutes: analytics.PopCount(v.Mask), Reqs: v.Reqs})
	}
	for slot, b := range blocks {
		b.Minutes = analytics.PopCount(hourMask[slot])
		sort.Slice(b.Sites, func(i, j int) bool { return b.Sites[i].Reqs > b.Sites[j].Reqs })
		b.Sites = head(b.Sites, 12)
		t.Blocks = append(t.Blocks, *b)
	}
	sort.Slice(t.Blocks, func(i, j int) bool { return t.Blocks[i].Hour < t.Blocks[j].Hour })

	if t.Traffic, err = e.trafficSeries(ctx, userID, rg); err != nil {
		return nil, err
	}
	return t, nil
}

// IPDetail answers "who used this address".
type IPDetail struct {
	IP      store.IP     `json:"ip"`
	Range   Range        `json:"range"`
	Users   []IPUserView `json:"users"`
	Present bool         `json:"present"`
}

// IPUserView is one user seen on an address.
type IPUserView struct {
	UserID   int64  `json:"user_id"`
	Email    string `json:"email"`
	Minutes  int64  `json:"minutes"`
	Sessions int    `json:"sessions"`
	First    int64  `json:"first"`
	Last     int64  `json:"last"`
}

// IP lists the users that connected from an address.
func (e *Env) IP(ctx ctxT, addr string, rg Range) (*IPDetail, error) {
	ip, err := e.Store.IPByAddr(ctx, addr)
	if errors.Is(err, sql.ErrNoRows) {
		return &IPDetail{Range: rg, IP: store.IP{IP: addr}}, nil
	}
	if err != nil {
		return nil, err
	}
	rows, err := e.Store.ActForIP(ctx, ip.ID, rg.fromHour(), rg.toHour())
	if err != nil {
		return nil, err
	}
	rows = clipAct(rows, rg)
	by := map[int64][]store.ActRow{}
	for _, r := range rows {
		by[r.UserID] = append(by[r.UserID], r)
	}
	users, err := e.Store.Users(ctx)
	if err != nil {
		return nil, err
	}
	email := map[int64]string{}
	for _, u := range users {
		email[u.ID] = u.Email
	}
	d := &IPDetail{IP: ip, Range: rg, Present: true}
	for id, rs := range by {
		mins := analytics.UnionMinutes(rs, analytics.Presence)
		if len(mins) == 0 {
			continue
		}
		d.Users = append(d.Users, IPUserView{
			UserID: id, Email: email[id], Minutes: int64(len(mins)),
			Sessions: len(analytics.Sessions(mins, e.GapMin)),
			First:    mins[0] * 60, Last: mins[len(mins)-1]*60 + 59,
		})
	}
	sort.Slice(d.Users, func(i, j int) bool { return d.Users[i].Last > d.Users[j].Last })
	return d, nil
}

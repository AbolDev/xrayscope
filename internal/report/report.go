// Package report assembles the datasets shown in the web panel and used by
// the Telegram bot.
package report

import (
	"sort"
	"time"

	"xrayscope/internal/analytics"
	"xrayscope/internal/live"
	"xrayscope/internal/store"
)

// Env carries what report functions need.
type Env struct {
	Store     *store.Store
	Loc       *time.Location
	GapMin    int64
	OnlineWin time.Duration
	Live      *live.Tracker
}

// Range is a half-open time range in unix seconds.
type Range struct {
	From int64 `json:"from"`
	To   int64 `json:"to"`
}

func (r Range) fromMin() int64  { return r.From / 60 }
func (r Range) toMin() int64    { return (r.To + 59) / 60 }
func (r Range) fromHour() int64 { return r.From / 3600 }
func (r Range) toHour() int64   { return (r.To + 3599) / 3600 }

// Days is the length of the range in days (at least 1).
func (r Range) Days() float64 {
	d := float64(r.To-r.From) / 86400
	if d < 1 {
		return 1
	}
	return d
}

// ClipMask clears the minute bits of an hour row that fall outside
// [fromMin, toMin).
func ClipMask(hour, mask, fromMin, toMin int64) int64 {
	base := hour * 60
	if fromMin > base {
		if fromMin >= base+60 {
			return 0
		}
		mask &^= (int64(1) << uint(fromMin-base)) - 1
	}
	if toMin < base+60 {
		if toMin <= base {
			return 0
		}
		mask &= (int64(1) << uint(toMin-base)) - 1
	}
	return mask
}

// clipAct applies ClipMask to presence rows and drops empty ones.
func clipAct(rows []store.ActRow, rg Range) []store.ActRow {
	out := rows[:0]
	for _, r := range rows {
		r.Mask = ClipMask(r.Hour, r.Mask, rg.fromMin(), rg.toMin())
		r.IMask = ClipMask(r.Hour, r.IMask, rg.fromMin(), rg.toMin())
		if r.Mask == 0 {
			continue
		}
		out = append(out, r)
	}
	return out
}

func clipSvc(rows []store.SvcRow, rg Range) []store.SvcRow {
	out := rows[:0]
	for _, r := range rows {
		r.Mask = ClipMask(r.Hour, r.Mask, rg.fromMin(), rg.toMin())
		if r.Mask == 0 {
			continue
		}
		out = append(out, r)
	}
	return out
}

// TrafficPoint is one bucket of a traffic series.
type TrafficPoint struct {
	T    int64 `json:"t"` // bucket start, unix seconds
	Up   int64 `json:"up"`
	Down int64 `json:"down"`
}

// TrafficSeries is upload/download over time.
type TrafficSeries struct {
	Bucket int64          `json:"bucket"` // seconds per point
	Points []TrafficPoint `json:"points"`
	Up     int64          `json:"up"`
	Down   int64          `json:"down"`
}

// trafficSeries builds a series for one user (0 = everyone). It uses
// 5-minute data for short ranges when available, hourly data otherwise, and
// local days for long ranges.
func (e *Env) trafficSeries(ctx ctxT, userID int64, rg Range) (TrafficSeries, error) {
	var ts TrafficSeries
	span := rg.To - rg.From
	use5 := false
	if span <= 2*86400 {
		oldest, err := e.Store.OldestTraffic5(ctx)
		if err != nil {
			return ts, err
		}
		use5 = oldest > 0 && oldest*300 <= rg.From+300
	}
	acc := map[int64]*TrafficPoint{}
	add := func(t, up, down int64) {
		p := acc[t]
		if p == nil {
			p = &TrafficPoint{T: t}
			acc[t] = p
		}
		p.Up += up
		p.Down += down
		ts.Up += up
		ts.Down += down
	}
	switch {
	case use5:
		ts.Bucket = 300
		pts, err := e.Store.Traffic5(ctx, userID, rg.From/300, (rg.To+299)/300)
		if err != nil {
			return ts, err
		}
		for _, p := range pts {
			add(p.Slot*300, p.Up, p.Down)
		}
	case span <= 4*86400:
		ts.Bucket = 3600
		pts, err := e.Store.TrafficHourly(ctx, userID, rg.fromHour(), rg.toHour())
		if err != nil {
			return ts, err
		}
		for _, p := range pts {
			add(p.Slot*3600, p.Up, p.Down)
		}
	default:
		ts.Bucket = 86400
		pts, err := e.Store.TrafficHourly(ctx, userID, rg.fromHour(), rg.toHour())
		if err != nil {
			return ts, err
		}
		for _, p := range pts {
			d := localDayOfHour(e.Loc, p.Slot)
			add(analytics.DayStart(e.Loc, d)*60, p.Up, p.Down)
		}
	}
	ts.Points = make([]TrafficPoint, 0, len(acc))
	for _, p := range acc {
		ts.Points = append(ts.Points, *p)
	}
	sort.Slice(ts.Points, func(i, j int) bool { return ts.Points[i].T < ts.Points[j].T })
	return ts, nil
}

func localDayOfHour(loc *time.Location, hour int64) int64 {
	t := time.Unix(hour*3600, 0).In(loc)
	y, m, d := t.Date()
	return time.Date(y, m, d, 12, 0, 0, 0, time.UTC).Unix() / 86400
}

// TotalTraffic sums traffic of one user (0 = everyone) in a range.
func (e *Env) TotalTraffic(ctx ctxT, userID int64, rg Range) (up, down int64, err error) {
	pts, err := e.Store.TrafficHourly(ctx, userID, rg.fromHour(), rg.toHour())
	if err != nil {
		return 0, 0, err
	}
	for _, p := range pts {
		up += p.Up
		down += p.Down
	}
	return up, down, nil
}

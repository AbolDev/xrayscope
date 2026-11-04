package analytics

import (
	"sort"
	"sync/atomic"
	"time"

	"xrayscope/internal/store"
)

// Heat is minutes of activity by weekday (0 = Sunday) and hour of day.
type Heat [7][24]int64

// Heatmap counts active minutes per local weekday and hour. When interactive
// is true only interactive minutes are counted.
func Heatmap(rows []store.ActRow, loc *time.Location, interactive bool) Heat {
	var h Heat
	pick := Presence
	if interactive {
		pick = Interactive
	}
	for hour, mask := range unionByHour(rows, pick) {
		sec := hour * 3600
		off := zoneOffset(loc, sec)
		// split the UTC hour across local hours (zones with :30 offsets)
		hourMinutes(hour, mask, func(m int64) {
			local := m*60 + off
			day := floorDiv(local, 86400)
			wd := mod(day+4, 7) // 1970-01-01 was a Thursday
			hod := mod(floorDiv(local, 3600), 24)
			h[wd][hod]++
		})
	}
	return h
}

// unionByHour ORs the masks of all rows of the same hour.
func unionByHour(rows []store.ActRow, pick func(store.ActRow) int64) map[int64]int64 {
	out := map[int64]int64{}
	for _, r := range rows {
		out[r.Hour] |= pick(r)
	}
	return out
}

// DayStat is the presence of one local day.
type DayStat struct {
	Date        string `json:"date"`
	Online      int64  `json:"online"`      // minutes with any traffic
	Interactive int64  `json:"interactive"` // minutes with interactive traffic
	Reqs        int64  `json:"reqs"`
	IPs         int    `json:"ips"` // distinct client IPs
}

// Daily folds presence rows of one user into per-day statistics.
func Daily(rows []store.ActRow, loc *time.Location) []DayStat {
	type acc struct {
		on, inter map[int64]bool
		reqs      int64
		ips       map[int64]bool
	}
	days := map[int64]*acc{}
	get := func(d int64) *acc {
		a := days[d]
		if a == nil {
			a = &acc{on: map[int64]bool{}, inter: map[int64]bool{}, ips: map[int64]bool{}}
			days[d] = a
		}
		return a
	}
	for _, r := range rows {
		hourMinutes(r.Hour, r.Mask, func(m int64) {
			a := get(localDay(loc, m))
			a.on[m] = true
		})
		hourMinutes(r.Hour, r.IMask, func(m int64) {
			a := get(localDay(loc, m))
			a.inter[m] = true
		})
		a := get(localDay(loc, r.Hour*60))
		a.reqs += r.Reqs
		a.ips[r.IPID] = true
	}
	keys := make([]int64, 0, len(days))
	for d := range days {
		keys = append(keys, d)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	out := make([]DayStat, 0, len(keys))
	for _, d := range keys {
		a := days[d]
		out = append(out, DayStat{Date: DayString(d), Online: int64(len(a.on)), Interactive: int64(len(a.inter)), Reqs: a.reqs, IPs: len(a.ips)})
	}
	return out
}

// HourlyPoint is activity within one absolute hour.
type HourlyPoint struct {
	Hour        int64 `json:"hour"`
	Online      int   `json:"online"`
	Interactive int   `json:"interactive"`
}

// HourlySeries returns minutes of activity per absolute hour for one user.
func HourlySeries(rows []store.ActRow) []HourlyPoint {
	pres := unionByHour(rows, Presence)
	inter := unionByHour(rows, Interactive)
	hours := make([]int64, 0, len(pres))
	for h := range pres {
		hours = append(hours, h)
	}
	sort.Slice(hours, func(i, j int) bool { return hours[i] < hours[j] })
	out := make([]HourlyPoint, 0, len(hours))
	for _, h := range hours {
		out = append(out, HourlyPoint{Hour: h, Online: PopCount(pres[h]), Interactive: PopCount(inter[h])})
	}
	return out
}

// TimeStat is time spent on a category or service.
type TimeStat struct {
	Category string `json:"category"`
	Service  string `json:"service,omitempty"`
	Minutes  int64  `json:"minutes"`
	Reqs     int64  `json:"reqs"`
}

// BridgeMinutes is the default for how long a pause may be before time on a
// site stops counting. The access log only records when a connection opens,
// so a video or a page being read leaves silent minutes in between that
// still belong to the visit.
const BridgeMinutes = 5

// Tuning holds the adjustable thresholds of the time calculations.
type Tuning struct {
	BridgeMin    int  // short pauses always bridged (0 = off)
	TrafficMax   int  // longest pause bridged while data flowed (0 = off)
	TrafficShare int  // percent of such a pause that must carry traffic
	CountPolling bool // count machine-regular polling as time spent
}

// DefaultTuning returns the built-in thresholds.
func DefaultTuning() Tuning {
	return Tuning{BridgeMin: BridgeMinutes, TrafficMax: TrafficBridgeMax, TrafficShare: trafficShare}
}

var tuning atomic.Pointer[Tuning]

// SetTuning changes the thresholds used by every later calculation.
func SetTuning(t Tuning) { tuning.Store(&t) }

func currentTuning() Tuning {
	if t := tuning.Load(); t != nil {
		return *t
	}
	return DefaultTuning()
}

// BridgeGaps fills the quiet minutes between two active minutes when fewer
// than gap of them lie in between. masks maps an absolute hour to its 60-bit
// minute mask and is changed in place.
func BridgeGaps(masks map[int64]int64, gap int) {
	if gap <= 0 || len(masks) == 0 {
		return
	}
	var mins []int64
	hours := make([]int64, 0, len(masks))
	for h := range masks {
		hours = append(hours, h)
	}
	sort.Slice(hours, func(i, j int) bool { return hours[i] < hours[j] })
	for _, h := range hours {
		hourMinutes(h, masks[h], func(m int64) { mins = append(mins, m) })
	}
	for i := 1; i < len(mins); i++ {
		a, b := mins[i-1], mins[i]
		if b-a > 1 && b-a-1 <= int64(gap) {
			for m := a + 1; m < b; m++ {
				masks[m/60] |= 1 << uint(m%60)
			}
		}
	}
}

// Traffic-based bridging: a longer pause between two visits of a site still
// counts as time on it while the user's connection kept moving data.
const (
	// TrafficBridgeMax is the default longest pause bridged this way.
	TrafficBridgeMax = 30
	// trafficShare is the default share of the pause that must have traffic.
	trafficShare = 70
)

// BridgeTraffic fills pauses of up to the tuned maximum between two active
// minutes when at least the tuned share of the pause carried traffic (tr:
// hour -> minute mask). It is meant to run after BridgeGaps.
func BridgeTraffic(masks map[int64]int64, tr map[int64]int64) {
	tu := currentTuning()
	maxGap, share := int64(tu.TrafficMax), int64(tu.TrafficShare)
	if len(masks) == 0 || len(tr) == 0 || maxGap <= 0 {
		return
	}
	hours := make([]int64, 0, len(masks))
	for h := range masks {
		hours = append(hours, h)
	}
	sort.Slice(hours, func(i, j int) bool { return hours[i] < hours[j] })
	var mins []int64
	for _, h := range hours {
		hourMinutes(h, masks[h], func(m int64) { mins = append(mins, m) })
	}
	active := func(m int64) bool { return tr[m/60]&(1<<uint(m%60)) != 0 }
	for i := 1; i < len(mins); i++ {
		a, b := mins[i-1], mins[i]
		gap := b - a - 1
		if gap <= 0 || gap > maxGap {
			continue
		}
		var n int64
		for m := a + 1; m < b; m++ {
			if active(m) {
				n++
			}
		}
		if n*100 >= gap*share {
			for m := a + 1; m < b; m++ {
				masks[m/60] |= 1 << uint(m%60)
			}
		}
	}
}

// TimeSpent aggregates service time. Category totals use the union of
// minutes across services so parallel use is not double counted. Per-user
// results are summed when rows from several users are given.
//
// tact (user -> hour -> minute mask, may be nil) holds the minutes with real
// traffic; it lets pauses longer than BridgeMinutes count while data flowed.
func TimeSpent(rows []store.SvcRow, services map[int64]store.Service, tact map[int64]map[int64]int64) (byCategory, byService []TimeStat) {
	byCategory, byService = []TimeStat{}, []TimeStat{}
	type ck struct {
		user int64
		cat  string
		hour int64
	}
	catMask := map[ck]int64{}
	type sk struct {
		cat, name string
	}
	svcMin := map[sk]int64{}
	svcReqs := map[sk]int64{}
	catReqs := map[string]int64{}
	type uk struct{ user, svc int64 }
	groups := map[uk]map[int64]int64{}
	for _, r := range rows {
		k := uk{r.UserID, r.ServiceID}
		if groups[k] == nil {
			groups[k] = map[int64]int64{}
		}
		groups[k][r.Hour] |= r.Mask
	}
	tu := currentTuning()
	for k, m := range groups {
		if !tu.CountPolling && periodicMasks(m) {
			// automatic polling: requests are kept, time is not
			for h := range m {
				m[h] = 0
			}
			continue
		}
		BridgeGaps(m, tu.BridgeMin)
		BridgeTraffic(m, tact[k.user])
	}
	for _, r := range rows {
		sv, ok := services[r.ServiceID]
		if !ok {
			continue
		}
		// the first row of a (user, service, hour) carries the bridged minutes
		k := uk{r.UserID, r.ServiceID}
		mask := groups[k][r.Hour]
		delete(groups[k], r.Hour)
		catMask[ck{r.UserID, sv.Category, r.Hour}] |= mask
		svcMin[sk{sv.Category, sv.Name}] += int64(PopCount(mask))
		svcReqs[sk{sv.Category, sv.Name}] += r.Reqs
		catReqs[sv.Category] += r.Reqs
	}
	catMin := map[string]int64{}
	for k, m := range catMask {
		catMin[k.cat] += int64(PopCount(m))
	}
	for c, m := range catMin {
		byCategory = append(byCategory, TimeStat{Category: c, Minutes: m, Reqs: catReqs[c]})
	}
	for k, m := range svcMin {
		byService = append(byService, TimeStat{Category: k.cat, Service: k.name, Minutes: m, Reqs: svcReqs[k]})
	}
	sort.Slice(byCategory, func(i, j int) bool { return byCategory[i].Minutes > byCategory[j].Minutes })
	sort.Slice(byService, func(i, j int) bool { return byService[i].Minutes > byService[j].Minutes })
	return byCategory, byService
}

// SiteStat is one site visited by a user.
type SiteStat struct {
	Domain   string `json:"domain"`
	Category string `json:"category"`
	Service  string `json:"service"`
	Minutes  int64  `json:"minutes"`
	Reqs     int64  `json:"reqs"`
	First    int64  `json:"first"` // unix minute
	Last     int64  `json:"last"`
	// Auto marks a site contacted at a machine-regular rhythm (an app
	// polling in the background); its minutes are not counted.
	Auto bool `json:"auto,omitempty"`
	// Bytes is the estimated data moved for this site: each stretch of the
	// user's measured traffic shared among the sites active in it.
	Bytes int64 `json:"bytes"`
}

// Sites aggregates visit rows per site. Daily (compacted) rows contribute
// hours*60 as an upper bound for minutes.
//
// tr (hour -> minute mask, may be nil) holds the user's minutes with real
// traffic, as for TimeSpent.
func Sites(rows []store.VisitRow, domains map[int64]store.Domain, services map[int64]store.Service, from, to int64, tr map[int64]int64) []SiteStat {
	acc := map[int64]*SiteStat{}
	// bridge quiet minutes inside one visit, per site
	bridged := map[int64]map[int64]int64{}
	for _, r := range rows {
		if r.Grain != 0 {
			continue
		}
		if bridged[r.DomainID] == nil {
			bridged[r.DomainID] = map[int64]int64{}
		}
		bridged[r.DomainID][r.Slot] |= r.Mask
	}
	periodic := map[int64]bool{}
	tu := currentTuning()
	for dom, m := range bridged {
		if !tu.CountPolling && periodicMasks(m) {
			periodic[dom] = true
			continue
		}
		BridgeGaps(m, tu.BridgeMin)
		BridgeTraffic(m, tr)
	}
	for _, r := range rows {
		d, ok := domains[r.DomainID]
		if !ok {
			continue
		}
		var first, last, mins int64
		if r.Grain == 0 {
			if r.Slot < from/60 || r.Slot >= (to+59)/60 {
				continue
			}
			mask, fresh := bridged[r.DomainID][r.Slot]
			if !fresh {
				// another user's row of this site and hour already carried the minutes
				if s := acc[r.DomainID]; s != nil {
					s.Reqs += r.Reqs
				}
				continue
			}
			delete(bridged[r.DomainID], r.Slot)
			mins = int64(PopCount(mask))
			first = r.Slot*60 + int64(lowBit(mask))
			last = r.Slot*60 + int64(highBit(mask))
			if periodic[r.DomainID] {
				mins = 0 // automatic polling is not time spent
			}
		} else {
			mins = int64(PopCount(r.Mask)) * 60
			first = r.Slot*60 + int64(lowBit(r.Mask))*60
			last = r.Slot*60 + int64(highBit(r.Mask))*60 + 59
		}
		s := acc[r.DomainID]
		if s == nil {
			sv := services[d.ServiceID]
			s = &SiteStat{Domain: d.Name, Category: d.Category, Service: sv.Name, First: first, Last: last, Auto: periodic[r.DomainID]}
			acc[r.DomainID] = s
		}
		s.Minutes += mins
		s.Reqs += r.Reqs
		if first < s.First {
			s.First = first
		}
		if last > s.Last {
			s.Last = last
		}
	}
	out := make([]SiteStat, 0, len(acc))
	for _, s := range acc {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Reqs != out[j].Reqs {
			return out[i].Reqs > out[j].Reqs
		}
		return out[i].Domain < out[j].Domain
	})
	return out
}

func lowBit(m int64) int {
	for i := 0; i < 64; i++ {
		if m&(1<<uint(i)) != 0 {
			return i
		}
	}
	return 0
}

func highBit(m int64) int {
	for i := 63; i >= 0; i-- {
		if m&(1<<uint(i)) != 0 {
			return i
		}
	}
	return 0
}

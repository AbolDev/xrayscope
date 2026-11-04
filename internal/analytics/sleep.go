package analytics

import (
	"fmt"
	"math"
	"sort"
	"time"
)

// SleepOptions tunes the sleep detector.
type SleepOptions struct {
	MinSleep  int64 // shortest inactivity gap counted as sleep, minutes
	Window    int64 // half-width of the density window, minutes
	Density   int   // interactive minutes needed inside the window to count as awake
	NightFrom int64 // start of the expected night, minutes after local midnight
	NightTo   int64 // end of the expected night
}

// DefaultSleepOptions are sensible defaults for a typical sleeping pattern.
func DefaultSleepOptions() SleepOptions {
	return SleepOptions{MinSleep: 180, Window: 10, Density: 3, NightFrom: 0, NightTo: 8 * 60}
}

// SleepDay is the estimated sleep of one night.
type SleepDay struct {
	Date       string `json:"date"`  // morning date (local)
	Start      int64  `json:"start"` // unix minutes
	End        int64  `json:"end"`
	Minutes    int64  `json:"minutes"`
	Present    int64  `json:"present"` // minutes with background-only traffic inside the window
	Awakenings int    `json:"awakenings"`
	// Kind: "connected" = VPN/device kept sending background traffic while
	// the user was inactive; "offline" = no traffic at all.
	Kind     string `json:"kind"`
	Atypical bool   `json:"atypical"`
}

// SleepSummary aggregates the nightly estimates.
type SleepSummary struct {
	Days         []SleepDay `json:"days"`
	DaysAnalyzed int        `json:"days_analyzed"`
	DaysTotal    int        `json:"days_total"`
	TypicalStart string     `json:"typical_start"` // HH:MM local
	TypicalEnd   string     `json:"typical_end"`
	AvgMinutes   int64      `json:"avg_minutes"`
	StdMinutes   int64      `json:"std_minutes"` // bedtime variability
	Confidence   float64    `json:"confidence"`
	// ConnectedShare is the share of analysed nights during which the
	// connection stayed alive with background traffic only.
	ConnectedShare float64 `json:"connected_share"`
}

// AnalyzeSleep estimates nightly sleep from interactive activity. pres and
// inter are sorted unique unix minutes of any traffic and of interactive
// traffic. Nights are evaluated for each morning in [fromDay, toDay] (local
// day indexes).
func AnalyzeSleep(pres, inter []int64, fromDay, toDay int64, loc *time.Location, opt SleepOptions) SleepSummary {
	sum := SleepSummary{Days: []SleepDay{}}
	var starts, durs []int64
	connected := 0
	for d := fromDay; d <= toDay; d++ {
		sum.DaysTotal++
		cycleStart := localNoon(loc, d-1)
		cycleEnd := localNoon(loc, d)
		in := between(inter, cycleStart, cycleEnd)
		if len(in) < 30 {
			continue // too little interactive use to say anything about this day
		}
		conf := confirmed(in, opt.Window, opt.Density)
		if len(conf) < 20 {
			continue
		}
		midnight := DayStart(loc, d)
		nightA, nightB := midnight+opt.NightFrom, midnight+opt.NightTo

		var best *SleepDay
		var bestOverlap, bestLen int64
		for i := 0; i+1 < len(conf); i++ {
			s, e := conf[i]+1, conf[i+1]-1
			n := e - s + 1
			if n < opt.MinSleep {
				continue
			}
			ov := minI(e, nightB) - maxI(s, nightA)
			if ov < 0 {
				ov = 0
			}
			if best == nil || ov > bestOverlap || (ov == bestOverlap && n > bestLen) {
				bestOverlap, bestLen = ov, n
				best = &SleepDay{Start: s, End: e, Minutes: n}
			}
		}
		if best == nil {
			continue
		}
		best.Date = DayString(d)
		best.Atypical = bestOverlap < 60
		best.Present = int64(len(between(pres, best.Start, best.End+1)))
		best.Awakenings = bursts(between(inter, best.Start, best.End+1), 10)
		if best.Present >= 4 {
			best.Kind = "connected"
		} else {
			best.Kind = "offline"
		}
		sum.Days = append(sum.Days, *best)
		if !best.Atypical {
			starts = append(starts, best.Start-cycleStart)
			durs = append(durs, best.Minutes)
			if best.Kind == "connected" {
				connected++
			}
		}
	}
	sum.DaysAnalyzed = len(starts)
	if len(starts) == 0 {
		return sum
	}
	// bedtime / wake time are expressed relative to local noon so the night
	// does not wrap around midnight.
	sort.Slice(starts, func(i, j int) bool { return starts[i] < starts[j] })
	sort.Slice(durs, func(i, j int) bool { return durs[i] < durs[j] })
	medStart := median(starts)
	medDur := median(durs)
	sum.TypicalStart = clock(mod(720+medStart, 1440))
	sum.TypicalEnd = clock(mod(720+medStart+medDur, 1440))
	var total int64
	for _, v := range durs {
		total += v
	}
	sum.AvgMinutes = total / int64(len(durs))
	sum.StdMinutes = stddev(starts)
	regularity := 1 - float64(sum.StdMinutes)/150
	if regularity < 0 {
		regularity = 0
	}
	coverage := float64(len(starts)) / 7
	if coverage > 1 {
		coverage = 1
	}
	sum.Confidence = round2(coverage * (0.4 + 0.6*regularity))
	sum.ConnectedShare = round2(float64(connected) / float64(len(starts)))
	return sum
}

// StreamAwake returns the minutes of sustained transfer that were started by
// the user: a stretch of heavy minutes (gaps of up to two minutes allowed)
// counts when a video, music, social or game service was opened shortly
// before or during it. Someone watching a film in bed is awake although the
// player makes no new connections; a phone installing updates at night is not.
// heavy and media are sorted unique unix minutes.
func StreamAwake(heavy, media []int64) []int64 {
	var out []int64
	for i := 0; i < len(heavy); {
		j := i
		for j+1 < len(heavy) && heavy[j+1]-heavy[j] <= 3 {
			j++
		}
		start, end := heavy[i], heavy[j]
		if len(between(media, start-10, end+1)) > 0 {
			for m := start; m <= end; m++ {
				out = append(out, m)
			}
		}
		i = j + 1
	}
	return out
}

// localNoon returns the unix minute of 12:00 local time on a day index.
func localNoon(loc *time.Location, day int64) int64 {
	t := time.Unix(day*86400, 0).UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 12, 0, 0, 0, loc).Unix() / 60
}

// between returns the sub-slice of sorted xs within [a, b).
func between(xs []int64, a, b int64) []int64 {
	i := sort.Search(len(xs), func(i int) bool { return xs[i] >= a })
	j := sort.Search(len(xs), func(i int) bool { return xs[i] >= b })
	return xs[i:j]
}

// confirmed keeps the minutes that have at least density minutes (itself
// included) within +-window; isolated blips such as a push-triggered request
// do not count as being awake.
func confirmed(xs []int64, window int64, density int) []int64 {
	out := make([]int64, 0, len(xs))
	lo, hi := 0, 0
	for _, x := range xs {
		for xs[lo] < x-window {
			lo++
		}
		for hi < len(xs) && xs[hi] <= x+window {
			hi++
		}
		if hi-lo >= density {
			out = append(out, x)
		}
	}
	return out
}

// bursts counts groups of minutes separated by at least sep minutes.
func bursts(xs []int64, sep int64) int {
	n := 0
	for i, x := range xs {
		if i == 0 || x-xs[i-1] >= sep {
			n++
		}
	}
	return n
}

func median(xs []int64) int64 {
	if len(xs) == 0 {
		return 0
	}
	return xs[len(xs)/2]
}

func stddev(xs []int64) int64 {
	if len(xs) < 2 {
		return 0
	}
	var mean float64
	for _, x := range xs {
		mean += float64(x)
	}
	mean /= float64(len(xs))
	var v float64
	for _, x := range xs {
		v += (float64(x) - mean) * (float64(x) - mean)
	}
	v /= float64(len(xs))
	return int64(math.Sqrt(v) + 0.5)
}

func clock(minuteOfDay int64) string {
	return fmt.Sprintf("%02d:%02d", minuteOfDay/60, minuteOfDay%60)
}

func round2(f float64) float64 { return float64(int64(f*100+0.5)) / 100 }

func minI(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func maxI(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// Package analytics derives behaviour from the stored presence masks:
// sessions, concurrent devices, sleep patterns, time spent per category and
// device guesses.
package analytics

import (
	"math/bits"
	"sort"
	"time"

	"xrayscope/internal/store"
)

// PopCount counts set bits of a minute mask.
func PopCount(mask int64) int { return bits.OnesCount64(uint64(mask)) }

// hourMinutes calls fn with the unix minute of every set bit of mask.
func hourMinutes(hour, mask int64, fn func(m int64)) {
	for mask != 0 {
		b := bits.TrailingZeros64(uint64(mask))
		fn(hour*60 + int64(b))
		mask &= mask - 1
	}
}

// UnionMinutes returns the sorted unique unix minutes selected by pick over
// all rows (typically the union of several IPs of one user).
func UnionMinutes(rows []store.ActRow, pick func(store.ActRow) int64) []int64 {
	merged := map[int64]int64{}
	for _, r := range rows {
		merged[r.Hour] |= pick(r)
	}
	hours := make([]int64, 0, len(merged))
	for h := range merged {
		hours = append(hours, h)
	}
	sort.Slice(hours, func(i, j int) bool { return hours[i] < hours[j] })
	out := make([]int64, 0, len(hours)*8)
	for _, h := range hours {
		hourMinutes(h, merged[h], func(m int64) { out = append(out, m) })
	}
	return out
}

// MaskMinutes lists the minutes of an hour->mask map that fall in [from, to).
func MaskMinutes(masks map[int64]int64, from, to int64) []int64 {
	hours := make([]int64, 0, len(masks))
	for h := range masks {
		hours = append(hours, h)
	}
	sort.Slice(hours, func(i, j int) bool { return hours[i] < hours[j] })
	var out []int64
	for _, h := range hours {
		hourMinutes(h, masks[h], func(m int64) {
			if m >= from && m < to {
				out = append(out, m)
			}
		})
	}
	return out
}

// MergeMinutes returns the sorted union of two sorted minute lists.
func MergeMinutes(a, b []int64) []int64 {
	out := make([]int64, 0, len(a)+len(b))
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		var m int64
		switch {
		case j >= len(b) || (i < len(a) && a[i] < b[j]):
			m = a[i]
			i++
		case i >= len(a) || b[j] < a[i]:
			m = b[j]
			j++
		default:
			m = a[i]
			i++
			j++
		}
		if len(out) == 0 || out[len(out)-1] != m {
			out = append(out, m)
		}
	}
	return out
}

// Presence picks the "any traffic" mask.
func Presence(r store.ActRow) int64 { return r.Mask }

// Interactive picks the "interactive traffic" mask.
func Interactive(r store.ActRow) int64 { return r.IMask }

// zoneOffset returns the UTC offset in seconds at the given unix second.
func zoneOffset(loc *time.Location, sec int64) int64 {
	_, off := time.Unix(sec, 0).In(loc).Zone()
	return int64(off)
}

// localDay returns the local calendar day index (days since 1970-01-01) of a
// unix minute.
func localDay(loc *time.Location, minute int64) int64 {
	sec := minute * 60
	return floorDiv(sec+zoneOffset(loc, sec), 86400)
}

// LocalDay is the exported form of localDay.
func LocalDay(loc *time.Location, minute int64) int64 { return localDay(loc, minute) }

// LocalNoon returns the unix minute of 12:00 local time on a day index.
func LocalNoon(loc *time.Location, day int64) int64 { return localNoon(loc, day) }

// ParseDay converts YYYY-MM-DD into a day index.
func ParseDay(s string) (int64, error) {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return 0, err
	}
	return t.Unix() / 86400, nil
}

// DayString formats a day index as YYYY-MM-DD.
func DayString(day int64) string {
	return time.Unix(day*86400, 0).UTC().Format("2006-01-02")
}

// DayStart returns the unix minute at which the local day begins.
func DayStart(loc *time.Location, day int64) int64 {
	t := time.Unix(day*86400, 0).UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc).Unix() / 60
}

func floorDiv(a, b int64) int64 {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

func mod(a, b int64) int64 {
	m := a % b
	if m < 0 {
		m += b
	}
	return m
}

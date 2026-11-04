package analytics

import (
	"math"
	"sort"
)

// Machine-like polling: an app that asks a server for news every few minutes,
// around the clock, leaves short pulses at a near-constant interval. People
// do not; their visits come in irregular stretches. Such a service is
// background traffic for that user even though its category says otherwise.
const (
	periodicMinPulses = 12   // pulses needed before judging
	periodicMaxPulse  = 2    // minutes; a pulse is short
	periodicMaxCV     = 0.25 // spread of the intervals relative to their mean
	periodicMinGap    = 2    // minutes between pulses, at least
	periodicMaxGap    = 180  // and at most
	periodicMinCover  = 0.75 // share of the hours in their span that pulses reach
	periodicMinSpan   = 20   // hours the pulses must span

	// ContextDays is how much history before a short range is looked at to
	// judge polling; callers load rows from this far back and pass the range
	// to count in.
	ContextDays = 3
)

// clipMask keeps the minutes of an hour mask inside [from, to) unix minutes.
func clipMask(hour, mask, from, to int64) int64 {
	base := hour * 60
	if from > base {
		if from >= base+60 {
			return 0
		}
		mask &^= (int64(1) << uint(from-base)) - 1
	}
	if to < base+60 {
		if to <= base {
			return 0
		}
		mask &= (int64(1) << uint(to-base)) - 1
	}
	return mask
}

// IsPeriodic reports whether sorted unique minutes look like automatic
// polling rather than use.
func IsPeriodic(mins []int64) bool {
	if len(mins) < periodicMinPulses {
		return false
	}
	// group consecutive minutes into pulses
	var starts []int64
	long := 0
	for i := 0; i < len(mins); {
		j := i
		for j+1 < len(mins) && mins[j+1]-mins[j] <= 1 {
			j++
		}
		if mins[j]-mins[i]+1 > periodicMaxPulse {
			long++
		}
		starts = append(starts, mins[i])
		i = j + 1
	}
	if len(starts) < periodicMinPulses || long*10 > len(starts) {
		return false // too few pulses, or real stretches of use among them
	}
	// a machine keeps going around the clock; a person, however regular,
	// stops for the night: the pulses must reach most hours of their span
	hours := map[int64]bool{}
	for _, s := range starts {
		hours[s/60] = true
	}
	span := starts[len(starts)-1]/60 - starts[0]/60 + 1
	if span < periodicMinSpan || float64(len(hours)) < periodicMinCover*float64(span) {
		return false // under a day nobody can tell a person's day from a machine
	}
	gaps := make([]float64, 0, len(starts)-1)
	for i := 1; i < len(starts); i++ {
		gaps = append(gaps, float64(starts[i]-starts[i-1]))
	}
	// ignore the longest tenth: the device was off or out of coverage
	sort.Float64s(gaps)
	gaps = gaps[:len(gaps)-len(gaps)/10]
	var mean float64
	for _, g := range gaps {
		mean += g
	}
	mean /= float64(len(gaps))
	if mean < periodicMinGap || mean > periodicMaxGap {
		return false
	}
	var v float64
	for _, g := range gaps {
		v += (g - mean) * (g - mean)
	}
	return math.Sqrt(v/float64(len(gaps)))/mean <= periodicMaxCV
}

// periodicMasks reports whether the minutes of an hour->mask map are
// periodic.
func periodicMasks(masks map[int64]int64) bool {
	return IsPeriodic(MaskMinutes(masks, math.MinInt64, math.MaxInt64))
}

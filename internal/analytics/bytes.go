package analytics

import "xrayscope/internal/store"

// EstimateSiteBytes shares a user's measured traffic among the sites they
// used. Xray counts bytes per user, not per destination, so each five-minute
// slot of traffic is split between the sites active in that slot, in
// proportion to their requests. Hours whose five-minute detail has expired
// are split the same way per hour. Traffic in a slot without any site
// activity stays unattributed.
//
// rows are the user's visit rows; t5 and th the user's traffic in five-minute
// and hourly slots. The result maps a domain id to its estimated bytes.
func EstimateSiteBytes(rows []store.VisitRow, t5, th []store.TrafficPoint) map[int64]int64 {
	type sd struct{ slot, dom int64 }
	w5 := map[sd]float64{}      // five-minute slot weights per site
	wH := map[sd]float64{}      // hourly weights per site
	sum5 := map[int64]float64{} // total weight per five-minute slot
	sumH := map[int64]float64{} // total weight per hour
	for _, r := range rows {
		if r.Grain != 0 || r.Mask == 0 {
			continue
		}
		per := float64(r.Reqs) / float64(PopCount(r.Mask))
		hourMinutes(r.Slot, r.Mask, func(m int64) {
			k := sd{m / 5, r.DomainID}
			w5[k] += per
			sum5[m/5] += per
		})
		wH[sd{r.Slot, r.DomainID}] += float64(r.Reqs)
		sumH[r.Slot] += float64(r.Reqs)
	}

	bytes5 := map[int64]int64{}
	covered := map[int64]bool{} // hours with five-minute detail
	for _, p := range t5 {
		bytes5[p.Slot] += p.Up + p.Down
		covered[p.Slot*5/60] = true
	}
	bytesH := map[int64]int64{}
	for _, p := range th {
		if !covered[p.Slot] {
			bytesH[p.Slot] += p.Up + p.Down
		}
	}

	out := map[int64]int64{}
	for k, w := range w5 {
		if b := bytes5[k.slot]; b > 0 {
			out[k.dom] += int64(float64(b) * w / sum5[k.slot])
		}
	}
	for k, w := range wH {
		if b := bytesH[k.slot]; b > 0 {
			out[k.dom] += int64(float64(b) * w / sumH[k.slot])
		}
	}
	return out
}

package report

import (
	"net/netip"

	"xrayscope/internal/analytics"
	"xrayscope/internal/store"
)

// sameNetwork is SameNetwork unless address changes are to be kept apart.
func (e *Env) sameNetwork(ips map[int64]store.IP) func(a, b int64) bool {
	if e.SeparateIPChanges {
		return func(a, b int64) bool { return false }
	}
	return SameNetwork(ips)
}

// SameNetwork returns a test for "these two addresses belong to one
// operator": the same autonomous system when the ASN is known, otherwise the
// same /16 (IPv4) or /32 (IPv6) block.
func SameNetwork(ips map[int64]store.IP) func(a, b int64) bool {
	return func(a, b int64) bool {
		x, okx := ips[a]
		y, oky := ips[b]
		if !okx || !oky {
			return false
		}
		return SameNetworkAddr(x.IP, x.ASN, y.IP, y.ASN)
	}
}

// addSiteBytes fills the estimated data of each site.
func (e *Env) addSiteBytes(ctx ctxT, userID int64, rg Range, visits []store.VisitRow, domains map[int64]store.Domain, sites []analytics.SiteStat) error {
	t5, err := e.Store.Traffic5(ctx, userID, rg.From/300, (rg.To+299)/300)
	if err != nil {
		return err
	}
	th, err := e.Store.TrafficHourly(ctx, userID, rg.fromHour(), rg.toHour())
	if err != nil {
		return err
	}
	est := analytics.EstimateSiteBytes(visits, t5, th)
	byName := make(map[string]int64, len(est))
	for id, b := range est {
		if d, ok := domains[id]; ok {
			byName[d.Name] += b
		}
	}
	for i := range sites {
		sites[i].Bytes = byName[sites[i].Domain]
	}
	return nil
}

// SameNetworkAddr compares two addresses with their (possibly unknown) ASNs.
func SameNetworkAddr(a string, asnA int, b string, asnB int) bool {
	if asnA != 0 && asnB != 0 {
		return asnA == asnB
	}
	x, err1 := netip.ParseAddr(a)
	y, err2 := netip.ParseAddr(b)
	if err1 != nil || err2 != nil || x.Is4() != y.Is4() {
		return false
	}
	bits := 16
	if !x.Is4() {
		bits = 32
	}
	p, err := x.Prefix(bits)
	return err == nil && p.Contains(y)
}

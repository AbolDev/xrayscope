package classify

import (
	_ "embed"
	"net/netip"
	"sort"
	"strings"
)

// cdn_ranges.txt holds the edge ranges the CDNs publish themselves
// (regenerate with `go run ./tools/cdnranges`). When users connect through
// such a CDN the proxy sees the CDN address instead of the client's.
//
//go:embed cdn_ranges.txt
var cdnRanges string

type cdnIndex struct {
	lens []int // distinct prefix lengths, longest first; IPv6 lengths are offset by 1000
	m    map[netip.Prefix]string
}

var cdnTable = buildCDN(cdnRanges)

func buildCDN(src string) *cdnIndex {
	idx := &cdnIndex{m: map[netip.Prefix]string{}}
	seen := map[int]bool{}
	for _, line := range strings.Split(src, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' {
			continue
		}
		name, cidr, ok := strings.Cut(line, "|")
		if !ok {
			continue
		}
		p, err := netip.ParsePrefix(cidr)
		if err != nil {
			continue
		}
		p = p.Masked()
		idx.m[p] = name
		l := p.Bits()
		if p.Addr().Is6() {
			l += 1000
		}
		if !seen[l] {
			seen[l] = true
			idx.lens = append(idx.lens, l)
		}
	}
	sort.Sort(sort.Reverse(sort.IntSlice(idx.lens)))
	return idx
}

// CDN returns the CDN name when ip belongs to a known CDN edge network.
func CDN(ip string) string {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return ""
	}
	a = a.Unmap()
	for _, l := range cdnTable.lens {
		bits := l
		if l >= 1000 {
			if !a.Is6() {
				continue
			}
			bits = l - 1000
		} else if !a.Is4() {
			continue
		}
		p, err := a.Prefix(bits)
		if err != nil {
			continue
		}
		if name, ok := cdnTable.m[p]; ok {
			return name
		}
	}
	return ""
}

package analytics

import (
	"sort"
	"strings"

	"xrayscope/internal/store"
)

// Device is a guess about the device behind a client IP.
type Device struct {
	OS         string  `json:"os"`
	Vendor     string  `json:"vendor,omitempty"`
	Browser    string  `json:"browser,omitempty"`
	Confidence float64 `json:"confidence"`
}

// osFamily maps fingerprint values to a family and a vendor/variant label.
func osFamily(value string) (family, variant string) {
	switch value {
	case "Windows", "Linux", "macOS", "iOS":
		return value, ""
	case "Android":
		return "Android", ""
	case "Samsung Android":
		return "Android", "Samsung"
	case "Xiaomi Android":
		return "Android", "Xiaomi"
	case "Huawei":
		return "Android", "Huawei"
	case "Oppo/Vivo Android":
		return "Android", "Oppo/Vivo"
	case "Apple":
		return "Apple", ""
	}
	return value, ""
}

// GuessDevices turns fingerprint hits into device guesses per IP. An IP with
// hits from several operating system families is reported as several
// devices (typically a shared NAT or hotspot).
func GuessDevices(rows []store.FPRow) map[int64][]Device {
	type score struct {
		fam      map[string]int64
		variants map[string]map[string]int64 // family -> variant -> score
		apple    map[string]int64            // iOS/macOS/Apple
		browser  map[string]int64
	}
	byIP := map[int64]*score{}
	for _, r := range rows {
		sc := byIP[r.IPID]
		if sc == nil {
			sc = &score{fam: map[string]int64{}, variants: map[string]map[string]int64{}, apple: map[string]int64{}, browser: map[string]int64{}}
			byIP[r.IPID] = sc
		}
		switch r.Kind {
		case "os":
			fam, variant := osFamily(r.Value)
			switch fam {
			case "iOS", "macOS", "Apple":
				sc.apple[fam] += r.N
				sc.fam["Apple"] += r.N
			default:
				sc.fam[fam] += r.N
			}
			if variant != "" {
				if sc.variants[fam] == nil {
					sc.variants[fam] = map[string]int64{}
				}
				sc.variants[fam][variant] += r.N
			}
		case "browser":
			sc.browser[r.Value] += r.N
		}
	}
	out := map[int64][]Device{}
	for ip, sc := range byIP {
		fams := sortedKeys(sc.fam)
		var topN int64
		if len(fams) > 0 {
			topN = sc.fam[fams[0]]
		}
		bestBrowser, browserShare := "", 0.0
		if bs := sortedKeys(sc.browser); len(bs) > 0 {
			var tot int64
			for _, v := range sc.browser {
				tot += v
			}
			bestBrowser = bs[0]
			browserShare = float64(sc.browser[bs[0]]) / float64(tot)
		}
		var devs []Device
		var famTotal int64
		for _, n := range sc.fam {
			famTotal += n
		}
		for _, fam := range fams {
			n := sc.fam[fam]
			if len(devs) > 0 && float64(n) < 0.2*float64(topN) {
				continue
			}
			d := Device{OS: fam}
			if fam == "Apple" {
				switch {
				case sc.apple["iOS"] > sc.apple["macOS"]:
					d.OS = "iOS"
				case sc.apple["macOS"] > sc.apple["iOS"]:
					d.OS = "macOS"
				default:
					d.OS = "iOS / macOS"
				}
			}
			if v := sc.variants[fam]; len(v) > 0 {
				vs := sortedKeys(v)
				if float64(v[vs[0]]) >= 0.25*float64(n) {
					d.Vendor = vs[0]
				}
			}
			d.Confidence = round2(float64(n) / float64(famTotal) * saturate(n))
			if len(devs) == 0 && bestBrowser != "" && browserCompatible(d.OS, bestBrowser) {
				d.Browser = bestBrowser
				if d.Confidence > 0 {
					d.Confidence = round2((d.Confidence*2 + browserShare*saturate(sc.browser[bestBrowser])) / 3)
				}
			}
			devs = append(devs, d)
		}
		if len(devs) == 0 && bestBrowser != "" {
			devs = append(devs, Device{OS: "Unknown", Browser: bestBrowser, Confidence: round2(browserShare * saturate(sc.browser[bestBrowser]) * 0.5)})
		}
		out[ip] = devs
	}
	return out
}

// browserCompatible rejects impossible combinations such as Safari on Windows.
func browserCompatible(os, browser string) bool {
	switch browser {
	case "Safari":
		return strings.Contains(os, "iOS") || strings.Contains(os, "macOS") || os == "Apple"
	}
	return true
}

// saturate grows from 0 to 1 as evidence accumulates.
func saturate(n int64) float64 {
	if n >= 30 {
		return 1
	}
	return float64(n) / 30
}

func sortedKeys(m map[string]int64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if m[keys[i]] != m[keys[j]] {
			return m[keys[i]] > m[keys[j]]
		}
		return keys[i] < keys[j]
	})
	return keys
}

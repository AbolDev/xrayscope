// Package classify maps destination hosts to site, category and service, and
// extracts device fingerprint hints (operating system, browser).
package classify

import (
	_ "embed"
	"net/netip"
	"strconv"
	"strings"

	"golang.org/x/net/publicsuffix"
)

//go:embed rules.txt
var builtin string

// Category describes one traffic category.
type Category struct {
	ID    string `json:"id"`
	Color string `json:"color"`
	// Background categories are machine-generated traffic (updates, CDN,
	// telemetry, push). They do not count as interactive use.
	Background bool `json:"background"`
}

// Categories is the ordered list of known categories.
var Categories = []Category{
	{"ai", "#8b5cf6", false},
	{"social", "#ec4899", false},
	{"messaging", "#06b6d4", false},
	{"video", "#ef4444", false},
	{"music", "#10b981", false},
	{"gaming", "#f59e0b", false},
	{"shopping", "#f97316", false},
	{"news", "#64748b", false},
	{"dev", "#3b82f6", false},
	{"search", "#14b8a6", false},
	{"email", "#6366f1", false},
	{"work", "#0ea5e9", false},
	{"cloud", "#22c55e", false},
	{"finance", "#eab308", false},
	{"education", "#a855f7", false},
	{"tools", "#84cc16", false},
	{"vpn", "#78716c", false},
	{"other", "#94a3b8", false},
	{"ip", "#a1a1aa", false},
	{"ads", "#fb7185", true},
	{"telemetry", "#fdba74", true},
	{"push", "#fcd34d", true},
	{"connectivity", "#bef264", true},
	{"dns", "#7dd3fc", true},
	{"update", "#c4b5fd", true},
	{"system", "#cbd5e1", true},
	{"cdn", "#d4d4d8", true},
}

var bgCategory = func() map[string]bool {
	m := map[string]bool{}
	for _, c := range Categories {
		m[c.ID] = c.Background
	}
	return m
}()

// IsBackground reports whether the category is background traffic.
func IsBackground(category string) bool { return bgCategory[category] }

// IsKnownCategory reports whether the category id exists.
func IsKnownCategory(category string) bool {
	_, ok := bgCategory[category]
	return ok
}

// Rule is a custom suffix rule supplied by the administrator.
type Rule struct {
	Domain   string `json:"domain"`
	Category string `json:"category"`
	Service  string `json:"service"`
}

// Info is the classification result of a host.
type Info struct {
	Site       string // grouping key: matched rule domain or registrable domain
	Category   string
	Service    string
	Background bool
}

// Hint is a device fingerprint clue produced by contacting a host.
type Hint struct {
	Kind   string // "os" or "browser"
	Value  string
	Weight int
}

type ruleVal struct{ category, service string }

type netRule struct {
	prefix   netip.Prefix
	category string
	service  string
}

// Classifier is safe for concurrent use after construction.
type Classifier struct {
	rules map[string]ruleVal
	hints map[string][]Hint
	nets  []netRule
}

// New builds a classifier from the built-in rules plus custom overrides.
func New(custom []Rule) *Classifier {
	c := &Classifier{rules: map[string]ruleVal{}, hints: map[string][]Hint{}}
	c.load(builtin)
	for _, r := range custom {
		d := normalize(r.Domain)
		if d == "" || !IsKnownCategory(r.Category) {
			continue
		}
		c.rules[d] = ruleVal{r.Category, r.Service}
	}
	return c
}

func normalize(d string) string {
	return strings.Trim(strings.ToLower(strings.TrimSpace(d)), ".")
}

func (c *Classifier) load(src string) {
	for _, line := range strings.Split(src, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' {
			continue
		}
		parts := strings.Split(line, "|")
		switch parts[0] {
		case "cidr":
			if len(parts) != 4 {
				continue
			}
			for _, s := range strings.Split(parts[3], ",") {
				if p, err := netip.ParsePrefix(strings.TrimSpace(s)); err == nil {
					c.nets = append(c.nets, netRule{p, parts[1], parts[2]})
				}
			}
		case "hint":
			if len(parts) != 5 {
				continue
			}
			w, _ := strconv.Atoi(parts[3])
			for _, d := range strings.Split(parts[4], ",") {
				d = normalize(d)
				if d != "" {
					c.hints[d] = append(c.hints[d], Hint{Kind: parts[1], Value: parts[2], Weight: w})
				}
			}
		default:
			if len(parts) != 3 {
				continue
			}
			for _, d := range strings.Split(parts[2], ",") {
				d = normalize(d)
				if d != "" {
					c.rules[d] = ruleVal{parts[0], parts[1]}
				}
			}
		}
	}
}

// Classify returns the site key, category and service for a host or IP.
func (c *Classifier) Classify(host string) Info {
	if addr, err := netip.ParseAddr(host); err == nil {
		addr = addr.Unmap()
		for _, n := range c.nets {
			if n.prefix.Contains(addr) {
				return Info{Site: n.service, Category: n.category, Service: n.service, Background: IsBackground(n.category)}
			}
		}
		return Info{Site: addr.String(), Category: "ip", Background: false}
	}
	for h := host; h != ""; {
		if r, ok := c.rules[h]; ok {
			return Info{Site: h, Category: r.category, Service: r.service, Background: IsBackground(r.category)}
		}
		i := strings.IndexByte(h, '.')
		if i < 0 {
			break
		}
		h = h[i+1:]
	}
	site, err := publicsuffix.EffectiveTLDPlusOne(host)
	if err != nil || site == "" {
		site = host
	}
	return Info{Site: site, Category: "other"}
}

// Hints returns the fingerprint clues for the longest matching suffix of host.
func (c *Classifier) Hints(host string) []Hint {
	for h := host; h != ""; {
		if hs, ok := c.hints[h]; ok {
			return hs
		}
		i := strings.IndexByte(h, '.')
		if i < 0 {
			break
		}
		h = h[i+1:]
	}
	return nil
}

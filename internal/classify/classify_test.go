package classify

import "testing"

func TestClassify(t *testing.T) {
	c := New(nil)
	cases := []struct {
		host, site, category, service string
		bg                            bool
	}{
		{"chatgpt.com", "chatgpt.com", "ai", "ChatGPT", false},
		{"cdn.oaistatic.com", "oaistatic.com", "ai", "ChatGPT", false},
		{"gemini.google.com", "gemini.google.com", "ai", "Gemini", false},
		{"www.google.com", "google.com", "search", "Google", false},
		{"mail.google.com", "mail.google.com", "email", "Gmail", false},
		{"rr3---sn-abc.googlevideo.com", "googlevideo.com", "video", "YouTube", false},
		{"i.instagram.com", "instagram.com", "social", "Instagram", false},
		{"connectivitycheck.gstatic.com", "connectivitycheck.gstatic.com", "connectivity", "Connectivity check", true},
		{"fonts.gstatic.com", "fonts.gstatic.com", "cdn", "Open CDNs", true},
		{"149.154.167.51", "Telegram", "messaging", "Telegram", false},
		{"8.8.8.8", "Public DNS", "dns", "Public DNS", true},
		{"93.184.216.34", "93.184.216.34", "ip", "", false},
		{"blog.example.co.uk", "example.co.uk", "other", "", false},
		{"localhost", "localhost", "other", "", false},
	}
	for _, tc := range cases {
		got := c.Classify(tc.host)
		if got.Site != tc.site || got.Category != tc.category || got.Service != tc.service || got.Background != tc.bg {
			t.Errorf("%s: got %+v", tc.host, got)
		}
	}
}

func TestCustomRuleOverrides(t *testing.T) {
	c := New([]Rule{{Domain: "example.org", Category: "work", Service: "Intranet"}, {Domain: "bad.org", Category: "nonsense"}})
	if got := c.Classify("a.example.org"); got.Category != "work" || got.Service != "Intranet" {
		t.Fatalf("custom rule not applied: %+v", got)
	}
	if got := c.Classify("bad.org"); got.Category != "other" {
		t.Fatalf("unknown category must be ignored: %+v", got)
	}
}

func TestCDN(t *testing.T) {
	for ip, want := range map[string]string{
		"172.67.10.5": "Cloudflare", "104.21.3.9": "Cloudflare", "2606:4700::6810:1": "Cloudflare",
		"151.101.1.1": "Fastly", "37.32.17.2": "ArvanCloud", "94.101.182.13": "ArvanCloud",
		"8.8.8.8": "", "93.184.216.34": "", "not an ip": "",
	} {
		if got := CDN(ip); got != want {
			t.Errorf("CDN(%s) = %q, want %q", ip, got, want)
		}
	}
}

func TestHints(t *testing.T) {
	c := New(nil)
	hs := c.Hints("settings-win.data.microsoft.com")
	if len(hs) == 0 || hs[0].Value != "Windows" {
		t.Fatalf("windows hint missing: %+v", hs)
	}
	if hs := c.Hints("www.example.com"); hs != nil {
		t.Fatalf("unexpected hints: %+v", hs)
	}
}

func TestRulesParse(t *testing.T) {
	c := New(nil)
	if len(c.rules) < 400 || len(c.hints) < 80 || len(c.nets) < 10 {
		t.Fatalf("rules not fully loaded: %d rules, %d hints, %d nets", len(c.rules), len(c.hints), len(c.nets))
	}
	for d, r := range c.rules {
		if !IsKnownCategory(r.category) {
			t.Errorf("rule %s uses unknown category %q", d, r.category)
		}
	}
}

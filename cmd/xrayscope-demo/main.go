// Command xrayscope-demo fills a data directory with realistic synthetic
// history so the panel can be explored (and screenshotted) without a real VPN
// server. It is a development tool and is not part of the release.
package main

import (
	"flag"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"time"
	_ "time/tzdata"

	"xrayscope/internal/auth"
	"xrayscope/internal/model"
	"xrayscope/internal/store"
)

type device struct {
	os     string // android, ios, windows, macos
	ip     func(day int, r *rand.Rand) string
	laptop bool
	node   bool
}

type persona struct {
	email   string
	limit   int
	wake    float64 // hour of day
	bed     float64
	night   bool // phone keeps the VPN on while asleep
	likes   map[string]float64
	devices []device
	quota   int64
	expiry  int
	extra   bool // lets a friend use the account from other IPs in the evening
}

var pools = map[string][]string{
	"ai":        {"chatgpt.com", "cdn.oaistatic.com", "claude.ai", "gemini.google.com", "www.perplexity.ai", "chat.deepseek.com"},
	"social":    {"i.instagram.com", "scontent.cdninstagram.com", "x.com", "abs.twimg.com", "www.tiktok.com", "v16m.tiktokcdn.com", "www.reddit.com", "www.linkedin.com"},
	"messaging": {"149.154.167.51", "web.telegram.org", "web.whatsapp.com", "discord.com", "149.154.175.53"},
	"video":     {"www.youtube.com", "rr3---sn-x1.googlevideo.com", "www.netflix.com", "www.twitch.tv", "www.aparat.com", "i.ytimg.com"},
	"music":     {"open.spotify.com", "api.spotify.com", "soundcloud.com"},
	"gaming":    {"store.steampowered.com", "steamcommunity.com", "www.epicgames.com", "pubgmobile.com"},
	"dev":       {"github.com", "stackoverflow.com", "registry.npmjs.org", "pypi.org", "api.github.com"},
	"news":      {"www.bbc.com", "www.dw.com", "www.iranintl.com", "www.theguardian.com"},
	"search":    {"www.google.com", "duckduckgo.com", "www.bing.com"},
	"shopping":  {"www.amazon.com", "www.digikala.com", "www.aliexpress.com", "www.etsy.com"},
	"finance":   {"www.binance.com", "www.tradingview.com", "www.coinmarketcap.com"},
	"work":      {"docs.google.com", "drive.google.com", "app.slack.com", "zoom.us", "www.notion.so"},
}

var background = []string{"connectivitycheck.gstatic.com", "mtalk.google.com", "play.googleapis.com", "fonts.gstatic.com", "www.googletagmanager.com", "app-measurement.com", "graph.facebook.com"}
var osNoise = map[string][]string{
	"android": {"android.clients.google.com", "connectivitycheck.gstatic.com", "play-fe.googleapis.com", "clients4.google.com"},
	"ios":     {"captive.apple.com", "api.smoot.apple.com", "weather-data.apple.com", "gspe35-ssl.ls.apple.com"},
	"windows": {"settings-win.data.microsoft.com", "www.msftconnecttest.com", "v10.events.data.microsoft.com", "update.googleapis.com", "edgedl.me.gvt1.com"},
	"macos":   {"swscan.apple.com", "valid.apple.com", "detectportal.firefox.com", "push.apple.com"},
}

func main() {
	data := flag.String("data", "demo-data", "data directory to fill")
	days := flag.Int("days", 21, "days of history")
	n := flag.Int("users", 14, "number of users")
	seed := flag.Int64("seed", 7, "random seed")
	tzName := flag.String("tz", "Asia/Tehran", "time zone the users live in")
	flag.Parse()

	loc, err := time.LoadLocation(*tzName)
	if err != nil {
		fatal(err)
	}
	if err := os.MkdirAll(*data, 0o750); err != nil {
		fatal(err)
	}
	st, err := store.Open(filepath.Join(*data, "xrayscope.db"), nil)
	if err != nil {
		fatal(err)
	}
	defer st.Close()
	if err := setAdmin(st); err != nil {
		fatal(err)
	}
	node, _, err := st.CreateNode("de-1")
	if err != nil {
		fatal(err)
	}

	r := rand.New(rand.NewSource(*seed))
	people := personas(*n, r)
	now := time.Now()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -*days)

	var local, remote []model.Rec
	var traffic []model.TrafficRec
	totals := map[string][2]int64{}
	for _, p := range people {
		for d := 0; d <= *days; d++ {
			day := start.AddDate(0, 0, d)
			recs, tr := simulate(p, day, d, now, r)
			for _, rec := range recs {
				if rec.node {
					remote = append(remote, rec.Rec)
				} else {
					local = append(local, rec.Rec)
				}
			}
			for _, t := range tr {
				traffic = append(traffic, t)
				c := totals[p.email]
				totals[p.email] = [2]int64{c[0] + t.Up, c[1] + t.Down}
			}
		}
	}
	fmt.Printf("generated %d local and %d node records, %d traffic points\n", len(local), len(remote), len(traffic))
	for _, batch := range chunks(local, 200000) {
		if _, err := st.ApplyBatch(0, batch, nil, int64(len(batch))); err != nil {
			fatal(err)
		}
	}
	for _, batch := range chunks(remote, 200000) {
		if _, err := st.ApplyBatch(node.ID, batch, nil, int64(len(batch))); err != nil {
			fatal(err)
		}
	}
	if err := st.ApplyTraffic(0, traffic, totals); err != nil {
		fatal(err)
	}
	var panelUsers []model.PanelUser
	for _, p := range people {
		t := totals[p.email]
		panelUsers = append(panelUsers, model.PanelUser{
			Email: p.email, Enabled: true, Up: t[0], Down: t[1], Quota: p.quota, LimitIP: p.limit,
			Expiry: now.AddDate(0, 0, p.expiry).Unix(), Inbound: "VLESS-Reality",
		})
	}
	if err := st.UpsertPanelUsers(panelUsers); err != nil {
		fatal(err)
	}
	fmt.Println("done. start the panel with:  XRAYSCOPE_DATA_DIR=" + *data + " xrayscope   (login: admin / demo12345)")
}

func setAdmin(st *store.Store) error {
	a, err := auth.New(st)
	if err != nil {
		return err
	}
	return a.SetAdmin("admin", "demo12345")
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

func chunks(s []model.Rec, n int) [][]model.Rec {
	var out [][]model.Rec
	for len(s) > 0 {
		k := n
		if k > len(s) {
			k = len(s)
		}
		out = append(out, s[:k])
		s = s[k:]
	}
	return out
}

func ipOf(prefix string, day, id int) string {
	return fmt.Sprintf("%s.%d.%d", prefix, (day*37+id*11)%250+1, (day*91+id*17)%250+1)
}

func personas(n int, r *rand.Rand) []persona {
	names := []string{"ali.rezaei", "sara.ahmadi", "mohammad.karimi", "neda.hosseini", "reza.mousavi", "maryam.jafari", "amir.shahbazi", "parisa.tehrani", "hamed.zand", "lila.farahani", "kian.moradi", "azadeh.nouri", "babak.salimi", "shirin.rahimi", "omid.ghasemi", "tina.sadeghi"}
	mixes := []map[string]float64{
		{"ai": 5, "dev": 4, "search": 2, "messaging": 2, "work": 2},
		{"social": 6, "messaging": 3, "video": 3, "music": 1.5, "shopping": 1},
		{"video": 5, "gaming": 4, "messaging": 2, "social": 2, "music": 1},
		{"news": 4, "social": 3, "messaging": 3, "search": 2, "finance": 2},
		{"ai": 3, "social": 3, "video": 3, "messaging": 2, "education": 0},
		{"finance": 5, "news": 3, "messaging": 2, "search": 2, "ai": 2},
	}
	var out []persona
	for i := 0; i < n; i++ {
		id := i + 1
		p := persona{
			email: names[i%len(names)], limit: 2,
			wake: 6.5 + r.Float64()*2.5, bed: 22.5 + r.Float64()*2.5,
			night: r.Float64() < 0.55, likes: mixes[i%len(mixes)],
			quota: int64(40+r.Intn(160)) << 30, expiry: 5 + r.Intn(70),
		}
		if i >= len(names) {
			p.email = fmt.Sprintf("%s%d", names[i%len(names)], i/len(names)+1)
		}
		home := func(day int, _ *rand.Rand) string { return ipOf("5.201", 0, id) }
		mobile := func(day int, _ *rand.Rand) string { return ipOf("37.254", day, id) }
		phone := device{os: []string{"android", "ios", "android"}[i%3], ip: mobile, node: i%3 == 1}
		wifi := device{os: phone.os, ip: home}
		p.devices = []device{phone, wifi}
		if i%2 == 0 {
			p.devices = append(p.devices, device{os: []string{"windows", "macos"}[i%4/2], ip: home, laptop: true})
		}
		if i == 2 {
			p.extra = true
			p.limit = 2
		}
		if i == 5 {
			p.bed = 3.2 // night owl
			p.wake = 11
		}
		out = append(out, p)
	}
	return out
}

type rec struct {
	model.Rec
	node bool
}

func pick(w map[string]float64, r *rand.Rand) string {
	var total float64
	for _, v := range w {
		total += v
	}
	x := r.Float64() * total
	for _, k := range []string{"ai", "social", "messaging", "video", "music", "gaming", "dev", "news", "search", "shopping", "finance", "work", "education"} {
		v, ok := w[k]
		if !ok {
			continue
		}
		if x < v {
			return k
		}
		x -= v
	}
	return "search"
}

// bytesFor estimates the traffic of one active minute in a category.
func bytesFor(cat string, r *rand.Rand) (up, down int64) {
	base := map[string]float64{"video": 38e6, "gaming": 9e6, "music": 3e6, "social": 12e6, "ai": 1.2e6, "dev": 4e6, "messaging": 1.5e6}[cat]
	if base == 0 {
		base = 2.5e6
	}
	down = int64(base * (0.4 + r.Float64()*1.2))
	up = down / int64(6+r.Intn(10))
	return
}

func simulate(p persona, day time.Time, dayIdx int, now time.Time, r *rand.Rand) ([]rec, []model.TrafficRec) {
	var out []rec
	hourly := map[int64][2]int64{}
	add := func(t time.Time, d device, host string, n int, extraIP string) {
		if t.After(now) {
			return
		}
		ip := d.ip(dayIdx, r)
		if extraIP != "" {
			ip = extraIP
		}
		out = append(out, rec{Rec: model.Rec{M: t.Unix() / 60, U: p.email, IP: ip, D: host, N: n}, node: d.node})
	}
	at := func(hour float64) time.Time { return day.Add(time.Duration(hour * float64(time.Hour))) }
	wake := p.wake + r.NormFloat64()*0.35
	bed := p.bed + r.NormFloat64()*0.4
	weekend := day.Weekday() == time.Friday || day.Weekday() == time.Thursday
	if weekend {
		wake += 1
		bed += 0.6
	}
	traffic := func(t time.Time, cat string) {
		up, down := bytesFor(cat, r)
		h := t.Unix() / 3600
		c := hourly[h]
		hourly[h] = [2]int64{c[0] + up, c[1] + down}
	}

	for di, d := range p.devices {
		sessions := 14 + r.Intn(14)
		if d.laptop {
			sessions = 1 + r.Intn(3)
		}
		if di == 1 { // wifi phone: evenings at home
			sessions = 6 + r.Intn(8)
		}
		for s := 0; s < sessions; s++ {
			var from float64
			switch {
			case d.laptop:
				from = 9 + r.Float64()*9
			case di == 1:
				from = 18 + r.Float64()*(math.Max(bed, 19)-18)
			default:
				from = wake + r.Float64()*(math.Min(bed, 26)-wake)
			}
			length := 3 + r.Intn(22)
			if d.laptop {
				length = 40 + r.Intn(140)
			}
			theme := pick(p.likes, r)
			if d.laptop && r.Float64() < 0.6 {
				theme = []string{"work", "dev", "ai", "news"}[r.Intn(4)]
			}
			if from > bed && bed > wake { // past bedtime
				continue
			}
			for m := 0; m < length; m++ {
				t := at(from).Add(time.Duration(m) * time.Minute)
				cat := theme
				if r.Float64() < 0.2 {
					cat = pick(p.likes, r)
				}
				if r.Float64() < 0.85 {
					host := pools[cat][r.Intn(len(pools[cat]))]
					add(t, d, host, 1+r.Intn(6), "")
					traffic(t, cat)
				}
				if r.Float64() < 0.12 {
					add(t, d, osNoise[d.os][r.Intn(len(osNoise[d.os]))], 1, "")
				}
				if r.Float64() < 0.1 {
					add(t, d, background[r.Intn(len(background))], 1, "")
				}
			}
		}
		// background chatter all day, and through the night for some users
		step := 7 + r.Intn(6)
		for m := 0; m < 1440; m += step {
			tt := day.Add(time.Duration(m) * time.Minute)
			hr := float64(m) / 60
			asleep := hr < wake-0.3 || hr > bed+0.2
			if di == 1 || (asleep && !(p.night && di == 0)) {
				continue
			}
			if d.laptop && asleep {
				continue
			}
			add(tt, d, osNoise[d.os][r.Intn(len(osNoise[d.os]))], 1, "")
			if r.Float64() < 0.5 {
				add(tt, d, background[r.Intn(len(background))], 1, "")
			}
			traffic(tt, "push")
		}
	}
	if p.extra { // a friend shares the account in the evening from two more addresses
		for m := 0; m < 150; m++ {
			t := at(20.2).Add(time.Duration(m) * time.Minute)
			if r.Float64() < 0.5 {
				add(t, device{os: "windows", ip: func(int, *rand.Rand) string { return "185.14.22.9" }}, pools["video"][r.Intn(3)], 2, "185.14.22.9")
				traffic(t, "video")
			}
			if r.Float64() < 0.4 {
				add(t, device{os: "android", ip: func(int, *rand.Rand) string { return "93.119.8.77" }}, pools["social"][r.Intn(4)], 2, "93.119.8.77")
				traffic(t, "social")
			}
		}
	}
	var tr []model.TrafficRec
	for h, v := range hourly {
		if h*3600 > now.Unix() {
			continue
		}
		tr = append(tr, model.TrafficRec{T: h*3600 + 1800, U: p.email, Up: v[0], Down: v[1]})
	}
	return out, tr
}

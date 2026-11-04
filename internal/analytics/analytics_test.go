package analytics

import (
	"testing"
	"time"

	"xrayscope/internal/store"
)

func TestOSFamilyVendors(t *testing.T) {
	for in, want := range map[string][2]string{
		"LG TV": {"Smart TV", "LG"}, "OnePlus Android": {"Android", "OnePlus"},
		"Samsung Android": {"Android", "Samsung"}, "Windows": {"Windows", ""}, "ChromeOS": {"ChromeOS", ""},
	} {
		if f, v := osFamily(in); f != want[0] || v != want[1] {
			t.Errorf("%s -> %s/%s", in, f, v)
		}
	}
}

func TestStreamAwake(t *testing.T) {
	// a film from 100 to 140 (YouTube opened at 98); an update download from
	// 300 to 320 with no media service around it
	var heavy []int64
	for m := int64(100); m <= 140; m++ {
		if m != 120 { // one quieter minute inside the film
			heavy = append(heavy, m)
		}
	}
	for m := int64(300); m <= 320; m++ {
		heavy = append(heavy, m)
	}
	got := StreamAwake(heavy, []int64{98})
	if len(got) != 41 || got[0] != 100 || got[40] != 140 {
		t.Fatalf("awake = %d minutes (%v..)", len(got), got[:2])
	}
}

func TestEstimateSiteBytes(t *testing.T) {
	// hour 10: site 1 in minutes 0-4 (8 requests), site 2 in minutes 0-1
	// (2 requests); 1000 bytes in the first five minutes, 500 in minutes
	// 10-14 when nothing was visited
	rows := []store.VisitRow{
		{Slot: 10, DomainID: 1, Mask: 0x1f, Reqs: 8},
		{Slot: 10, DomainID: 2, Mask: 0x3, Reqs: 2},
	}
	t5 := []store.TrafficPoint{{Slot: 120, Down: 900, Up: 100}, {Slot: 122, Down: 500}}
	got := EstimateSiteBytes(rows, t5, nil)
	if got[1] != 800 || got[2] != 200 {
		t.Fatalf("split = %v", got)
	}
	// without five-minute detail the hour is split by requests
	got = EstimateSiteBytes(rows, nil, []store.TrafficPoint{{Slot: 10, Down: 1000}})
	if got[1] != 800 || got[2] != 200 {
		t.Fatalf("hourly split = %v", got)
	}
}

func TestIsPeriodic(t *testing.T) {
	// a ping every 5 minutes for two hours (one minute late now and then)
	var poll []int64
	for i := int64(0); i < 24; i++ {
		m := 1000 + i*5
		if i%7 == 3 {
			m++
		}
		poll = append(poll, m)
	}
	if !IsPeriodic(poll) {
		t.Fatal("regular polling not detected")
	}
	// a person: irregular stretches of several minutes
	var human []int64
	for _, s := range []struct{ at, n int64 }{{0, 6}, {20, 3}, {31, 1}, {70, 12}, {95, 2}, {160, 4}, {171, 1}, {240, 8}, {300, 2}, {303, 1}, {390, 5}, {420, 2}, {500, 7}} {
		for j := int64(0); j < s.n; j++ {
			human = append(human, s.at+j)
		}
	}
	if IsPeriodic(human) {
		t.Fatal("human use taken for polling")
	}
	if IsPeriodic(poll[:6]) {
		t.Fatal("too little evidence must not be judged")
	}
}

func TestMergeHops(t *testing.T) {
	// ip 1 until minute 100, ip 2 (same operator) from minute 99: one phone
	// that changed address; ip 3 (another operator) overlaps: a second device
	runs := []Run{{IPID: 1, Start: 0, End: 100}, {IPID: 2, Start: 99, End: 200}, {IPID: 3, Start: 150, End: 180}}
	same := func(a, b int64) bool { return a+b == 3 } // 1 and 2
	if got := MaxConcurrent(runs); got != 2 {
		t.Fatalf("raw peak = %d", got)
	}
	merged := MergeHops(runs, same)
	ov := Overlaps(merged, 1)
	for _, o := range ov {
		if o.Start < 150 {
			t.Fatalf("the address change counted as two devices: %+v", ov)
		}
	}
	if MaxConcurrent(merged) != 2 {
		t.Fatalf("peak = %d, want 2", MaxConcurrent(merged))
	}
	// without the same operator nothing is merged
	if MaxConcurrent(MergeHops(runs, func(a, b int64) bool { return false })) != 2 {
		t.Fatal("unmerged peak changed")
	}
	if o := Overlaps(MergeHops(runs, func(a, b int64) bool { return false }), 1); len(o) == 0 || o[0].Start != 99 {
		t.Fatalf("unrelated addresses must still overlap at the handover: %+v", o)
	}
}

func TestMergeMinutes(t *testing.T) {
	got := MergeMinutes([]int64{1, 3, 5}, []int64{2, 3, 6, 7})
	want := []int64{1, 2, 3, 5, 6, 7}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v", got)
		}
	}
	if m := MaskMinutes(map[int64]int64{2: 1<<0 | 1<<5}, 120, 125); len(m) != 1 || m[0] != 120 {
		t.Fatalf("MaskMinutes = %v", m)
	}
}

func TestTuningTurnsBridgingOff(t *testing.T) {
	defer SetTuning(DefaultTuning())
	services := map[int64]store.Service{1: {ID: 1, Category: "video", Name: "YouTube"}}
	rows := []store.SvcRow{{UserID: 1, ServiceID: 1, Hour: 10, Mask: 1<<0 | 1<<3, Reqs: 4}}
	_, svcs := TimeSpent(rows, services, nil)
	if svcs[0].Minutes != 4 {
		t.Fatalf("default: %d minutes", svcs[0].Minutes)
	}
	SetTuning(Tuning{BridgeMin: 0, TrafficMax: 0, TrafficShare: 70})
	_, svcs = TimeSpent(rows, services, nil)
	if svcs[0].Minutes != 2 {
		t.Fatalf("bridging off: %d minutes", svcs[0].Minutes)
	}
}

func TestBridgeTraffic(t *testing.T) {
	// requests at minutes 0 and 20 of one hour; the 19 minutes between have
	// traffic in 15 of them (79 percent), so the pause counts
	m := map[int64]int64{10: 1<<0 | 1<<20}
	tr := map[int64]int64{10: (1<<20 - 1) &^ (1<<0 | 1<<5 | 1<<9 | 1<<13)}
	BridgeTraffic(m, tr)
	if got := PopCount(m[10]); got != 21 {
		t.Fatalf("busy pause: %d minutes", got)
	}
	// the same pause without traffic stays empty
	m = map[int64]int64{10: 1<<0 | 1<<20}
	BridgeTraffic(m, map[int64]int64{10: 1 << 40})
	if got := PopCount(m[10]); got != 2 {
		t.Fatalf("quiet pause: %d minutes", got)
	}
	// and a pause longer than the limit is never bridged
	m = map[int64]int64{10: 1 << 0, 11: 1 << 5}
	BridgeTraffic(m, map[int64]int64{10: ^int64(1), 11: 0x1f})
	if PopCount(m[10]) != 1 {
		t.Fatal("a very long pause must not be bridged")
	}
}

func TestBridgeGaps(t *testing.T) {
	// minutes 0 and 3 of hour 10 (a gap of two), minute 58 of hour 10 and
	// minute 1 of hour 11 (a gap of two across the hour), minute 30 alone
	m := map[int64]int64{10: 1<<0 | 1<<3 | 1<<30 | 1<<58, 11: 1 << 1}
	BridgeGaps(m, 4)
	if got := PopCount(m[10]); got != 4+1+2 { // 0..3, 30, 58..59
		t.Fatalf("hour 10 has %d minutes", got)
	}
	if got := PopCount(m[11]); got != 2 { // 0..1
		t.Fatalf("hour 11 has %d minutes", got)
	}
	if m[10]&(1<<20) != 0 {
		t.Fatal("a long pause must not be bridged")
	}
}

var tehran = time.FixedZone("IRST", 3*3600+1800)

func TestSessions(t *testing.T) {
	mins := []int64{100, 101, 102, 110, 111, 200}
	got := Sessions(mins, 5)
	if len(got) != 3 {
		t.Fatalf("sessions = %+v", got)
	}
	if got[0].Start != 100 || got[0].End != 102 || got[0].Active != 3 {
		t.Fatalf("first = %+v", got[0])
	}
	if got := Sessions(mins, 10); len(got) != 2 {
		t.Fatalf("with gap 10: %+v", got)
	}
}

func TestOverlaps(t *testing.T) {
	runs := []Run{
		{IPID: 1, Start: 0, End: 100},
		{IPID: 2, Start: 50, End: 80},   // fully inside run 1
		{IPID: 3, Start: 100, End: 120}, // touches run 1 for one minute
		{IPID: 4, Start: 300, End: 310},
	}
	got := Overlaps(runs, 2)
	if len(got) != 1 {
		t.Fatalf("overlaps = %+v", got)
	}
	o := got[0]
	if o.Start != 50 || o.End != 80 || o.Peak != 2 || len(o.IPs) != 2 {
		t.Fatalf("overlap = %+v", o)
	}
	if n := len(Overlaps(runs, 1)); n != 2 {
		t.Fatalf("minLen 1 should also report the 1-minute touch, got %d", n)
	}
	if MaxConcurrent(runs) != 2 {
		t.Fatalf("max = %d", MaxConcurrent(runs))
	}
	if MaxConcurrent([]Run{{IPID: 1, Start: 0, End: 5}}) != 1 {
		t.Fatal("single run must report 1")
	}
}

// synthetic user: awake 07:30-23:30 local with an interactive request every
// 2 minutes, asleep otherwise with a background request every 15 minutes.
func syntheticRows(days int, loc *time.Location) ([]store.ActRow, int64, int64) {
	start := time.Date(2025, 3, 1, 0, 0, 0, 0, loc)
	var pres, inter []int64
	for d := 0; d < days; d++ {
		day := start.AddDate(0, 0, d)
		for m := 0; m < 1440; m++ {
			ts := day.Add(time.Duration(m)*time.Minute).Unix() / 60
			awake := m >= 7*60+30 && m < 23*60+30
			if awake && m%2 == 0 {
				pres = append(pres, ts)
				inter = append(inter, ts)
			}
			if !awake && m%15 == 0 {
				pres = append(pres, ts)
			}
		}
	}
	rows := map[int64]*store.ActRow{}
	add := func(ms []int64, interactive bool) {
		for _, m := range ms {
			h := m / 60
			r := rows[h]
			if r == nil {
				r = &store.ActRow{Hour: h, UserID: 1, IPID: 1}
				rows[h] = r
			}
			bit := int64(1) << uint(m%60)
			r.Mask |= bit
			if interactive {
				r.IMask |= bit
			}
		}
	}
	add(pres, false)
	add(inter, true)
	var out []store.ActRow
	for _, r := range rows {
		out = append(out, *r)
	}
	first := localDay(loc, start.Unix()/60)
	return out, first, first + int64(days) - 1
}

func TestAnalyzeSleep(t *testing.T) {
	rows, first, last := syntheticRows(10, tehran)
	pres := UnionMinutes(rows, Presence)
	inter := UnionMinutes(rows, Interactive)
	got := AnalyzeSleep(pres, inter, first+1, last, tehran, DefaultSleepOptions())
	if got.DaysAnalyzed < 8 {
		t.Fatalf("days analysed = %d (%+v)", got.DaysAnalyzed, got)
	}
	if got.TypicalStart < "23:25" || got.TypicalStart > "23:45" {
		t.Fatalf("typical start = %s", got.TypicalStart)
	}
	if got.TypicalEnd < "07:15" || got.TypicalEnd > "07:35" {
		t.Fatalf("typical end = %s", got.TypicalEnd)
	}
	if got.AvgMinutes < 450 || got.AvgMinutes > 500 {
		t.Fatalf("avg = %d", got.AvgMinutes)
	}
	if got.ConnectedShare < 0.9 {
		t.Fatalf("background traffic overnight must be reported as connected: %v", got.ConnectedShare)
	}
	if got.Confidence < 0.7 {
		t.Fatalf("confidence = %v", got.Confidence)
	}
	for _, d := range got.Days {
		if d.Kind != "connected" || d.Atypical {
			t.Fatalf("day = %+v", d)
		}
	}
}

func TestAnalyzeSleepIgnoresLightUsers(t *testing.T) {
	rows, first, last := syntheticRows(5, tehran)
	pres := UnionMinutes(rows, Presence)
	got := AnalyzeSleep(pres, pres[:10], first+1, last, tehran, DefaultSleepOptions())
	if got.DaysAnalyzed != 0 {
		t.Fatalf("analysed %d days from almost no interactive data", got.DaysAnalyzed)
	}
}

func TestHeatmapAndDaily(t *testing.T) {
	rows, first, last := syntheticRows(3, tehran)
	h := Heatmap(rows, tehran, true)
	var total int64
	for wd := range h {
		for hod := range h[wd] {
			total += h[wd][hod]
			if hod < 7 && h[wd][hod] != 0 {
				t.Fatalf("interactive minute at %02d:00 on weekday %d", hod, wd)
			}
		}
	}
	if total != 3*480 { // 16 h awake, every 2nd minute
		t.Fatalf("total interactive minutes = %d", total)
	}
	days := Daily(rows, tehran)
	if int64(len(days)) < last-first {
		t.Fatalf("days = %d", len(days))
	}
}

func TestTimeSpentUsesUnionPerCategory(t *testing.T) {
	services := map[int64]store.Service{
		1: {ID: 1, Category: "ai", Name: "ChatGPT"},
		2: {ID: 2, Category: "ai", Name: "Claude"},
	}
	rows := []store.SvcRow{
		{Hour: 10, UserID: 1, ServiceID: 1, Mask: 0b1111, Reqs: 4},
		{Hour: 10, UserID: 1, ServiceID: 2, Mask: 0b1100, Reqs: 2},
	}
	cats, svcs := TimeSpent(rows, services, nil)
	if len(cats) != 1 || cats[0].Minutes != 4 || cats[0].Reqs != 6 {
		t.Fatalf("cats = %+v", cats)
	}
	if len(svcs) != 2 || svcs[0].Service != "ChatGPT" || svcs[0].Minutes != 4 {
		t.Fatalf("svcs = %+v", svcs)
	}
}

func TestGuessDevices(t *testing.T) {
	rows := []store.FPRow{
		{IPID: 1, Kind: "os", Value: "Windows", N: 100},
		{IPID: 1, Kind: "os", Value: "Android", N: 40},
		{IPID: 1, Kind: "os", Value: "Samsung Android", N: 30},
		{IPID: 1, Kind: "browser", Value: "Chrome", N: 30},
		{IPID: 2, Kind: "os", Value: "Apple", N: 3},
		{IPID: 2, Kind: "os", Value: "iOS", N: 9},
		{IPID: 3, Kind: "browser", Value: "Safari", N: 5},
	}
	got := GuessDevices(rows)
	if len(got[1]) != 2 || got[1][0].OS != "Windows" || got[1][1].OS != "Android" || got[1][1].Vendor != "Samsung" {
		t.Fatalf("ip1 = %+v", got[1])
	}
	if got[1][0].Browser != "Chrome" {
		t.Fatalf("browser = %+v", got[1][0])
	}
	if len(got[2]) != 1 || got[2][0].OS != "iOS" {
		t.Fatalf("ip2 = %+v", got[2])
	}
	if got[2][0].Confidence <= 0 || got[2][0].Confidence >= 0.9 {
		t.Fatalf("low evidence must give modest confidence: %v", got[2][0].Confidence)
	}
}

package report

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"xrayscope/internal/model"
	"xrayscope/internal/store"
)

// seed creates ten days of a user who is awake 08:00-23:00 UTC, uses AI and
// social sites, keeps a background connection overnight and, on the last
// evening, connects from a second address at the same time.
func seed(t *testing.T) (*Env, int64) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "r.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	var recs []model.Rec
	for d := 10; d >= 1; d-- {
		day := today.AddDate(0, 0, -d)
		for m := 0; m < 1440; m++ {
			ts := day.Add(time.Duration(m)*time.Minute).Unix() / 60
			awake := m >= 8*60 && m < 23*60
			switch {
			case awake && m%2 == 0:
				host := "chatgpt.com"
				if m%4 == 0 {
					host = "i.instagram.com"
				}
				recs = append(recs, model.Rec{M: ts, U: "alice", IP: "1.1.1.1", D: host, N: 3})
				if m%10 == 0 {
					recs = append(recs, model.Rec{M: ts, U: "alice", IP: "1.1.1.1", D: "settings-win.data.microsoft.com", N: 1})
				}
				if d == 1 && m >= 20*60 && m < 22*60 {
					recs = append(recs, model.Rec{M: ts, U: "alice", IP: "2.2.2.2", D: "www.youtube.com", N: 2})
				}
			case !awake && m%15 == 0:
				recs = append(recs, model.Rec{M: ts, U: "alice", IP: "1.1.1.1", D: "connectivitycheck.gstatic.com", N: 1})
			}
		}
	}
	if _, err := s.ApplyBatch(0, recs, nil, int64(len(recs))); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyTraffic(0, []model.TrafficRec{
		{T: today.Add(-30 * time.Hour).Unix(), U: "alice", Up: 1000, Down: 9000},
		{T: today.Add(-5 * time.Hour).Unix(), U: "alice", Up: 500, Down: 4000},
	}, nil); err != nil {
		t.Fatal(err)
	}
	u, err := s.UserByEmail(context.Background(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	return &Env{Store: s, Loc: time.UTC, GapMin: 5, OnlineWin: 3 * time.Minute}, u.ID
}

func TestUserReport(t *testing.T) {
	e, id := seed(t)
	now := time.Now().Unix()
	rg := Range{From: now - 8*86400, To: now}
	r, err := e.User(context.Background(), id, rg)
	if err != nil {
		t.Fatal(err)
	}
	if r.Activity.ActiveDays < 7 || r.Activity.Sessions < 7 {
		t.Fatalf("activity = %+v", r.Activity)
	}
	if r.Activity.InteractiveMinutes == 0 || r.Activity.InteractiveMinutes >= r.Activity.OnlineMinutes {
		t.Fatalf("interactive %d of %d", r.Activity.InteractiveMinutes, r.Activity.OnlineMinutes)
	}
	ai := r.Favorites["ai"]
	if len(ai) == 0 || ai[0].Service != "ChatGPT" {
		t.Fatalf("favourites = %+v", r.Favorites)
	}
	if len(r.ByCategory) == 0 || r.ByCategory[0].Category == "" {
		t.Fatalf("categories = %+v", r.ByCategory)
	}
	if len(r.TopSites) == 0 {
		t.Fatal("no sites")
	}
	if len(r.IPs) != 2 {
		t.Fatalf("ips = %+v", r.IPs)
	}
	var win bool
	for _, d := range r.Devices {
		if d.OS == "Windows" {
			win = true
		}
	}
	if !win {
		t.Fatalf("devices = %+v", r.Devices)
	}
	if r.Concurrency.Max != 2 || len(r.Concurrency.Overlaps) != 1 {
		t.Fatalf("concurrency = %+v", r.Concurrency)
	}
	if r.Sleep.DaysAnalyzed < 6 || r.Sleep.TypicalStart < "22:50" || r.Sleep.TypicalStart > "23:20" {
		t.Fatalf("sleep = %+v", r.Sleep)
	}
	if r.Sleep.ConnectedShare < 0.9 {
		t.Fatalf("overnight background traffic not detected: %+v", r.Sleep)
	}
	if r.Traffic.Up != 1500 || r.Traffic.Down != 13000 {
		t.Fatalf("traffic = %+v", r.Traffic)
	}
}

func TestTimelineAndOverview(t *testing.T) {
	e, id := seed(t)
	day := time.Now().UTC().AddDate(0, 0, -2).Format("2006-01-02")
	tl, err := e.Timeline(context.Background(), id, day)
	if err != nil {
		t.Fatal(err)
	}
	if tl.Minutes != 1440 || len(tl.Strip) != 1440 {
		t.Fatalf("strip = %d", len(tl.Strip))
	}
	if tl.Strip[3*60] != '1' || tl.Strip[12*60] != '2' || tl.Strip[3*60+1] != '0' {
		t.Fatalf("strip wrong: night=%c day=%c gap=%c", tl.Strip[3*60], tl.Strip[12*60], tl.Strip[3*60+1])
	}
	if len(tl.Blocks) == 0 || len(tl.Sessions) < 2 {
		t.Fatalf("blocks=%d sessions=%d", len(tl.Blocks), len(tl.Sessions))
	}

	now := time.Now().Unix()
	o, err := e.Overview(context.Background(), Range{From: now - 5*86400, To: now})
	if err != nil {
		t.Fatal(err)
	}
	if o.Totals.UsersActive != 1 || len(o.Categories) == 0 || len(o.Activity) == 0 {
		t.Fatalf("overview = %+v", o.Totals)
	}
	if o.Prev.UsersActive != 1 || o.Prev.Minutes == 0 {
		t.Fatalf("previous period missing: %+v", o.Prev)
	}
	rows, err := e.Users(context.Background(), Range{From: now - 5*86400, To: now})
	if err != nil || len(rows) != 1 || rows[0].TopCategory != "ai" && rows[0].TopCategory != "social" && rows[0].TopCategory != "video" {
		t.Fatalf("users = %+v err=%v", rows, err)
	}

	if len(rows[0].Spark) != 5 {
		t.Fatalf("spark bins = %d", len(rows[0].Spark))
	}
	var sum int64
	for _, v := range rows[0].Spark {
		sum += int64(v)
	}
	if sum != rows[0].Minutes {
		t.Fatalf("spark holds %d minutes, row says %d", sum, rows[0].Minutes)
	}
	short, _ := e.Users(context.Background(), Range{From: now - 3600, To: now})
	if len(short[0].Spark) != 24 {
		t.Fatalf("short range must give 24 hourly slices, got %d", len(short[0].Spark))
	}

	sl, err := e.Sites(context.Background(), id, Range{From: now - 5*86400, To: now}, "insta", "", 10, 0)
	if err != nil || sl.Total != 1 || sl.Sites[0].Domain != "instagram.com" {
		t.Fatalf("sites = %+v err=%v", sl, err)
	}
}

func TestClipMask(t *testing.T) {
	hour := int64(100)
	base := hour * 60
	full := int64(1<<60 - 1)
	if got := ClipMask(hour, full, base+10, base+20); got != (1<<20-1)&^(1<<10-1) {
		t.Fatalf("clip = %b", got)
	}
	if ClipMask(hour, full, base+60, base+120) != 0 || ClipMask(hour, full, base-60, base) != 0 {
		t.Fatal("ranges outside the hour must clear the mask")
	}
	if ClipMask(hour, full, base-5, base+100) != full {
		t.Fatal("covering range must keep the mask")
	}
}

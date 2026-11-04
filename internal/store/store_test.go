package store

import (
	"context"
	"math/bits"
	"path/filepath"
	"testing"
	"time"

	"xrayscope/internal/model"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "x.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestApplyBatchAndQuery(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	base := time.Now().Add(-2*time.Hour).Unix() / 60

	recs := []model.Rec{
		{M: base, U: "alice", IP: "1.1.1.1", D: "chatgpt.com", N: 3},
		{M: base, U: "alice", IP: "1.1.1.1", D: "connectivitycheck.gstatic.com", N: 1},
		{M: base + 1, U: "alice", IP: "1.1.1.1", D: "connectivitycheck.gstatic.com", N: 1},
		{M: base + 1, U: "alice", IP: "2.2.2.2", D: "instagram.com", N: 2},
		{M: base, U: "bob", IP: "3.3.3.3", D: "settings-win.data.microsoft.com", N: 5},
	}
	obs, err := s.ApplyBatch(0, recs, map[string]string{"offset": "42"}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(obs) != 3 {
		t.Fatalf("obs = %d, want 3", len(obs))
	}
	// applying the same minute again must merge, not duplicate
	if _, err := s.ApplyBatch(0, recs[:1], nil, 1); err != nil {
		t.Fatal(err)
	}
	if v, _, _ := s.KV("offset"); v != "42" {
		t.Fatalf("offset kv = %q", v)
	}

	users, _ := s.Users(ctx)
	if len(users) != 2 {
		t.Fatalf("users = %d", len(users))
	}
	alice, err := s.UserByEmail(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if alice.FirstSeen != base*60 || alice.LastSeen != (base+1)*60 {
		t.Fatalf("seen = %d..%d", alice.FirstSeen, alice.LastSeen)
	}

	hour := base / 60
	rows, err := s.Act(ctx, alice.ID, hour-1, hour+2)
	if err != nil {
		t.Fatal(err)
	}
	var total, inter int
	var reqs int64
	for _, r := range rows {
		total += bits.OnesCount64(uint64(r.Mask))
		inter += bits.OnesCount64(uint64(r.IMask))
		reqs += r.Reqs
	}
	if reqs != 3+1+1+2+3 {
		t.Fatalf("reqs = %d", reqs)
	}
	if total == 0 || inter == 0 || inter > total {
		t.Fatalf("masks total=%d interactive=%d", total, inter)
	}

	svc, _ := s.Svc(ctx, alice.ID, hour-1, hour+2)
	names, _ := s.Services(ctx)
	seen := map[string]bool{}
	for _, v := range svc {
		seen[names[v.ServiceID].Name] = true
	}
	for _, want := range []string{"ChatGPT", "Instagram", "Connectivity check"} {
		if !seen[want] {
			t.Errorf("service %q missing: %v", want, seen)
		}
	}

	fp, _ := s.FP(ctx, 0, 0, 1<<40)
	found := false
	for _, f := range fp {
		if f.Kind == "os" && f.Value == "Windows" {
			found = true
		}
	}
	if !found {
		t.Fatalf("windows fingerprint missing: %+v", fp)
	}
}

// A lone request to a site in a minute (a widget, a tracker) must not count
// as the user using it, but two in the same minute do, even when they arrive
// in separate batches.
func TestStrayRequestsDoNotCountAsUse(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	base := time.Now().Add(-time.Hour).Unix() / 60

	stray := []model.Rec{{M: base, U: "dana", IP: "1.1.1.1", D: "www.instagram.com", N: 1}}
	if _, err := s.ApplyBatch(0, stray, nil, 1); err != nil {
		t.Fatal(err)
	}
	u, _ := s.UserByEmail(ctx, "dana")
	rows, _ := s.Act(ctx, u.ID, base/60-1, base/60+2)
	var inter int64
	for _, r := range rows {
		inter |= r.IMask
	}
	svc, _ := s.Svc(ctx, u.ID, base/60-1, base/60+2)
	if inter != 0 || len(svc) != 0 {
		t.Fatalf("one stray request counted as use: imask=%b svc=%d", inter, len(svc))
	}
	if len(rows) == 0 || rows[0].Mask == 0 {
		t.Fatal("the connection itself must still be recorded as presence")
	}

	// the second request in the same minute arrives in a later batch
	if _, err := s.ApplyBatch(0, stray, nil, 1); err != nil {
		t.Fatal(err)
	}
	rows, _ = s.Act(ctx, u.ID, base/60-1, base/60+2)
	inter = 0
	for _, r := range rows {
		inter |= r.IMask
	}
	svc, _ = s.Svc(ctx, u.ID, base/60-1, base/60+2)
	if inter == 0 || len(svc) != 1 {
		t.Fatalf("two requests in a minute must count: imask=%b svc=%d", inter, len(svc))
	}
}

// A connection to a bare address is told apart by who owns the network.
func TestAddressClassifiedByOwner(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	s.SetGeo(func(ip string) (string, int, string) {
		if ip == "149.154.167.51" {
			return "NL", 62041, "Telegram Messenger Inc"
		}
		return "US", 1, "Some Hosting Ltd"
	})
	base := time.Now().Add(-time.Hour).Unix() / 60
	recs := []model.Rec{
		{M: base, U: "eve", IP: "1.1.1.1", D: "149.154.167.51", N: 3},
		{M: base, U: "eve", IP: "1.1.1.1", D: "203.0.113.77", N: 3},
	}
	if _, err := s.ApplyBatch(0, recs, nil, 2); err != nil {
		t.Fatal(err)
	}
	u, _ := s.UserByEmail(ctx, "eve")
	svc, _ := s.Svc(ctx, u.ID, base/60-1, base/60+2)
	services, _ := s.Services(ctx)
	got := map[string]bool{}
	for _, r := range svc {
		got[services[r.ServiceID].Category+"/"+services[r.ServiceID].Name] = true
	}
	if !got["messaging/Telegram"] {
		t.Fatalf("the Telegram address was not recognised: %v", got)
	}
	if got["messaging/Telegram"] && len(got) != 2 {
		t.Fatalf("an unknown address must stay a direct IP: %v", got)
	}
}

func TestTrafficDeltas(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	now := time.Now().Unix()
	deltas := []model.TrafficRec{
		{T: now, U: "alice", Up: 10, Down: 100},
		{T: now + 1, U: "alice", Up: 5, Down: 50},
	}
	if err := s.ApplyTraffic(0, deltas, map[string][2]int64{"alice": {15, 150}}); err != nil {
		t.Fatal(err)
	}
	last, _ := s.TrafficLast(0)
	if last["alice"] != [2]int64{15, 150} {
		t.Fatalf("last = %v", last)
	}
	pts, _ := s.TrafficHourly(ctx, 0, now/3600-1, now/3600+2)
	var up, down int64
	for _, p := range pts {
		up += p.Up
		down += p.Down
	}
	if up != 15 || down != 150 {
		t.Fatalf("hourly up=%d down=%d", up, down)
	}
}

func TestCompactVisits(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	old := time.Now().Add(-100*24*time.Hour).Unix() / 60
	recs := []model.Rec{
		{M: old, U: "a", IP: "1.1.1.1", D: "example.com", N: 2},
		{M: old + 120, U: "a", IP: "1.1.1.1", D: "example.com", N: 3},
	}
	if _, err := s.ApplyBatch(0, recs, nil, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Compact(time.Now().Add(-30*24*time.Hour), time.Now().Add(-14*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	u, _ := s.UserByEmail(ctx, "a")
	rows, err := s.Visits(ctx, u.ID, old/60-48, old/60+48)
	if err != nil {
		t.Fatal(err)
	}
	var reqs int64
	for _, r := range rows {
		if r.Grain != 1 {
			t.Fatalf("expected daily rows, got %+v", r)
		}
		reqs += r.Reqs
	}
	if reqs != 5 {
		t.Fatalf("reqs after compaction = %d", reqs)
	}
}

func TestNodeAuth(t *testing.T) {
	s := openTest(t)
	n, tok, err := s.CreateNode("de-1")
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.NodeByToken(context.Background(), tok)
	if err != nil || got.ID != n.ID {
		t.Fatalf("auth failed: %v %+v", err, got)
	}
	if _, err := s.NodeByToken(context.Background(), "bad"); err == nil {
		t.Fatal("bad token accepted")
	}
	if _, _, err := s.CreateNode("de-1"); err == nil {
		t.Fatal("duplicate name accepted")
	}
}

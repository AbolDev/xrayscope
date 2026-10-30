package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"xrayscope/internal/config"
	"xrayscope/internal/model"
	"xrayscope/internal/store"
)

type tg struct {
	mu   sync.Mutex
	text []string
}

func (f *tg) all() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.text...)
}

func newTestApp(t *testing.T) (*App, *tg) {
	t.Helper()
	fake := &tg{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		q, _ := url.ParseQuery(string(b))
		fake.mu.Lock()
		fake.text = append(fake.text, q.Get("text"))
		fake.mu.Unlock()
		io.WriteString(w, `{"ok":true,"result":{}}`)
	}))
	t.Cleanup(srv.Close)

	cfg := config.Default()
	cfg.DataDir = filepath.Join(t.TempDir(), "data")
	cfg.Panel.Type = "none"
	a, err := New(cfg, "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	s := a.Settings.Get()
	s.Language = "en"
	s.Telegram.Enabled, s.Telegram.Token, s.Telegram.ChatIDs, s.Telegram.APIBase = true, "1:tok", []int64{5}, srv.URL
	s.Alerts.MinOverlapMin = 1
	if err := a.Settings.Set(s); err != nil {
		t.Fatal(err)
	}
	return a, fake
}

func TestMaxIPsAlertFiresOnceAndRespectsCooldown(t *testing.T) {
	a, fake := newTestApp(t)
	ctx := context.Background()
	m := time.Now().Unix() / 60
	recs := []model.Rec{
		{M: m, U: "alice", IP: "1.1.1.1", D: "example.com", N: 1},
		{M: m, U: "alice", IP: "2.2.2.2", D: "example.com", N: 1},
		{M: m, U: "alice", IP: "3.3.3.3", D: "example.com", N: 1},
	}
	obs, err := a.Store.ApplyBatch(0, recs, nil, 3)
	if err != nil {
		t.Fatal(err)
	}
	a.Live.Observe(obs)
	if err := a.Store.UpsertPanelUsers([]model.PanelUser{{Email: "alice", Enabled: true, LimitIP: 2}}); err != nil {
		t.Fatal(err)
	}

	// first evaluation starts the clock, the second (after the overlap
	// window) raises the alert
	if err := a.evaluateAlerts(ctx); err != nil {
		t.Fatal(err)
	}
	if len(fake.all()) != 0 {
		t.Fatalf("alert fired immediately: %v", fake.all())
	}
	a.alerts.mu.Lock()
	for id := range a.alerts.ipSince {
		a.alerts.ipSince[id] = time.Now().Add(-2 * time.Minute)
	}
	a.alerts.mu.Unlock()
	if err := a.evaluateAlerts(ctx); err != nil {
		t.Fatal(err)
	}
	got := fake.all()
	if len(got) != 1 || !strings.Contains(got[0], "alice") || !strings.Contains(got[0], "3 IPs") || !strings.Contains(got[0], "1.1.1.1") {
		t.Fatalf("messages = %q", got)
	}
	_ = a.evaluateAlerts(ctx)
	if len(fake.all()) != 1 {
		t.Fatalf("cooldown ignored: %q", fake.all())
	}
	ev, _ := a.Store.Events(ctx, 0, 10)
	if len(ev) != 1 || ev[0].Kind != "max_ips" || strings.Contains(ev[0].Text, "<") {
		t.Fatalf("events = %+v", ev)
	}
}

func TestQuotaExpiryAndNodeAlerts(t *testing.T) {
	a, fake := newTestApp(t)
	ctx := context.Background()
	now := time.Now()
	if err := a.Store.UpsertPanelUsers([]model.PanelUser{
		{Email: "full", Enabled: true, Quota: 100, Up: 50, Down: 45},
		{Email: "soon", Enabled: true, Expiry: now.Add(36 * time.Hour).Unix()},
		{Email: "gone", Enabled: true, Expiry: now.Add(-time.Hour).Unix()},
		{Email: "fine", Enabled: true, Quota: 1 << 40, Up: 1, Expiry: now.Add(90 * 24 * time.Hour).Unix()},
		{Email: "off", Enabled: false, Quota: 10, Up: 10},
	}); err != nil {
		t.Fatal(err)
	}
	n, tok, _ := a.Store.CreateNode("de-1")
	_ = tok
	if _, err := a.Store.NodeByToken(ctx, tok); err != nil {
		t.Fatal(err)
	}
	if err := a.Store.TouchNode(n.ID, "9.9.9.9", "1", "3x-ui"); err != nil {
		t.Fatal(err)
	}
	// make the node look silent for an hour
	if _, err := a.Store.ApplyBatch(n.ID, nil, nil, 0); err != nil {
		t.Fatal(err)
	}
	a.nodeSilent(t, n.ID, now.Add(-time.Hour))

	if err := a.evaluateAlerts(ctx); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(fake.all(), "\n")
	for _, want := range []string{"full", "95%", "soon", "2 day(s)", "gone", "expired", "de-1"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in alerts:\n%s", want, joined)
		}
	}
	for _, unwanted := range []string{"fine", "off"} {
		if strings.Contains(joined, "<b>"+unwanted+"</b>") {
			t.Errorf("unexpected alert for %q", unwanted)
		}
	}
}

func TestIngestNodeIsIdempotent(t *testing.T) {
	a, _ := newTestApp(t)
	n, _, _ := a.Store.CreateNode("fr-1")
	m := time.Now().Unix() / 60
	b := model.Batch{ID: "x1", Recs: []model.Rec{{M: m, U: "bob", IP: "4.4.4.4", D: "x.com", N: 5}}, Traffic: []model.TrafficRec{{T: time.Now().Unix(), U: "bob", Up: 10, Down: 20}}}
	for i := 0; i < 3; i++ {
		if err := a.IngestNode(context.Background(), n, b, "7.7.7.7"); err != nil {
			t.Fatal(err)
		}
	}
	h := time.Now().Unix() / 3600
	pts, _ := a.Store.TrafficHourly(context.Background(), 0, h-1, h+1)
	var down int64
	for _, p := range pts {
		down += p.Down
	}
	if down != 20 {
		t.Fatalf("a retried batch was counted %d times", down/20)
	}
	if _, ok := a.Live.OnlineUser(1, time.Minute); !ok {
		t.Fatal("node traffic did not reach the live view")
	}
}

func TestStripTagsAndBytes(t *testing.T) {
	if got := stripTags("<b>a &amp; b</b> &lt;x&gt;"); got != "a & b <x>" {
		t.Fatalf("stripTags = %q", got)
	}
	if humanBytes(1536) != "1.5 KiB" || humanBytes(10) != "10 B" {
		t.Fatalf("humanBytes: %s %s", humanBytes(1536), humanBytes(10))
	}
}

// nodeSilent rewinds a node's last contact for the test.
func (a *App) nodeSilent(t *testing.T, id int64, at time.Time) {
	t.Helper()
	if err := a.Store.SetNodeLastSeen(id, at.Unix()); err != nil {
		t.Fatal(err)
	}
}

var _ = store.Node{}

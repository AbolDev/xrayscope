package xraystats

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

func statMsg(name string, value uint64) []byte {
	stat := []byte{0x0a}
	stat = appendVarint(stat, uint64(len(name)))
	stat = append(stat, name...)
	stat = append(stat, 0x10)
	stat = appendVarint(stat, value)
	out := []byte{0x0a}
	out = appendVarint(out, uint64(len(stat)))
	return append(out, stat...)
}

// fakeXray serves StatsService/QueryStats over h2c like Xray does.
func fakeXray(t *testing.T, stats map[string]uint64, status string) string {
	t.Helper()
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != method || r.Header.Get("Content-Type") != "application/grpc" {
			http.Error(w, "bad request", 400)
			return
		}
		body, _ := io.ReadAll(r.Body)
		if len(body) < 5 || !strings.Contains(string(body[5:]), "user>>>") {
			http.Error(w, "missing pattern", 400)
			return
		}
		w.Header().Set("Content-Type", "application/grpc")
		w.Header().Set("Trailer", "Grpc-Status, Grpc-Message")
		var msg []byte
		for n, v := range stats {
			msg = append(msg, statMsg(n, v)...)
		}
		out := make([]byte, 5+len(msg))
		binary.BigEndian.PutUint32(out[1:5], uint32(len(msg)))
		copy(out[5:], msg)
		w.Write(out)
		w.Header().Set("Grpc-Status", status)
		if status != "0" {
			w.Header().Set("Grpc-Message", "boom")
		}
	})
	srv := httptest.NewServer(h2c.NewHandler(h, &http2.Server{}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}

func TestClientQuery(t *testing.T) {
	addr := fakeXray(t, map[string]uint64{
		"user>>>alice@x>>>traffic>>>uplink":   1000,
		"user>>>alice@x>>>traffic>>>downlink": 5000,
		"user>>>bob>>>traffic>>>downlink":     7,
		"inbound>>>api>>>traffic>>>uplink":    99, // not a user counter
	}, "0")
	got, err := NewClient(addr).Query(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got["alice@x"] != [2]int64{1000, 5000} || got["bob"] != [2]int64{0, 7} || len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
}

func TestClientReportsGRPCError(t *testing.T) {
	addr := fakeXray(t, nil, "13")
	if _, err := NewClient(addr).Query(context.Background()); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v", err)
	}
	if _, err := NewClient("127.0.0.1:1").Query(context.Background()); err == nil {
		t.Fatal("unreachable API must fail")
	}
}

type seq struct {
	steps []map[string][2]int64
	err   error
	i     int
}

func (s *seq) Query(context.Context) (map[string][2]int64, error) {
	if s.err != nil {
		return nil, s.err
	}
	v := s.steps[s.i]
	if s.i < len(s.steps)-1 {
		s.i++
	}
	return v, nil
}

func TestTrackerSpeeds(t *testing.T) {
	q := &seq{steps: []map[string][2]int64{
		{"a": {0, 0}, "b": {100, 100}},
		{"a": {2000, 10000}, "b": {100, 100}}, // a moved, b idle
		{"a": {0, 50}, "b": {100, 100}},       // counters reset: no negative speed
		{"a": {1000, 50}, "b": {100, 100}},
	}}
	tr := NewTracker(q)
	now := time.Unix(1000, 0)
	ctx := context.Background()

	tr.Sample(ctx, now) // baseline only
	if len(tr.Rates()) != 0 {
		t.Fatal("first sample must only set the baseline")
	}
	tr.Sample(ctx, now.Add(2*time.Second))
	r := tr.Rates()
	if r["a"] != (Rate{Up: 1000, Down: 5000}) {
		t.Fatalf("a = %+v", r["a"])
	}
	if _, ok := r["b"]; ok {
		t.Fatal("an idle user must not appear")
	}
	if tot := tr.Total(); tot.Up != 1000 || tot.Down != 5000 {
		t.Fatalf("total = %+v", tot)
	}
	tr.Sample(ctx, now.Add(4*time.Second))
	if len(tr.Rates()) != 0 {
		t.Fatalf("reset counters produced a rate: %+v", tr.Rates())
	}
	tr.Sample(ctx, now.Add(6*time.Second))
	if tr.Rates()["a"].Up != 500 {
		t.Fatalf("after reset: %+v", tr.Rates())
	}
	if h := tr.History("a"); len(h) != 3 || h[0].Down != 5000 || h[1].Down != 0 || h[2].Up != 500 {
		t.Fatalf("history = %+v", h)
	}
	if top := tr.Top(5); len(top) != 1 || top[0] != "a" {
		t.Fatalf("top = %v", top)
	}
}

func TestTrackerError(t *testing.T) {
	tr := NewTracker(&seq{err: errors.New("down")})
	tr.Sample(context.Background(), time.Now())
	if _, err := tr.Status(); err == nil || len(tr.Rates()) != 0 {
		t.Fatal("an unreachable API must clear the rates and report the error")
	}
}

func TestFindAPI(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "config.json")
	os.WriteFile(good, []byte(`{"inbounds":[{"tag":"in-80","port":80},{"tag":"api","listen":"127.0.0.1","port":62789}]}`), 0o600)
	addr, ok := FindAPI(filepath.Join(dir, "missing.json"), good)
	if !ok || addr != "127.0.0.1:62789" {
		t.Fatalf("addr=%q ok=%v", addr, ok)
	}
	os.WriteFile(good, []byte(`{"inbounds":[{"tag":"api","port":"1234"}]}`), 0o600)
	if addr, ok := FindAPI(good); !ok || addr != "127.0.0.1:1234" {
		t.Fatalf("string port: %q %v", addr, ok)
	}
	os.WriteFile(good, []byte(`{"inbounds":[{"tag":"x","port":1}]}`), 0o600)
	if _, ok := FindAPI(good); ok {
		t.Fatal("no api inbound must report not found")
	}
}

package app

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"time"

	"xrayscope/internal/model"
	"xrayscope/internal/netutil"
	"xrayscope/internal/store"
	"xrayscope/internal/xraystats"
)

// ---------------------------------------------------------------- node side

// RecordOutbox queues a batch for a main server that pulls from this node.
// It does nothing until a main server has asked for data.
func (a *App) RecordOutbox(b model.Batch) {
	if !a.Store.OutboxOn() {
		return
	}
	b.Version, b.Node = a.Version, a.Cfg.NodeName
	if a.Panel != nil {
		b.Panel = a.Panel.Name()
	}
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestSpeed)
	if err := json.NewEncoder(zw).Encode(&b); err != nil {
		return
	}
	if zw.Close() != nil {
		return
	}
	if err := a.Store.PushOutbox(buf.Bytes()); err != nil {
		a.Log.Warn("outbox", "err", err)
	}
}

// PulledBatch is one entry of a pull response.
type PulledBatch struct {
	Seq   int64           `json:"seq"`
	Batch json.RawMessage `json:"batch"`
}

// PullResponse is what a node returns to its main server.
type PullResponse struct {
	Version string        `json:"version"`
	Panel   string        `json:"panel"`
	Last    int64         `json:"last"`
	Batches []PulledBatch `json:"batches"`
}

// ServePull acknowledges what the main server has stored and returns the
// next batches.
func (a *App) ServePull(ctx context.Context, after int64, limit int) (*PullResponse, error) {
	if err := a.Store.EnableOutbox(); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	items, last, err := a.Store.PullOutbox(ctx, after, limit, 8<<20)
	if err != nil {
		return nil, err
	}
	res := &PullResponse{Version: a.Version, Last: last, Batches: []PulledBatch{}}
	if a.Panel != nil {
		res.Panel = a.Panel.Name()
	}
	for _, it := range items {
		zr, err := gzip.NewReader(bytes.NewReader(it.Body))
		if err != nil {
			continue
		}
		raw, err := io.ReadAll(io.LimitReader(zr, 128<<20))
		zr.Close()
		if err != nil {
			continue
		}
		res.Batches = append(res.Batches, PulledBatch{Seq: it.Seq, Batch: raw})
	}
	return res, nil
}

// ---------------------------------------------------------------- main side

// pullEvery is how often the main server collects from a node.
const defaultPullEvery = 5 * time.Second

// runPull keeps one worker per pull node.
func (a *App) runPull(ctx context.Context) {
	workers := map[int64]context.CancelFunc{}
	var wg sync.WaitGroup
	defer func() {
		for _, cancel := range workers {
			cancel()
		}
		wg.Wait()
	}()
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		nodes, err := a.Store.Nodes(ctx)
		if err == nil {
			live := map[int64]bool{}
			for _, n := range nodes {
				if n.Mode != "pull" {
					continue
				}
				live[n.ID] = true
				if _, ok := workers[n.ID]; !ok {
					wctx, cancel := context.WithCancel(ctx)
					workers[n.ID] = cancel
					wg.Add(1)
					go func(n store.Node) {
						defer wg.Done()
						a.pullWorker(wctx, n)
					}(n)
				}
			}
			for id, cancel := range workers {
				if !live[id] {
					cancel()
					delete(workers, id)
					a.SpeedBook.Drop(fmt.Sprintf("node:%d", id))
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// nodeClient is an authenticated HTTP session with a node's panel.
type nodeClient struct {
	base string
	user string
	pass string
	http *http.Client
	in   bool
}

func newNodeClient(n store.PullNode) (*nodeClient, error) {
	hc, err := netutil.NewClient(netutil.Options{Proxy: n.Proxy, Timeout: 30 * time.Second, InsecureTLS: n.Insecure})
	if err != nil {
		return nil, err
	}
	jar, _ := cookiejar.New(nil)
	hc.Jar = jar
	return &nodeClient{base: strings.TrimRight(n.URL, "/"), user: n.Username, pass: n.Password, http: hc}, nil
}

func (c *nodeClient) login(ctx context.Context) error {
	body, _ := json.Marshal(map[string]string{"username": c.user, "password": c.pass})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/api/login", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-XS", "1")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("login refused (HTTP %d): %s", resp.StatusCode, apiMessage(msg))
	}
	c.in = true
	return nil
}

func apiMessage(b []byte) string {
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(b, &e) == nil && e.Error != "" {
		return e.Error
	}
	s := strings.TrimSpace(string(b))
	if len(s) > 120 {
		s = s[:120]
	}
	return s
}

// get calls a node API path and decodes the JSON answer, signing in first
// (and again when the session has expired).
func (c *nodeClient) get(ctx context.Context, path string, v any) error {
	for attempt := 0; attempt < 2; attempt++ {
		if !c.in {
			if err := c.login(ctx); err != nil {
				return err
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
		if err != nil {
			return err
		}
		resp, err := c.http.Do(req)
		if err != nil {
			return err
		}
		data, rerr := io.ReadAll(io.LimitReader(resp.Body, 256<<20))
		resp.Body.Close()
		if rerr != nil {
			return rerr
		}
		switch {
		case resp.StatusCode == http.StatusUnauthorized:
			c.in = false
			continue
		case resp.StatusCode == http.StatusNotFound:
			return errors.New("the node does not support pulling; update XrayScope on it")
		case resp.StatusCode != http.StatusOK:
			return fmt.Errorf("HTTP %d: %s", resp.StatusCode, apiMessage(data))
		}
		return json.Unmarshal(data, v)
	}
	return errors.New("the node refused the credentials")
}

// TestPullNode checks that a node can be reached and signed in to.
func (a *App) TestPullNode(ctx context.Context, n store.PullNode) (PullInfo, error) {
	c, err := newNodeClient(n)
	if err != nil {
		return PullInfo{}, err
	}
	var info PullInfo
	err = c.get(ctx, "/api/node/info", &info)
	return info, err
}

// PullInfo describes a node.
type PullInfo struct {
	Version string `json:"version"`
	Panel   string `json:"panel"`
	Role    string `json:"role"`
}

func (a *App) pullInterval() time.Duration {
	if a.PullInterval > 0 {
		return a.PullInterval
	}
	return defaultPullEvery
}

// pullWorker collects data and live speeds from one node until ctx ends.
func (a *App) pullWorker(ctx context.Context, n store.Node) {
	pw, err := a.Store.NodePassword(ctx, n.ID)
	if err != nil {
		return
	}
	cfg := store.PullNode{Name: n.Name, URL: n.URL, Username: n.Username, Password: pw, Proxy: n.Proxy, Insecure: n.Insecure}
	client, err := newNodeClient(cfg)
	if err != nil {
		_ = a.Store.SetNodeError(n.ID, err.Error())
		return
	}
	host := hostOf(n.URL)
	seq := n.PullSeq
	lastErr := n.LastError
	report := func(err error) {
		msg := ""
		if err != nil {
			msg = err.Error()
		}
		if msg != lastErr {
			lastErr = msg
			_ = a.Store.SetNodeError(n.ID, msg)
			if err != nil {
				a.Log.Warn("pull node", "node", n.Name, "err", err)
			}
		}
	}
	source := fmt.Sprintf("node:%d", n.ID)
	defer a.SpeedBook.Drop(source)

	pullTick := time.NewTicker(a.pullInterval())
	defer pullTick.Stop()
	speedEvery := time.Second
	if a.pullInterval() < speedEvery {
		speedEvery = a.pullInterval()
	}
	speedTick := time.NewTicker(speedEvery)
	defer speedTick.Stop()

	pull := func() {
		for round := 0; round < 20 && ctx.Err() == nil; round++ {
			var res PullResponse
			if err := client.get(ctx, fmt.Sprintf("/api/node/pull?after=%d&limit=20", seq), &res); err != nil {
				report(err)
				return
			}
			if res.Last < seq && len(res.Batches) == 0 {
				seq = 0 // the node's database was replaced; start over
				_ = a.Store.SetNodePullSeq(n.ID, 0)
				return
			}
			_ = a.Store.TouchNode(n.ID, host, res.Version, res.Panel)
			for _, pb := range res.Batches {
				var b model.Batch
				if err := json.Unmarshal(pb.Batch, &b); err != nil {
					seq = pb.Seq
					continue
				}
				b.ID = fmt.Sprintf("pull-%d-%d", n.ID, pb.Seq)
				if err := a.IngestNode(ctx, n, b, host); err != nil {
					report(err)
					return
				}
				seq = pb.Seq
				_ = a.Store.SetNodePullSeq(n.ID, seq)
			}
			report(nil)
			if len(res.Batches) < 20 {
				return
			}
		}
	}
	speed := func() {
		var sp struct {
			Speeds map[string][2]int64 `json:"speeds"`
		}
		ac := a.Settings.Get().Accuracy
		if err := client.get(ctx, fmt.Sprintf("/api/node/speed?active_kb=%d&heavy_kb=%d", ac.ActiveKB, ac.HeavyKB), &sp); err != nil {
			return
		}
		rates := make(map[string]xraystats.Rate, len(sp.Speeds))
		for e, v := range sp.Speeds {
			rates[e] = xraystats.Rate{Up: v[0], Down: v[1]}
		}
		a.SpeedBook.Set(source, rates)
	}

	pull()
	for {
		select {
		case <-ctx.Done():
			return
		case <-pullTick.C:
			pull()
		case <-speedTick.C:
			speed()
		}
	}
}

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	return u.Hostname()
}

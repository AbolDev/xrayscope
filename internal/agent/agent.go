// Package agent runs the node role: it follows the local access log and the
// local panel, spools batches on disk and ships them to the main server.
package agent

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"xrayscope/internal/collect"
	"xrayscope/internal/config"
	"xrayscope/internal/model"
	"xrayscope/internal/netutil"
	"xrayscope/internal/panel"
)

const maxSpoolBytes = 2 << 30

// Agent is a node collector.
type Agent struct {
	cfg     config.Config
	version string
	log     *slog.Logger
	panel   panel.Panel
	client  *http.Client
	logLoc  *time.Location

	spoolDir  string
	statePath string

	mu    sync.Mutex
	state state
	seq   int64
}

type state struct {
	Path    string              `json:"path"`
	Pos     collect.Position    `json:"pos"`
	Traffic map[string][2]int64 `json:"traffic"`
}

// New prepares an agent.
func New(cfg config.Config, version string, log *slog.Logger) (*Agent, error) {
	if log == nil {
		log = slog.Default()
	}
	loc, err := cfg.LogLocation()
	if err != nil {
		return nil, fmt.Errorf("log_timezone: %w", err)
	}
	proxyURL := cfg.ServerProxy
	if proxyURL == "" {
		proxyURL = cfg.Proxy
	}
	client, err := netutil.NewClient(netutil.Options{
		Proxy: proxyURL, Timeout: 60 * time.Second, InsecureTLS: cfg.InsecureTLS, ServerName: cfg.ServerHostname,
	})
	if err != nil {
		return nil, err
	}
	p, err := panel.New(cfg.Panel.Type, cfg.Panel.DBPath)
	if err != nil {
		return nil, err
	}
	a := &Agent{
		cfg: cfg, version: version, log: log, panel: p, client: client, logLoc: loc,
		spoolDir:  filepath.Join(cfg.DataDir, "spool"),
		statePath: filepath.Join(cfg.DataDir, "agent-state.json"),
	}
	if err := os.MkdirAll(filepath.Join(a.spoolDir, "bad"), 0o750); err != nil {
		return nil, err
	}
	if b, err := os.ReadFile(a.statePath); err == nil {
		_ = json.Unmarshal(b, &a.state)
	}
	if a.state.Traffic == nil {
		a.state.Traffic = map[string][2]int64{}
	}
	return a, nil
}

// Run blocks until ctx ends.
func (a *Agent) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for name, f := range map[string]func(context.Context){
		"tail": a.runTail, "traffic": a.runTraffic, "send": a.runSend,
	} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					a.log.Error("agent job crashed", "job", name, "panic", r)
				}
			}()
			f(ctx)
		}()
	}
	wg.Wait()
}

func (a *Agent) panelName() string {
	if a.panel == nil {
		return ""
	}
	return a.panel.Name()
}

// ---------------------------------------------------------------- state and spool

func (a *Agent) saveState() error {
	b, err := json.Marshal(a.state)
	if err != nil {
		return err
	}
	tmp := a.statePath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, a.statePath)
}

// spool writes a batch to disk.
func (a *Agent) spool(b model.Batch) error {
	a.mu.Lock()
	a.seq++
	seq := a.seq
	a.mu.Unlock()
	b.Node, b.Version, b.Panel = a.cfg.NodeName, a.version, a.panelName()
	name := fmt.Sprintf("%020d-%06d", time.Now().UnixNano(), seq%1000000)
	b.ID = a.cfg.NodeName + "-" + name
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestSpeed)
	if err := json.NewEncoder(zw).Encode(&b); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	final := filepath.Join(a.spoolDir, name+".json.gz")
	if err := os.WriteFile(final+".part", buf.Bytes(), 0o600); err != nil {
		return err
	}
	return os.Rename(final+".part", final)
}

func (a *Agent) spoolFiles() []string {
	ents, err := os.ReadDir(a.spoolDir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json.gz") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// enforceSpoolLimit drops the oldest files when the spool grows too large.
func (a *Agent) enforceSpoolLimit() {
	files := a.spoolFiles()
	var total int64
	sizes := make([]int64, len(files))
	for i, f := range files {
		if st, err := os.Stat(filepath.Join(a.spoolDir, f)); err == nil {
			sizes[i] = st.Size()
			total += st.Size()
		}
	}
	for i := 0; total > maxSpoolBytes && i < len(files); i++ {
		if os.Remove(filepath.Join(a.spoolDir, files[i])) == nil {
			total -= sizes[i]
			a.log.Warn("spool is full, dropped the oldest batch", "file", files[i])
		}
	}
}

// ---------------------------------------------------------------- log tailing

func (a *Agent) resolveLog(ctx context.Context) string {
	if p := a.cfg.LogPath; p != "" && p != "auto" {
		return p
	}
	if a.panel == nil {
		return ""
	}
	info, err := a.panel.AccessLog(ctx)
	if err != nil {
		a.log.Warn("cannot read the panel configuration", "err", err)
		return ""
	}
	if info.Disabled {
		a.log.Warn("access logging is disabled in the panel; enable it to collect data")
		return ""
	}
	if info.Path == "" {
		if filepath.IsAbs(info.Setting) {
			return info.Setting
		}
		a.log.Warn("access log not found", "setting", info.Setting)
	}
	return info.Path
}

func (a *Agent) runTail(ctx context.Context) {
	for ctx.Err() == nil {
		path := a.resolveLog(ctx)
		if path == "" {
			sleep(ctx, 30*time.Second)
			continue
		}
		child, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() {
			defer close(done)
			a.tail(child, path)
		}()
		t := time.NewTicker(time.Minute)
	wait:
		for {
			select {
			case <-done:
				sleep(ctx, 10*time.Second)
				break wait
			case <-t.C:
				if next := a.resolveLog(ctx); next != "" && next != path {
					cancel()
					<-done
					break wait
				}
			case <-ctx.Done():
				cancel()
				<-done
				break wait
			}
		}
		t.Stop()
		cancel()
	}
}

func (a *Agent) tail(ctx context.Context, path string) {
	a.mu.Lock()
	var start collect.Position
	if a.state.Path == path {
		start = a.state.Pos
	}
	a.mu.Unlock()
	a.log.Info("following access log", "path", path, "offset", start.Offset)
	var lastWarn time.Time
	err := collect.Run(ctx, collect.PipelineConfig{
		Tailer: collect.Tailer{
			Path: path, Loc: a.logLoc, Interval: time.Second, Start: start,
			Warn: func(err error) {
				if time.Since(lastWarn) > time.Minute {
					lastWarn = time.Now()
					a.log.Warn("access log", "err", err)
				}
			},
		},
		Sink:       sink{a: a, path: path},
		FlushEvery: 10 * time.Second,
	})
	if err != nil && ctx.Err() == nil {
		a.log.Error("access log collection stopped", "err", err)
	}
}

type sink struct {
	a    *Agent
	path string
}

func (s sink) Flush(recs []model.Rec, pos collect.Position, lines int64) error {
	a := s.a
	if len(recs) > 0 || lines > 0 {
		if err := a.spool(model.Batch{Recs: recs, Lines: lines}); err != nil {
			return err
		}
	}
	a.mu.Lock()
	a.state.Path, a.state.Pos = s.path, pos
	err := a.saveState()
	a.mu.Unlock()
	return err
}

// ---------------------------------------------------------------- panel traffic

func (a *Agent) runTraffic(ctx context.Context) {
	if a.panel == nil {
		return
	}
	a.mu.Lock()
	last := make(map[string][2]int64, len(a.state.Traffic))
	for k, v := range a.state.Traffic {
		last[k] = v
	}
	a.mu.Unlock()
	var lastWarn time.Time
	p := &collect.TrafficPoller{
		Panel:    a.panel,
		Interval: time.Duration(a.cfg.Panel.PollSeconds) * time.Second,
		Last:     last,
		Deliver: func(deltas []model.TrafficRec, totals map[string][2]int64, users []model.PanelUser) error {
			if err := a.spool(model.Batch{Traffic: deltas, Users: users}); err != nil {
				return err
			}
			a.mu.Lock()
			for e, t := range totals {
				a.state.Traffic[e] = t
			}
			err := a.saveState()
			a.mu.Unlock()
			return err
		},
		Warn: func(err error) {
			if time.Since(lastWarn) > 5*time.Minute {
				lastWarn = time.Now()
				a.log.Warn("panel poll failed", "err", err)
			}
		},
	}
	p.Run(ctx)
}

// ---------------------------------------------------------------- sending

func (a *Agent) runSend(ctx context.Context) {
	backoff := 2 * time.Second
	for ctx.Err() == nil {
		a.enforceSpoolLimit()
		files := a.spoolFiles()
		if len(files) == 0 {
			sleep(ctx, 3*time.Second)
			continue
		}
		var sent int
		for _, f := range files {
			path := filepath.Join(a.spoolDir, f)
			err := a.send(ctx, path)
			var perm *permanentError
			switch {
			case err == nil:
				_ = os.Remove(path)
				sent++
				backoff = 2 * time.Second
				continue
			case errors.As(err, &perm):
				a.log.Error("server rejected a batch, moving it aside", "file", f, "err", err)
				_ = os.Rename(path, filepath.Join(a.spoolDir, "bad", f))
				continue
			case ctx.Err() != nil:
				return
			default:
				a.log.Warn("cannot reach the main server", "err", err, "retry_in", backoff.String(), "queued", len(files)-sent)
				sleep(ctx, backoff)
				if backoff < time.Minute {
					backoff *= 2
				}
			}
			break
		}
		if sent > 0 {
			sleep(ctx, 500*time.Millisecond)
		}
	}
}

type permanentError struct{ msg string }

func (e *permanentError) Error() string { return e.msg }

func (a *Agent) send(ctx context.Context, path string) error {
	body, err := os.ReadFile(path)
	if err != nil {
		return &permanentError{err.Error()}
	}
	url := strings.TrimRight(a.cfg.ServerURL, "/") + "/api/ingest"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return &permanentError{err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	req.Header.Set("Authorization", "Bearer "+a.cfg.Token)
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	switch {
	case resp.StatusCode/100 == 2:
		return nil
	case resp.StatusCode == http.StatusBadRequest:
		return &permanentError{fmt.Sprintf("HTTP 400: %s", strings.TrimSpace(string(msg)))}
	case resp.StatusCode == http.StatusUnauthorized:
		return fmt.Errorf("the server rejected the token (HTTP 401); check token in the configuration")
	default:
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

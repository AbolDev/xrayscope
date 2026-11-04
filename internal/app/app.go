// Package app wires the collector, storage, analytics, alerts, Telegram bot
// and background jobs of a server instance.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"xrayscope/internal/auth"
	"xrayscope/internal/backup"
	"xrayscope/internal/classify"
	"xrayscope/internal/collect"
	"xrayscope/internal/config"
	"xrayscope/internal/geo"
	"xrayscope/internal/health"
	"xrayscope/internal/live"
	"xrayscope/internal/model"
	"xrayscope/internal/panel"
	"xrayscope/internal/parser"
	"xrayscope/internal/report"
	"xrayscope/internal/settings"
	"xrayscope/internal/store"
	"xrayscope/internal/telegram"
	"xrayscope/internal/xraystats"
)

// LogState describes the access-log collection.
type LogState struct {
	State   string `json:"state"` // ok, waiting, disabled, missing, no_panel
	Path    string `json:"path"`
	Message string `json:"message,omitempty"`
}

// PanelState describes the connection to the VPN panel.
type PanelState struct {
	Type   string `json:"type"`
	OK     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
	LastOK int64  `json:"last_ok"`
	Users  int    `json:"users"`
}

// App is a running server instance.
type App struct {
	Cfg      config.Config
	Version  string
	Store    *store.Store
	Settings *settings.Manager
	Auth     *auth.Auth
	Live     *live.Tracker
	Bot      *telegram.Bot
	Geo      *geo.Resolver
	Panel    panel.Panel
	Log      *slog.Logger
	Started  time.Time

	// InitialPassword is set when the administrator account was just created.
	InitialPassword string

	logLoc *time.Location

	mu         sync.RWMutex
	logState   LogState
	panelState PanelState

	lastLineAt atomic.Int64
	linesTotal atomic.Int64
	onlineAPI  atomic.Int32

	alerts alertState

	// Speed samples Xray's statistics API; SpeedBook adds remote nodes.
	Speed     *xraystats.Tracker
	SpeedSrc  *xraystats.Source
	SpeedBook *SpeedBook

	// Health watches the log for configuration problems; ClockFix corrects
	// a log written in another time zone.
	Health   *health.Monitor
	ClockFix *health.ClockFixer

	// PullInterval overrides how often pull nodes are collected (tests).
	PullInterval time.Duration

	// RestartFn is called to restart the process (set by main).
	RestartFn func()
}

// New opens the database and prepares every component. Call Run to start
// the background work.
func New(cfg config.Config, version string, log *slog.Logger) (*App, error) {
	if log == nil {
		log = slog.Default()
	}
	logLoc, err := cfg.LogLocation()
	if err != nil {
		return nil, fmt.Errorf("log_timezone: %w", err)
	}
	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return nil, err
	}
	restored, err := backup.ApplyPending(cfg.DataDir, cfg.DBFile(), cfg.BackupDir())
	if err != nil {
		return nil, fmt.Errorf("apply restore: %w", err)
	}
	if restored {
		log.Info("restored database from staged backup")
	}
	st, err := store.Open(cfg.DBFile(), nil)
	if err != nil {
		return nil, err
	}
	a := &App{Cfg: cfg, Version: version, Store: st, Log: log, Started: time.Now(), logLoc: logLoc, Live: live.New()}
	a.alerts = newAlertState()

	if a.Settings, err = settings.NewManager(st); err != nil {
		st.Close()
		return nil, err
	}
	if a.Auth, err = auth.New(st); err != nil {
		st.Close()
		return nil, err
	}
	if !a.Auth.HasAdmin() {
		pw := auth.GeneratePassword(14)
		if err := a.Auth.SetAdmin("admin", pw); err != nil {
			st.Close()
			return nil, err
		}
		a.InitialPassword = pw
	}
	if a.Geo, err = geo.Open(cfg.GeoDB, cfg.GeoASNDB); err != nil {
		log.Warn("geo database not usable", "err", err)
	}
	if a.Geo != nil {
		st.SetGeo(a.Geo.Lookup)
	}
	// an address change on a mobile network is the same device
	a.Live.SameNet = func(x, y string) bool {
		var asnX, asnY int
		if a.Geo != nil {
			_, asnX, _ = a.Geo.Lookup(x)
			_, asnY, _ = a.Geo.Lookup(y)
		}
		return report.SameNetworkAddr(x, asnX, y, asnY)
	}
	if a.Panel, err = panel.New(cfg.Panel.Type, cfg.Panel.DBPath); err != nil {
		st.Close()
		return nil, err
	}

	a.SpeedSrc = &xraystats.Source{Override: cfg.XrayAPI, Find: a.apiFinder}
	a.Speed = xraystats.NewTracker(a.SpeedSrc)
	a.SpeedBook = NewSpeedBook()
	a.Health = health.New()
	shift := 0
	if v, ok, _ := st.KV("log:shift"); ok {
		shift, _ = strconv.Atoi(v)
	}
	a.ClockFix = health.NewClockFixer(a.Health, shift, func(min int) {
		_ = st.SetKV("log:shift", strconv.Itoa(min))
		log.Warn("access log time zone corrected", "shift_minutes", min)
	})

	a.Bot = telegram.New(cfg.Proxy)
	apply := func(s settings.Settings) {
		st.SetClassifier(classify.New(s.CustomRules))
		if err := a.Bot.Configure(s.Telegram); err != nil {
			log.Warn("telegram configuration", "err", err)
		}
	}
	apply(a.Settings.Get())
	a.Settings.OnChange(apply)
	a.preloadLive()
	return a, nil
}

// Close releases resources.
func (a *App) Close() {
	a.Geo.Close()
	a.Store.Close()
}

// Env returns the report environment for the current settings.
func (a *App) Env() *report.Env {
	s := a.Settings.Get()
	loc, err := s.Location()
	if err != nil {
		loc = time.UTC
	}
	return &report.Env{
		Store:     a.Store,
		Loc:       loc,
		GapMin:    int64(s.SessionGapMin),
		OnlineWin: time.Duration(s.OnlineWindowMin) * time.Minute,
		Live:      a.Live,
	}
}

// preloadLive rebuilds the online view from the last two hours of data.
func (a *App) preloadLive() {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	now := time.Now().Unix()
	rows, err := a.Store.Act(ctx, 0, now/3600-2, now/3600+1)
	if err != nil || len(rows) == 0 {
		return
	}
	ids := map[int64]bool{}
	for _, r := range rows {
		ids[r.IPID] = true
	}
	list := make([]int64, 0, len(ids))
	for id := range ids {
		list = append(list, id)
	}
	ips, _ := a.Store.IPsByID(ctx, list)
	users, _ := a.Store.Users(ctx)
	email := map[int64]string{}
	for _, u := range users {
		email[u.ID] = u.Email
	}
	var obs []store.Obs
	for _, r := range rows {
		last := int64(-1)
		for b := 59; b >= 0; b-- {
			if r.Mask&(1<<uint(b)) != 0 {
				last = r.Hour*60 + int64(b)
				break
			}
		}
		if last > 0 {
			obs = append(obs, store.Obs{UserID: r.UserID, Email: email[r.UserID], IP: ips[r.IPID].IP, Minute: last})
		}
	}
	a.Live.Observe(obs)
}

// Run starts the background jobs and blocks until ctx ends.
func (a *App) Run(ctx context.Context) {
	var wg sync.WaitGroup
	run := func(name string, f func(context.Context)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					a.Log.Error("job crashed", "job", name, "panic", r)
				}
			}()
			f(ctx)
		}()
	}
	run("log", a.runLog)
	if a.Panel != nil {
		run("panel", a.runPanel)
	} else {
		a.setPanelState(PanelState{Type: "none"})
	}
	run("speed", a.runSpeed)
	run("pull", a.runPull)
	run("online", a.runOnline)
	run("maintenance", a.runMaintenance)
	run("bot", func(ctx context.Context) { a.Bot.Poll(ctx, a.botHandler) })
	wg.Wait()
}

// ---------------------------------------------------------------- access log

func (a *App) setLogState(s LogState) {
	a.mu.Lock()
	a.logState = s
	a.mu.Unlock()
}

// LogState returns the collection state.
func (a *App) LogState() LogState {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.logState
}

func (a *App) setPanelState(s PanelState) {
	a.mu.Lock()
	a.panelState = s
	a.mu.Unlock()
}

// PanelState returns the panel connection state.
func (a *App) PanelState() PanelState {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.panelState
}

// LastLineAt returns when a log line was last received (zero if never).
func (a *App) LastLineAt() time.Time {
	if v := a.lastLineAt.Load(); v > 0 {
		return time.Unix(v, 0)
	}
	return time.Time{}
}

// LinesTotal is the number of log lines read since start.
func (a *App) LinesTotal() int64 { return a.linesTotal.Load() }

func (a *App) resolveLog(ctx context.Context) LogState {
	if p := a.Cfg.LogPath; p != "" && p != "auto" {
		return LogState{State: "waiting", Path: p}
	}
	if a.Panel == nil {
		return LogState{State: "no_panel", Message: "Set log_path in the configuration: no panel is configured."}
	}
	info, err := a.Panel.AccessLog(ctx)
	switch {
	case err != nil:
		return LogState{State: "missing", Message: "Cannot read the panel database: " + err.Error()}
	case info.Disabled:
		return LogState{State: "disabled", Message: "Access logging is turned off in the panel. Set the Xray log access path (for example /var/log/xray/access.log) in the panel's Xray settings and restart Xray."}
	case info.Path == "":
		msg := "The panel's access log path (" + info.Setting + ") does not exist yet."
		return LogState{State: "missing", Path: info.Setting, Message: msg}
	}
	return LogState{State: "waiting", Path: info.Path}
}

func (a *App) runLog(ctx context.Context) {
	for ctx.Err() == nil {
		st := a.resolveLog(ctx)
		a.setLogState(st)
		if st.Path == "" || st.State == "missing" && !filepath.IsAbs(st.Path) {
			sleepCtx(ctx, 30*time.Second)
			continue
		}
		child, cancel := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { done <- a.tail(child, st.Path) }()
		ticker := time.NewTicker(time.Minute)
	wait:
		for {
			select {
			case err := <-done:
				if err != nil && ctx.Err() == nil {
					a.Log.Error("access log collection stopped", "err", err)
					sleepCtx(ctx, 10*time.Second)
				}
				break wait
			case <-ticker.C:
				if next := a.resolveLog(ctx); next.Path != st.Path && next.Path != "" {
					a.Log.Info("access log path changed", "from", st.Path, "to", next.Path)
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
		ticker.Stop()
		cancel()
	}
}

func (a *App) tail(ctx context.Context, path string) error {
	var start collect.Position
	if p, ok, _ := a.Store.KV("tail:path"); ok && p == path {
		if v, ok, _ := a.Store.KV("tail:pos"); ok {
			start = collect.DecodePosition(v)
		}
	}
	a.Log.Info("following access log", "path", path, "offset", start.Offset)
	var lastWarn time.Time
	return collect.Run(ctx, collect.PipelineConfig{
		Tailer: collect.Tailer{
			Path: path, Loc: a.logLoc, Interval: time.Second, Start: start,
			Warn: func(err error) {
				if time.Since(lastWarn) > time.Minute {
					lastWarn = time.Now()
					a.setLogState(LogState{State: "waiting", Path: path, Message: err.Error()})
					a.Log.Warn("access log", "err", err)
				}
			},
		},
		Sink:    localSink{a: a, path: path},
		OnEntry: func(e *parser.Entry) { a.ClockFix.Fix(e, time.Now()) },
	})
}

type localSink struct {
	a    *App
	path string
}

func (s localSink) Flush(recs []model.Rec, pos collect.Position, lines int64) error {
	a := s.a
	obs, err := a.Store.ApplyBatch(0, recs, map[string]string{
		"tail:pos":  collect.EncodePosition(pos),
		"tail:path": s.path,
	}, lines)
	if err != nil {
		return err
	}
	a.Live.Observe(obs)
	a.RecordOutbox(model.Batch{ID: fmt.Sprintf("%d", time.Now().UnixNano()), Recs: recs, Lines: lines})
	if lines > 0 {
		a.lastLineAt.Store(time.Now().Unix())
		a.linesTotal.Add(lines)
		if st := a.LogState(); st.State != "ok" || st.Message != "" {
			a.setLogState(LogState{State: "ok", Path: s.path})
		}
	}
	a.onObs(obs)
	return nil
}

// ---------------------------------------------------------------- panel

func (a *App) runPanel(ctx context.Context) {
	last, err := a.Store.TrafficLast(0)
	if err != nil {
		a.Log.Error("load traffic counters", "err", err)
		last = map[string][2]int64{}
	}
	var lastWarn time.Time
	p := &collect.TrafficPoller{
		Panel:    a.Panel,
		Interval: time.Duration(a.Cfg.Panel.PollSeconds) * time.Second,
		Last:     last,
		Deliver: func(deltas []model.TrafficRec, totals map[string][2]int64, users []model.PanelUser) error {
			if err := a.Store.UpsertPanelUsers(users); err != nil {
				return err
			}
			if err := a.Store.ApplyTraffic(0, deltas, totals); err != nil {
				return err
			}
			a.RecordOutbox(model.Batch{Traffic: deltas, Users: users})
			a.setPanelState(PanelState{Type: a.Panel.Name(), OK: true, LastOK: time.Now().Unix(), Users: len(users)})
			return nil
		},
		Warn: func(err error) {
			prev := a.PanelState()
			a.setPanelState(PanelState{Type: a.Panel.Name(), OK: false, Error: err.Error(), LastOK: prev.LastOK, Users: prev.Users})
			if time.Since(lastWarn) > 5*time.Minute {
				lastWarn = time.Now()
				a.Log.Warn("panel poll failed", "err", err)
			}
		},
	}
	p.Run(ctx)
}

// ---------------------------------------------------------------- nodes

// ErrNodeData is returned for malformed node submissions.
var ErrNodeData = errors.New("invalid batch")

// IngestNode stores a batch received from a remote node.
func (a *App) IngestNode(ctx context.Context, n store.Node, b model.Batch, remote string) error {
	if len(b.Recs) > 2_000_000 || len(b.Traffic) > 200_000 {
		return ErrNodeData
	}
	if b.ID != "" && !a.firstBatch(n.ID, b.ID) {
		return nil // a retry of a batch that was already stored
	}
	if err := a.Store.TouchNode(n.ID, remote, b.Version, b.Panel); err != nil {
		return err
	}
	obs, err := a.Store.ApplyBatch(n.ID, b.Recs, nil, b.Lines)
	if err != nil {
		return err
	}
	a.Live.Observe(obs)
	if err := a.Store.ApplyTraffic(n.ID, b.Traffic, nil); err != nil {
		return err
	}
	if err := a.Store.ApplyActive(n.ID, b.Active); err != nil {
		return err
	}
	if a.Panel == nil && len(b.Users) > 0 {
		if err := a.Store.UpsertPanelUsers(b.Users); err != nil {
			return err
		}
	}
	a.onObs(obs)
	return nil
}

// ---------------------------------------------------------------- maintenance

func (a *App) runMaintenance(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	lastCompact := time.Now()
	a.step(ctx, "backup", a.backupIfDue)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		a.Live.Prune(3 * time.Hour)
		a.step(ctx, "alerts", a.evaluateAlerts)
		a.step(ctx, "digest", a.digestIfDue)
		a.step(ctx, "backup", a.backupIfDue)
		if time.Since(lastCompact) > 6*time.Hour {
			lastCompact = time.Now()
			a.step(ctx, "compact", func(context.Context) error { return a.Compact() })
		}
	}
}

func (a *App) step(ctx context.Context, name string, f func(context.Context) error) {
	defer func() {
		if r := recover(); r != nil {
			a.Log.Error("maintenance step crashed", "step", name, "panic", r)
		}
	}()
	if err := f(ctx); err != nil && ctx.Err() == nil {
		a.Log.Warn("maintenance step failed", "step", name, "err", err)
	}
}

// Compact merges old detail rows according to the retention settings.
func (a *App) Compact() error {
	r := a.Settings.Get().Retention
	now := time.Now()
	n, err := a.Store.Compact(now.AddDate(0, 0, -r.VisitsHourlyDays), now.AddDate(0, 0, -r.Traffic5Days))
	if err == nil && n > 0 {
		a.Log.Info("compacted visit rows", "merged", n)
	}
	return err
}

// BackupNow creates a backup immediately.
func (a *App) BackupNow(ctx context.Context) (backup.Info, error) {
	s := a.Settings.Get()
	info, err := backup.Create(ctx, a.Store, a.Cfg.BackupDir(), s.Backup.Keep)
	if err != nil {
		_ = a.Store.AddEvent("backup", 0, 0, "backup failed: "+err.Error())
		if a.Bot.Enabled() && s.Backup.SendTelegram {
			_ = a.Bot.Send(ctx, a.msg("backup.failed", telegram.Escape(err.Error())))
		}
		return info, err
	}
	_ = a.Store.AddEvent("backup", 0, 0, "backup created: "+info.Name)
	if s.Backup.SendTelegram && a.Bot.Enabled() && info.Size < 45<<20 {
		if data, err := os.ReadFile(filepath.Join(a.Cfg.BackupDir(), info.Name)); err == nil {
			cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			defer cancel()
			if err := a.Bot.SendFile(cctx, info.Name, data, stripTags(a.msg("backup.done", info.Name, humanBytes(info.Size)))); err != nil {
				a.Log.Warn("sending backup to telegram", "err", err)
			}
		}
	}
	return info, nil
}

func (a *App) backupIfDue(ctx context.Context) error {
	s := a.Settings.Get()
	if !s.Backup.Enabled {
		return nil
	}
	list, err := backup.List(a.Cfg.BackupDir())
	if err != nil {
		return err
	}
	if len(list) > 0 && time.Since(time.Unix(list[0].Created, 0)) < time.Duration(s.Backup.IntervalHours)*time.Hour {
		return nil
	}
	_, err = a.BackupNow(ctx)
	return err
}

func sleepCtx(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

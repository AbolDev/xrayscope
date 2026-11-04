package server

import (
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"xrayscope/internal/analytics"
	"xrayscope/internal/app"
	"xrayscope/internal/auth"
	"xrayscope/internal/backup"
	"xrayscope/internal/classify"
	"xrayscope/internal/model"
	"xrayscope/internal/netutil"
	"xrayscope/internal/report"
	"xrayscope/internal/settings"
	"xrayscope/internal/store"
	"xrayscope/internal/update"
	"xrayscope/internal/xraystats"
)

// ---------------------------------------------------------------- auth

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "version": s.app.Version})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if r.Header.Get("X-XS") != "1" {
		fail(w, http.StatusForbidden, "missing request header")
		return
	}
	if !readJSON(w, r, &req) {
		return
	}
	tok, err := s.app.Auth.Login(req.Username, req.Password, s.clientIP(r))
	switch {
	case errors.Is(err, auth.ErrLocked):
		fail(w, http.StatusTooManyRequests, err.Error())
		return
	case err != nil:
		fail(w, http.StatusUnauthorized, err.Error())
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: tok, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode,
		Secure: s.isHTTPS(r), Expires: time.Now().Add(auth.SessionTTL),
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"username": s.app.Auth.Username()})
}

func (s *Server) handlePassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Old string `json:"old"`
		New string `json:"new"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if err := s.app.Auth.ChangePassword(req.Old, req.New); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	// the change invalidated every session, including this one
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---------------------------------------------------------------- meta and status

func (s *Server) handleMeta(w http.ResponseWriter, r *http.Request) {
	cfg := s.app.Settings.Get()
	writeJSON(w, http.StatusOK, map[string]any{
		"version":         s.app.Version,
		"categories":      classify.Categories,
		"timezone":        cfg.DisplayTimezone,
		"language":        cfg.Language,
		"now":             time.Now().Unix(),
		"session_gap_min": cfg.SessionGapMin,
		"base_path":       s.app.Cfg.BasePath,
	})
}

type nodeStatus struct {
	store.Node
	Online bool `json:"online"`
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	a := s.app
	st, err := a.Store.Stats(ctx)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	nodes, err := a.Store.Nodes(ctx)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	cfg := a.Settings.Get()
	ns := make([]nodeStatus, 0, len(nodes))
	for _, n := range nodes {
		ns = append(ns, nodeStatus{n, n.LastSeen > 0 && time.Since(time.Unix(n.LastSeen, 0)) < time.Duration(cfg.Alerts.NodeOffline.Value+1)*time.Minute})
	}
	online := a.Live.Online(time.Duration(cfg.OnlineWindowMin) * time.Minute)
	ips, behindCDN := 0, 0
	for _, o := range online {
		ips += len(o.Conns)
		for _, c := range o.Conns {
			if classify.CDN(c.IP) != "" {
				behindCDN++
			}
		}
	}
	list, _ := backup.List(a.Cfg.BackupDir())
	var last *backup.Info
	if len(list) > 0 {
		last = &list[0]
	}
	ipDest, destTotal, _ := a.Store.DestShare(ctx, time.Now().Unix()/3600-24)
	var lastLine int64
	if t := a.LastLineAt(); !t.IsZero() {
		lastLine = t.Unix()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version":          a.Version,
		"uptime":           int64(time.Since(a.Started).Seconds()),
		"now":              time.Now().Unix(),
		"log":              a.LogState(),
		"last_line":        lastLine,
		"lines":            a.LinesTotal(),
		"panel":            a.PanelState(),
		"db":               st,
		"online":           len(online),
		"online_ips":       ips,
		"cdn_ips":          behindCDN,
		"dest_ip_requests": ipDest,
		"dest_requests":    destTotal,
		"nodes":            ns,
		"backups":          map[string]any{"count": len(list), "last": last, "pending_restore": backup.HasPending(a.Cfg.DataDir)},
		"speed":            speedState(a),
		"telegram":         a.Bot.Enabled(),
		"geo":              a.Geo != nil,
	})
}

// ---------------------------------------------------------------- reports

func parseRange(r *http.Request, defaultSpan time.Duration) (report.Range, error) {
	q := r.URL.Query()
	now := time.Now().Unix()
	rg := report.Range{From: now - int64(defaultSpan/time.Second), To: now}
	if v := q.Get("to"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return rg, errors.New("invalid 'to'")
		}
		rg.To = n
	}
	if v := q.Get("from"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return rg, errors.New("invalid 'from'")
		}
		rg.From = n
	} else if q.Get("to") != "" {
		rg.From = rg.To - int64(defaultSpan/time.Second)
	}
	if rg.To > now+3600 {
		rg.To = now + 3600
	}
	if rg.From < 0 || rg.From >= rg.To {
		return rg, errors.New("invalid time range")
	}
	if rg.To-rg.From > 3*366*86400 {
		return rg, errors.New("time range too long")
	}
	return rg, nil
}

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	rg, err := parseRange(r, 24*time.Hour)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	key := fmt.Sprintf("overview:%d:%d", rg.From/30, rg.To/30)
	b, err := s.cached(key, 5*time.Second, func() (any, error) { return s.app.Env().Overview(r.Context(), rg) })
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	writeRaw(w, b)
}

func (s *Server) handleUsers(w http.ResponseWriter, r *http.Request) {
	rg, err := parseRange(r, 24*time.Hour)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	key := fmt.Sprintf("users:%d:%d", rg.From/30, rg.To/30)
	b, err := s.cached(key, 5*time.Second, func() (any, error) { return s.app.Env().Users(r.Context(), rg) })
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	writeRaw(w, b)
}

func (s *Server) handleUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		fail(w, http.StatusBadRequest, "invalid user id")
		return
	}
	rg, err := parseRange(r, 7*24*time.Hour)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	key := fmt.Sprintf("user:%d:%d:%d", id, rg.From/30, rg.To/30)
	b, err := s.cached(key, 5*time.Second, func() (any, error) { return s.app.Env().User(r.Context(), id, rg) })
	switch {
	case errors.Is(err, report.ErrNotFound):
		fail(w, http.StatusNotFound, "user not found")
	case err != nil:
		s.serverError(w, r, err)
	default:
		writeRaw(w, b)
	}
}

// handleUserInfo is the cheap user lookup used by tabs that load their own data.
func (s *Server) handleUserInfo(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		fail(w, http.StatusBadRequest, "invalid user id")
		return
	}
	u, err := s.app.Store.UserByID(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, "user not found")
		return
	} else if err != nil {
		s.serverError(w, r, err)
		return
	}
	resp := map[string]any{"user": u, "online": nil}
	if o, ok := s.app.Live.OnlineUser(id, s.app.Env().OnlineWin); ok {
		resp["online"] = o
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleTimeline(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		fail(w, http.StatusBadRequest, "invalid user id")
		return
	}
	date := r.URL.Query().Get("date")
	if date == "" {
		date = time.Now().In(s.app.Env().Loc).Format("2006-01-02")
	}
	t, err := s.app.Env().Timeline(r.Context(), id, date)
	switch {
	case errors.Is(err, report.ErrNotFound):
		fail(w, http.StatusNotFound, "user not found")
	case err != nil && err.Error() == "invalid date":
		fail(w, http.StatusBadRequest, "invalid date")
	case err != nil:
		s.serverError(w, r, err)
	default:
		writeJSON(w, http.StatusOK, t)
	}
}

func (s *Server) handleSites(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		fail(w, http.StatusBadRequest, "invalid user id")
		return
	}
	rg, err := parseRange(r, 7*24*time.Hour)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	offset, _ := strconv.Atoi(q.Get("offset"))
	res, err := s.app.Env().Sites(r.Context(), id, rg, q.Get("q"), q.Get("category"), limit, max(offset, 0))
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// handleExport streams CSV: kind=sites | sessions | daily.
func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		fail(w, http.StatusBadRequest, "invalid user id")
		return
	}
	rg, err := parseRange(r, 30*24*time.Hour)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	env := s.app.Env()
	u, err := s.app.Store.UserByID(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, "user not found")
		return
	} else if err != nil {
		s.serverError(w, r, err)
		return
	}
	kind := r.URL.Query().Get("kind")
	if kind == "" {
		kind = "sites"
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-%s.csv"`, safeName(u.Email), kind))
	cw := csv.NewWriter(w)
	defer cw.Flush()
	_, _ = w.Write([]byte("\xef\xbb\xbf")) // BOM so spreadsheets read UTF-8
	ts := func(sec int64) string { return time.Unix(sec, 0).In(env.Loc).Format("2006-01-02 15:04:05") }
	switch kind {
	case "sites":
		res, err := env.Sites(r.Context(), id, rg, "", "", 1_000_000, 0)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		_ = cw.Write([]string{"domain", "category", "service", "requests", "active_minutes", "first_seen", "last_seen"})
		for _, x := range res.Sites {
			_ = cw.Write([]string{x.Domain, x.Category, x.Service, strconv.FormatInt(x.Reqs, 10), strconv.FormatInt(x.Minutes, 10), ts(x.First * 60), ts(x.Last * 60)})
		}
	case "sessions":
		rep, err := env.User(r.Context(), id, rg)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		_ = cw.Write([]string{"ip", "country", "org", "first_seen", "last_seen", "active_minutes", "sessions"})
		for _, x := range rep.IPs {
			_ = cw.Write([]string{x.IP, x.Country, x.Org, ts(x.First), ts(x.Last), strconv.FormatInt(x.Minutes, 10), strconv.Itoa(x.Sessions)})
		}
	case "daily":
		rep, err := env.User(r.Context(), id, rg)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		_ = cw.Write([]string{"date", "online_minutes", "interactive_minutes", "requests", "ips"})
		for _, d := range rep.Activity.Daily {
			_ = cw.Write([]string{d.Date, strconv.FormatInt(d.Online, 10), strconv.FormatInt(d.Interactive, 10), strconv.FormatInt(d.Reqs, 10), strconv.Itoa(d.IPs)})
		}
	default:
		fail(w, http.StatusBadRequest, "unknown export kind")
	}
}

func safeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 128 && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

func (s *Server) handleNote(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		fail(w, http.StatusBadRequest, "invalid user id")
		return
	}
	var req struct {
		Note string `json:"note"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if len(req.Note) > 2000 {
		fail(w, http.StatusBadRequest, "note too long")
		return
	}
	if err := s.app.Store.SetUserNote(id, req.Note); err != nil {
		s.serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		fail(w, http.StatusBadRequest, "invalid user id")
		return
	}
	if err := s.app.Store.DeleteUserData(id); err != nil {
		s.serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleOnline(w http.ResponseWriter, r *http.Request) {
	env := s.app.Env()
	online := s.app.Live.Online(env.OnlineWin)
	type conn struct {
		IP       string `json:"ip"`
		LastSeen int64  `json:"last_seen"`
		Country  string `json:"country"`
		Org      string `json:"org"`
		CDN      string `json:"cdn,omitempty"`
	}
	type row struct {
		UserID   int64  `json:"user_id"`
		Email    string `json:"email"`
		LastSeen int64  `json:"last_seen"`
		LimitIP  int    `json:"limit_ip"`
		Conns    []conn `json:"conns"`
	}
	users, err := s.app.Store.Users(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	limit := map[int64]int{}
	for _, u := range users {
		limit[u.ID] = u.LimitIP
	}
	out := make([]row, 0, len(online))
	for _, o := range online {
		x := row{UserID: o.UserID, Email: o.Email, LastSeen: o.LastSeen, LimitIP: limit[o.UserID]}
		for _, c := range o.Conns {
			cc := conn{IP: c.IP, LastSeen: c.LastSeen, CDN: classify.CDN(c.IP)}
			if ip, err := s.app.Store.IPByAddr(r.Context(), c.IP); err == nil {
				cc.Country, cc.Org = ip.Country, ip.Org
			}
			x.Conns = append(x.Conns, cc)
		}
		out = append(out, x)
	}
	writeJSON(w, http.StatusOK, out)
}

func speedState(a *app.App) map[string]any {
	updated, err := a.Speed.Status()
	m := map[string]any{"ok": !updated.IsZero() && err == nil}
	if err != nil {
		m["error"] = err.Error()
	}
	return m
}

// handleSpeed returns the live transfer speed of every user.
func (s *Server) handleSpeed(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.app.Speeds(r.Context(), 200))
}

// handleUserSpeed returns one user's current speed and the last few minutes.
func (s *Server) handleUserSpeed(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		fail(w, http.StatusBadRequest, "invalid user id")
		return
	}
	u, err := s.app.Store.UserByID(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, "user not found")
		return
	} else if err != nil {
		s.serverError(w, r, err)
		return
	}
	all := s.app.SpeedBook.Merge(s.app.Speed.Rates())
	cur := all[u.Email]
	hist := s.app.Speed.History(u.Email)
	if hist == nil {
		hist = []xraystats.Point{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"up": cur.Up, "down": cur.Down, "history": hist})
}

// handleIngestSpeed accepts the live speeds of a node (Bearer token).
func (s *Server) handleIngestSpeed(w http.ResponseWriter, r *http.Request) {
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	node, err := s.app.Store.NodeByToken(r.Context(), tok)
	if tok == "" || err != nil {
		fail(w, http.StatusUnauthorized, "invalid token")
		return
	}
	var body struct {
		Speeds map[string][2]int64 `json:"speeds"`
	}
	if err := jsonDecode(http.MaxBytesReader(w, r.Body, 4<<20), &body); err != nil {
		fail(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	rates := make(map[string]xraystats.Rate, len(body.Speeds))
	for email, v := range body.Speeds {
		rates[email] = xraystats.Rate{Up: v[0], Down: v[1]}
	}
	s.app.SpeedBook.Set(fmt.Sprintf("node:%d", node.ID), rates)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleIP(w http.ResponseWriter, r *http.Request) {
	rg, err := parseRange(r, 30*24*time.Hour)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	d, err := s.app.Env().IP(r.Context(), r.PathValue("ip"), rg)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	uid, _ := strconv.ParseInt(q.Get("user"), 10, 64)
	ev, err := s.app.Store.Events(r.Context(), uid, limit)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if ev == nil {
		ev = []store.Event{}
	}
	writeJSON(w, http.StatusOK, ev)
}

// ---------------------------------------------------------------- nodes

func (s *Server) handleNodes(w http.ResponseWriter, r *http.Request) {
	nodes, err := s.app.Store.Nodes(r.Context())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	cfg := s.app.Settings.Get()
	out := make([]nodeStatus, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, nodeStatus{n, n.LastSeen > 0 && time.Since(time.Unix(n.LastSeen, 0)) < time.Duration(cfg.Alerts.NodeOffline.Value+1)*time.Minute})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleNodeInfo lets a main server check the connection and credentials.
func (s *Server) handleNodeInfo(w http.ResponseWriter, r *http.Request) {
	info := app.PullInfo{Version: s.app.Version, Role: "node"}
	if s.app.Panel != nil {
		info.Panel = s.app.Panel.Name()
	}
	writeJSON(w, http.StatusOK, info)
}

// handleNodePull hands the queued batches to the main server.
func (s *Server) handleNodePull(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	after, _ := strconv.ParseInt(q.Get("after"), 10, 64)
	limit, _ := strconv.Atoi(q.Get("limit"))
	res, err := s.app.ServePull(r.Context(), after, limit)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// handleNodeSpeed returns this server's own live speeds for a main server.
func (s *Server) handleNodeSpeed(w http.ResponseWriter, r *http.Request) {
	rates := s.app.Speed.Rates()
	out := make(map[string][2]int64, len(rates))
	for e, v := range rates {
		out[e] = [2]int64{v.Up, v.Down}
	}
	writeJSON(w, http.StatusOK, map[string]any{"speeds": out})
}

func (s *Server) handleCreateNode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name     string `json:"name"`
		Mode     string `json:"mode"`
		URL      string `json:"url"`
		Username string `json:"username"`
		Password string `json:"password"`
		Proxy    string `json:"proxy"`
		Insecure bool   `json:"insecure"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.Mode == "pull" {
		u, err := url.Parse(strings.TrimSpace(req.URL))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			fail(w, http.StatusBadRequest, "the address must look like http://203.0.113.5:8686 or https://node.example.com")
			return
		}
		p := store.PullNode{Name: req.Name, URL: strings.TrimRight(u.String(), "/"), Username: strings.TrimSpace(req.Username),
			Password: req.Password, Proxy: strings.TrimSpace(req.Proxy), Insecure: req.Insecure}
		if p.Username == "" || p.Password == "" {
			fail(w, http.StatusBadRequest, "the node's panel username and password are required")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		info, err := s.app.TestPullNode(ctx, p)
		if err != nil {
			fail(w, http.StatusBadGateway, "could not connect to the node: "+err.Error())
			return
		}
		n, err := s.app.Store.CreatePullNode(p)
		if err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"node": n, "info": info})
		return
	}
	n, tok, err := s.app.Store.CreateNode(req.Name)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"node": n, "token": tok})
}

func (s *Server) handleDeleteNode(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		fail(w, http.StatusBadRequest, "invalid node id")
		return
	}
	if err := s.app.Store.DeleteNode(id); err != nil {
		s.serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleIngest accepts batches from agent nodes (Bearer token).
func (s *Server) handleIngest(w http.ResponseWriter, r *http.Request) {
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if tok == "" {
		fail(w, http.StatusUnauthorized, "token required")
		return
	}
	node, err := s.app.Store.NodeByToken(r.Context(), tok)
	if err != nil {
		fail(w, http.StatusUnauthorized, "invalid token")
		return
	}
	var body io.Reader = http.MaxBytesReader(w, r.Body, 64<<20)
	if r.Header.Get("Content-Encoding") == "gzip" {
		zr, err := gzip.NewReader(body)
		if err != nil {
			fail(w, http.StatusBadRequest, "invalid gzip body")
			return
		}
		defer zr.Close()
		body = io.LimitReader(zr, 256<<20)
	}
	var b model.Batch
	if err := jsonDecode(body, &b); err != nil {
		fail(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := s.app.IngestNode(r.Context(), node, b, s.clientIP(r)); err != nil {
		if errors.Is(err, errBadBatch) {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		s.serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "time": time.Now().Unix()})
}

// ---------------------------------------------------------------- settings

type settingsDTO struct {
	settings.Settings
	TokenSet   bool `json:"token_set"`
	ClearToken bool `json:"clear_token,omitempty"`
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	cur := s.app.Settings.Get()
	dto := settingsDTO{Settings: cur, TokenSet: cur.Telegram.Token != ""}
	dto.Telegram.Token = ""
	if dto.CustomRules == nil {
		dto.CustomRules = []classify.Rule{}
	}
	if dto.Telegram.ChatIDs == nil {
		dto.Telegram.ChatIDs = []int64{}
	}
	writeJSON(w, http.StatusOK, dto)
}

func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	var dto settingsDTO
	if !readJSON(w, r, &dto) {
		return
	}
	cur := s.app.Settings.Get()
	next := dto.Settings
	switch {
	case dto.ClearToken:
		next.Telegram.Token = ""
	case strings.TrimSpace(next.Telegram.Token) == "":
		next.Telegram.Token = cur.Telegram.Token
	default:
		next.Telegram.Token = strings.TrimSpace(next.Telegram.Token)
	}
	if err := s.app.Settings.Set(next); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	s.cacheMu.Lock()
	s.cache = map[string]cacheEntry{}
	s.cacheMu.Unlock()
	s.handleGetSettings(w, r)
}

func (s *Server) handleTelegramTest(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := contextWithTimeout(r, 45*time.Second)
	defer cancel()
	name, err := s.app.Bot.Test(ctx)
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "bot": name})
}

func (s *Server) handleReclassify(w http.ResponseWriter, r *http.Request) {
	n, err := s.app.Store.Reclassify()
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.cacheMu.Lock()
	s.cache = map[string]cacheEntry{}
	s.cacheMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"changed": n})
}

// handleUnclassified lists the busiest sites that no rule recognises.
func (s *Server) handleUnclassified(w http.ResponseWriter, r *http.Request) {
	list, err := s.app.Store.Unclassified(r.Context(), 40)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// handleAddRule saves one custom rule and applies it to what is stored.
func (s *Server) handleAddRule(w http.ResponseWriter, r *http.Request) {
	var rule classify.Rule
	if !readJSON(w, r, &rule) {
		return
	}
	rule.Domain = strings.Trim(strings.ToLower(strings.TrimSpace(rule.Domain)), ".")
	rule.Service = strings.TrimSpace(rule.Service)
	if rule.Domain == "" || !classify.IsKnownCategory(rule.Category) {
		fail(w, http.StatusBadRequest, "a domain and a known category are required")
		return
	}
	if rule.Service == "" {
		rule.Service = rule.Domain
	}
	cur := s.app.Settings.Get()
	rules := cur.CustomRules[:0:0]
	for _, x := range cur.CustomRules {
		if !strings.EqualFold(x.Domain, rule.Domain) {
			rules = append(rules, x)
		}
	}
	cur.CustomRules = append(rules, rule)
	if err := s.app.Settings.Set(cur); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	n, err := s.app.Store.Reclassify()
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.cacheMu.Lock()
	s.cache = map[string]cacheEntry{}
	s.cacheMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"changed": n, "rule": rule})
}

func (s *Server) handleGeoRefresh(w http.ResponseWriter, r *http.Request) {
	if s.app.Geo == nil {
		fail(w, http.StatusBadRequest, "no geo database is configured (geo_db / asn_db)")
		return
	}
	n, err := s.app.Store.RefreshGeo(s.app.Geo.Lookup)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"changed": n})
}

// ---------------------------------------------------------------- backups

func (s *Server) handleBackups(w http.ResponseWriter, r *http.Request) {
	list, err := backup.List(s.app.Cfg.BackupDir())
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if list == nil {
		list = []backup.Info{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"backups": list, "pending_restore": backup.HasPending(s.app.Cfg.DataDir)})
}

func (s *Server) handleCreateBackup(w http.ResponseWriter, r *http.Request) {
	info, err := s.app.BackupNow(r.Context())
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleDownloadBackup(w http.ResponseWriter, r *http.Request) {
	p, err := backup.Path(s.app.Cfg.BackupDir(), r.PathValue("name"))
	if err != nil {
		fail(w, http.StatusNotFound, "backup not found")
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="`+r.PathValue("name")+`"`)
	w.Header().Set("Content-Type", "application/gzip")
	http.ServeFile(w, r, p)
}

func (s *Server) handleDeleteBackup(w http.ResponseWriter, r *http.Request) {
	if err := backup.Delete(s.app.Cfg.BackupDir(), r.PathValue("name")); err != nil {
		fail(w, http.StatusNotFound, "backup not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleRestore stages a restore from an uploaded file (multipart "file") or
// a stored backup (?name=). The swap happens on the next start.
func (s *Server) handleRestore(w http.ResponseWriter, r *http.Request) {
	var src io.Reader
	if name := r.URL.Query().Get("name"); name != "" {
		p, err := backup.Path(s.app.Cfg.BackupDir(), name)
		if err != nil {
			fail(w, http.StatusNotFound, "backup not found")
			return
		}
		f, err := os.Open(p)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		defer f.Close()
		src = f
	} else {
		r.Body = http.MaxBytesReader(w, r.Body, 8<<30)
		mr, err := r.MultipartReader()
		if err != nil {
			fail(w, http.StatusBadRequest, "expected a multipart upload")
			return
		}
		for {
			part, err := mr.NextPart()
			if err != nil {
				fail(w, http.StatusBadRequest, "no file in the upload")
				return
			}
			if part.FormName() == "file" {
				src = part
				break
			}
		}
	}
	if err := backup.StageRestore(s.app.Cfg.DataDir, src, store.SchemaVersion); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "restart_required": true})
}

func (s *Server) handleRestart(w http.ResponseWriter, r *http.Request) {
	if s.app.RestartFn == nil {
		fail(w, http.StatusNotImplemented, "restart is not available")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	go func() {
		time.Sleep(500 * time.Millisecond)
		s.app.RestartFn()
	}()
}

func updateRepo() string {
	if r := os.Getenv("XRAYSCOPE_REPO"); r != "" {
		return r
	}
	return update.DefaultRepo
}

// handleUpdateCheck reports the newest stable and beta releases.
func (s *Server) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	hc, err := netutil.NewClient(netutil.Options{Proxy: s.app.Cfg.Proxy, Timeout: 20 * time.Second})
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	list, err := update.Releases(ctx, hc, updateRepo())
	if err != nil {
		fail(w, http.StatusBadGateway, "could not reach GitHub: "+err.Error())
		return
	}
	cur := s.app.Version
	out := map[string]any{"current": cur, "releases": list, "can_install": runtime.GOOS == "linux" && s.app.RestartFn != nil}
	for _, ch := range []string{"stable", "beta"} {
		if rel, ok := update.Pick(list, ch); ok {
			out[ch] = map[string]any{"tag": rel.Tag, "url": rel.URL, "newer": update.Newer(rel.Tag, cur)}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// handleUpdateApply installs a release and restarts.
func (s *Server) handleUpdateApply(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Channel string `json:"channel"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if s.app.RestartFn == nil {
		fail(w, http.StatusNotImplemented, "restart is not available")
		return
	}
	hc, err := netutil.NewClient(netutil.Options{Proxy: s.app.Cfg.Proxy, Timeout: 5 * time.Minute})
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Minute)
	defer cancel()
	list, err := update.Releases(ctx, hc, updateRepo())
	if err != nil {
		fail(w, http.StatusBadGateway, "could not reach GitHub: "+err.Error())
		return
	}
	rel, ok := update.Pick(list, req.Channel)
	if !ok {
		fail(w, http.StatusNotFound, "no release found for this channel")
		return
	}
	if !update.Newer(rel.Tag, s.app.Version) {
		fail(w, http.StatusConflict, "already up to date")
		return
	}
	if err := update.Install(ctx, hc, updateRepo(), rel.Tag); err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	s.app.Log.Info("updated", "from", s.app.Version, "to", rel.Tag)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "version": rel.Tag})
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	go func() {
		time.Sleep(500 * time.Millisecond)
		s.app.RestartFn()
	}()
}

// keep the analytics import used for date helpers in handlers below
var _ = analytics.DayString
var _ = sort.Strings

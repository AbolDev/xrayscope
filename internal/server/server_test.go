package server_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"xrayscope/internal/agent"
	"xrayscope/internal/app"
	"xrayscope/internal/config"
	"xrayscope/internal/server"
)

type env struct {
	app    *app.App
	srv    *httptest.Server
	client *http.Client
	log    string
}

func logLine(ts time.Time, ip, host, user string) string {
	return fmt.Sprintf("%s from %s:4000 accepted tcp:%s:443 [in >> direct] email: %s\n", ts.Format("2006/01/02 15:04:05.000000"), ip, host, user)
}

func newEnv(t *testing.T, opts ...func(*app.App)) *env {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "access.log")
	if err := os.WriteFile(logPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.DataDir = filepath.Join(dir, "data")
	cfg.LogPath = logPath
	cfg.LogTimezone = "UTC"
	cfg.Panel.Type = "none"
	a, err := app.New(cfg, "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	for _, o := range opts {
		o(a)
	}
	s, err := server.New(a)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); a.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })

	jar, _ := cookiejar.New(nil)
	e := &env{app: a, srv: ts, client: &http.Client{Jar: jar, Timeout: 20 * time.Second}, log: logPath}
	return e
}

func (e *env) do(t *testing.T, method, path string, body any) (int, []byte) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, rd)
	req.Header.Set("X-XS", "1")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func (e *env) login(t *testing.T) {
	t.Helper()
	if e.app.InitialPassword == "" {
		t.Fatal("expected an initial password")
	}
	code, b := e.do(t, "POST", "/api/login", map[string]string{"username": "admin", "password": e.app.InitialPassword})
	if code != 200 {
		t.Fatalf("login: %d %s", code, b)
	}
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(40 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

func TestPullNode(t *testing.T) {
	node := newEnv(t)
	node.login(t)
	main := newEnv(t, func(a *app.App) { a.PullInterval = 100 * time.Millisecond })
	main.login(t)

	// wrong credentials are refused up front
	code, _ := main.do(t, "POST", "/api/nodes", map[string]any{
		"name": "bad", "mode": "pull", "url": node.srv.URL, "username": "admin", "password": "nope",
	})
	if code == 200 {
		t.Fatal("a wrong password must not create a node")
	}

	code, b := main.do(t, "POST", "/api/nodes", map[string]any{
		"name": "edge", "mode": "pull", "url": node.srv.URL, "username": "admin", "password": node.app.InitialPassword,
	})
	if code != 200 {
		t.Fatalf("create pull node: %d %s", code, b)
	}

	// the first pull switches the node's outbox on; new log lines then flow
	waitUntil(t, "outbox enabled", func() bool { return node.app.Store.OutboxOn() })
	f, err := os.OpenFile(node.log, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(logLine(time.Now().UTC(), "203.0.113.9", "example.com", "alice@x")); err != nil {
		t.Fatal(err)
	}
	f.Close()

	waitUntil(t, "data pulled into the main server", func() bool {
		users, err := main.app.Store.Users(context.Background())
		if err != nil {
			return false
		}
		for _, u := range users {
			if u.Email == "alice@x" {
				return true
			}
		}
		return false
	})

	_, b = main.do(t, "GET", "/api/nodes", nil)
	if !strings.Contains(string(b), `"mode":"pull"`) {
		t.Fatalf("node list lacks the pull node: %s", b)
	}
	if strings.Contains(string(b), node.app.InitialPassword) {
		t.Fatal("the node password must never be returned")
	}
}

func TestLiveWebSocket(t *testing.T) {
	e := newEnv(t)
	e.login(t)
	u, _ := url.Parse(e.srv.URL)
	conn, err := net.Dial("tcp", u.Host)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var cookie string
	for _, c := range e.client.Jar.Cookies(u) {
		cookie += c.Name + "=" + c.Value + "; "
	}
	fmt.Fprintf(conn, "GET /api/live HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nAccept-Encoding: gzip\r\nCookie: %s\r\n\r\n", u.Host, cookie)
	br := bufio.NewReader(conn)
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	status, _ := br.ReadString('\n')
	if !strings.Contains(status, "101") {
		t.Fatalf("upgrade failed: %s", status)
	}
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(line, "Sec-WebSocket-Accept: s3pPLMBiTxaQ9kYGzzhZRbK+xOo=") {
			break // the example key from RFC 6455
		}
		if line == "\r\n" {
			t.Fatal("missing Sec-WebSocket-Accept")
		}
	}
	for {
		if line, _ := br.ReadString('\n'); line == "\r\n" {
			break
		}
	}
	var h [2]byte
	if _, err := io.ReadFull(br, h[:]); err != nil || h[0] != 0x81 {
		t.Fatalf("frame header %v %v", h, err)
	}
	n := int(h[1] & 0x7f)
	if n == 126 {
		var l [2]byte
		io.ReadFull(br, l[:])
		n = int(l[0])<<8 | int(l[1])
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(br, body); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"speed"`) || !strings.Contains(string(body), `"online"`) {
		t.Fatalf("unexpected snapshot: %s", body)
	}

	// without a session the endpoint is closed
	if code, _ := (&env{srv: e.srv, client: &http.Client{}}).do(t, "GET", "/api/live", nil); code != 401 {
		t.Fatalf("anonymous /api/live: %d", code)
	}
}

func TestAuthRequired(t *testing.T) {
	e := newEnv(t)
	for _, p := range []string{"/api/overview", "/api/users", "/api/status", "/api/settings"} {
		if code, _ := e.do(t, "GET", p, nil); code != 401 {
			t.Errorf("%s without login: %d", p, code)
		}
	}
	if code, _ := e.do(t, "POST", "/api/login", map[string]string{"username": "admin", "password": "wrong-password"}); code != 401 {
		t.Errorf("wrong password: %d", code)
	}
	if code, _ := e.do(t, "GET", "/api/health", nil); code != 200 {
		t.Errorf("health: %d", code)
	}
	e.login(t)
	if code, _ := e.do(t, "GET", "/api/status", nil); code != 200 {
		t.Errorf("status after login: %d", code)
	}
	// state-changing requests need the custom header
	req, _ := http.NewRequest("POST", e.srv.URL+"/api/backups", nil)
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Errorf("POST without header: %d", resp.StatusCode)
	}
}

func TestCollectAndReport(t *testing.T) {
	e := newEnv(t)
	e.login(t)

	now := time.Now().UTC()
	var sb strings.Builder
	for i := 0; i < 20; i++ {
		ts := now.Add(-time.Duration(i) * time.Minute)
		for k := 0; k < 2; k++ { // a page load makes several requests
			sb.WriteString(logLine(ts, "1.1.1.1", "chatgpt.com", "alice"))
		}
		sb.WriteString(logLine(ts, "1.1.1.1", "settings-win.data.microsoft.com", "alice"))
		if i < 10 {
			for k := 0; k < 2; k++ {
				sb.WriteString(logLine(ts, "2.2.2.2", "i.instagram.com", "alice"))
			}
		}
		sb.WriteString(logLine(ts, "3.3.3.3", "www.youtube.com", "bob"))
	}
	f, _ := os.OpenFile(e.log, os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString(sb.String())
	f.Close()

	waitUntil(t, "users to appear", func() bool {
		_, b := e.do(t, "GET", "/api/users", nil)
		return strings.Contains(string(b), "alice") && strings.Contains(string(b), "bob")
	})

	_, b := e.do(t, "GET", "/api/users", nil)
	var users []struct {
		ID     int64  `json:"id"`
		Email  string `json:"email"`
		Online bool   `json:"online"`
		MaxIPs int    `json:"max_ips"`
		IPs    int    `json:"ips"`
	}
	if err := json.Unmarshal(b, &users); err != nil {
		t.Fatal(err, string(b))
	}
	var alice int64
	for _, u := range users {
		if u.Email == "alice" {
			alice = u.ID
			if !u.Online || u.IPs != 2 || u.MaxIPs != 2 {
				t.Errorf("alice row = %+v", u)
			}
		}
	}

	code, b := e.do(t, "GET", fmt.Sprintf("/api/users/%d", alice), nil)
	if code != 200 {
		t.Fatalf("user report: %d %s", code, b)
	}
	var rep struct {
		Activity struct {
			OnlineMinutes int64 `json:"online_minutes"`
		} `json:"activity"`
		Favorites map[string][]struct {
			Service string `json:"service"`
		} `json:"favorites"`
		Devices []struct {
			OS string `json:"os"`
		} `json:"devices"`
		Concurrency struct {
			Max int `json:"max"`
		} `json:"concurrency"`
	}
	if err := json.Unmarshal(b, &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Activity.OnlineMinutes < 15 || rep.Concurrency.Max != 2 {
		t.Errorf("report = %+v", rep)
	}
	if len(rep.Favorites["ai"]) == 0 || rep.Favorites["ai"][0].Service != "ChatGPT" {
		t.Errorf("favourites = %+v", rep.Favorites)
	}
	if len(rep.Devices) == 0 || rep.Devices[0].OS != "Windows" {
		t.Errorf("devices = %+v", rep.Devices)
	}

	if code, _ := e.do(t, "GET", "/api/overview", nil); code != 200 {
		t.Errorf("overview: %d", code)
	}
	if code, b := e.do(t, "GET", "/api/online", nil); code != 200 || !strings.Contains(string(b), "alice") {
		t.Errorf("online: %d %s", code, b)
	}
	if code, b := e.do(t, "GET", fmt.Sprintf("/api/users/%d/export?kind=sites", alice), nil); code != 200 || !strings.Contains(string(b), "chatgpt.com") {
		t.Errorf("export: %d %s", code, b)
	}
	if code, _ := e.do(t, "GET", "/api/ips/1.1.1.1", nil); code != 200 {
		t.Errorf("ip detail: %d", code)
	}

	// settings: the Telegram token is never returned
	code, b = e.do(t, "PUT", "/api/settings", map[string]any{
		"language": "en", "display_timezone": "UTC", "session_gap_min": 5, "online_window_min": 3,
		"telegram": map[string]any{"enabled": false, "token": "123:secret", "chat_ids": []int64{1}},
		"alerts":   map[string]any{}, "backup": map[string]any{}, "retention": map[string]any{},
	})
	if code != 200 || strings.Contains(string(b), "123:secret") || !strings.Contains(string(b), `"token_set":true`) {
		t.Errorf("settings: %d %s", code, b)
	}
	if code, _ := e.do(t, "PUT", "/api/settings", map[string]any{"language": "xx"}); code != 400 {
		t.Errorf("bad settings accepted: %d", code)
	}

	// backups
	if code, b := e.do(t, "POST", "/api/backups", nil); code != 200 {
		t.Errorf("backup: %d %s", code, b)
	}
	if _, b := e.do(t, "GET", "/api/backups", nil); !strings.Contains(string(b), "xrayscope-") {
		t.Errorf("backups list: %s", b)
	}
}

func TestLiveSpeedFromNodes(t *testing.T) {
	e := newEnv(t)
	e.login(t)
	_, b := e.do(t, "POST", "/api/nodes", map[string]string{"name": "fast-1"})
	var created struct {
		Token string `json:"token"`
	}
	json.Unmarshal(b, &created)

	body := `{"speeds":{"alice":[1000,250000],"bob":[10,20]}}`
	req, _ := http.NewRequest("POST", e.srv.URL+"/api/ingest/speed", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+created.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("speed ingest: %v %v", err, resp)
	}
	resp.Body.Close()
	bad, _ := http.NewRequest("POST", e.srv.URL+"/api/ingest/speed", strings.NewReader(body))
	bad.Header.Set("Authorization", "Bearer nope")
	if r, _ := http.DefaultClient.Do(bad); r.StatusCode != 401 {
		t.Fatalf("bad token: %d", r.StatusCode)
	}

	code, b := e.do(t, "GET", "/api/speed", nil)
	if code != 200 {
		t.Fatalf("speed: %d %s", code, b)
	}
	var rep struct {
		Available bool `json:"available"`
		Total     struct{ Up, Down int64 }
		Users     []struct {
			Email    string
			Up, Down int64
		}
	}
	if err := json.Unmarshal(b, &rep); err != nil {
		t.Fatal(err)
	}
	if !rep.Available || rep.Total.Down != 250020 || len(rep.Users) != 2 || rep.Users[0].Email != "alice" {
		t.Fatalf("speed report = %s", b)
	}
}

func TestNodeAgent(t *testing.T) {
	e := newEnv(t)
	e.login(t)

	code, b := e.do(t, "POST", "/api/nodes", map[string]string{"name": "de-1"})
	if code != 200 {
		t.Fatalf("create node: %d %s", code, b)
	}
	var created struct {
		Token string `json:"token"`
	}
	json.Unmarshal(b, &created)
	if !strings.HasPrefix(created.Token, "xs_") {
		t.Fatalf("token = %q", created.Token)
	}

	dir := t.TempDir()
	nodeLog := filepath.Join(dir, "access.log")
	os.WriteFile(nodeLog, []byte(logLine(time.Now().UTC(), "9.9.9.9", "claude.ai", "carol")), 0o600)
	cfg := config.Default()
	cfg.Role = "agent"
	cfg.DataDir = filepath.Join(dir, "data")
	cfg.LogPath = nodeLog
	cfg.LogTimezone = "UTC"
	cfg.Panel.Type = "none"
	cfg.ServerURL = e.srv.URL
	cfg.Token = created.Token
	cfg.NodeName = "de-1"
	ag, err := agent.New(cfg, "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); ag.Run(ctx) }()
	defer func() { cancel(); <-done }()

	waitUntil(t, "node data", func() bool {
		_, b := e.do(t, "GET", "/api/users", nil)
		return strings.Contains(string(b), "carol")
	})
	_, b = e.do(t, "GET", "/api/nodes", nil)
	if !strings.Contains(string(b), `"online":true`) || !strings.Contains(string(b), "de-1") {
		t.Errorf("nodes = %s", b)
	}

	// a wrong token is refused
	req, _ := http.NewRequest("POST", e.srv.URL+"/api/ingest", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer nope")
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != 401 {
		t.Errorf("bad token: %d", resp.StatusCode)
	}
	resp.Body.Close()
}

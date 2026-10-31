// Package server exposes the HTTP API and the embedded web panel.
package server

import (
	"compress/gzip"
	"context"
	"crypto/sha1"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"xrayscope/internal/app"
)

//go:embed web
var webFS embed.FS

const cookieName = "xs_session"

// Server serves the panel.
type Server struct {
	app    *app.App
	log    *slog.Logger
	static fs.FS
	dev    bool
	etags  map[string]string

	cacheMu sync.Mutex
	cache   map[string]cacheEntry
}

type cacheEntry struct {
	at   time.Time
	body []byte
}

// New builds the server for an app.
func New(a *app.App) (*Server, error) {
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		return nil, err
	}
	devDir := os.Getenv("XRAYSCOPE_WEB_DIR") // development: serve the UI from disk
	if devDir != "" {
		sub = os.DirFS(devDir)
	}
	s := &Server{app: a, log: a.Log, static: sub, dev: devDir != "", etags: map[string]string{}, cache: map[string]cacheEntry{}}
	_ = fs.WalkDir(sub, ".", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if b, err := fs.ReadFile(sub, p); err == nil {
				h := sha1.Sum(b)
				s.etags[p] = `"` + hex.EncodeToString(h[:6]) + `"`
			}
		}
		return nil
	})
	return s, nil
}

// Handler returns the root handler (with the base path applied).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.routes(mux)
	var h http.Handler = mux
	h = s.securityHeaders(h)
	h = gzipMiddleware(h)
	h = s.recoverer(h)
	if bp := s.app.Cfg.BasePath; bp != "" {
		root := http.NewServeMux()
		root.Handle(bp+"/", http.StripPrefix(bp, h))
		root.HandleFunc(bp, func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, bp+"/", http.StatusMovedPermanently)
		})
		return root
	}
	return h
}

// Serve listens until ctx ends.
func (s *Server) Serve(ctx context.Context) error {
	cfg := s.app.Cfg
	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	scheme := "http"
	if cfg.TLSCert != "" {
		scheme = "https"
	}
	s.log.Info("web panel listening", "url", fmt.Sprintf("%s://%s%s/", scheme, cfg.Listen, cfg.BasePath))
	if cfg.TLSCert != "" {
		err = srv.ServeTLS(ln, cfg.TLSCert, cfg.TLSKey)
	} else {
		err = srv.Serve(ln)
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) routes(mux *http.ServeMux) {
	// public
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("POST /api/ingest", s.handleIngest)
	mux.HandleFunc("POST /api/ingest/speed", s.handleIngestSpeed)

	// authenticated
	authed := func(pattern string, h http.HandlerFunc) {
		mux.Handle(pattern, s.requireAuth(h))
	}
	authed("POST /api/logout", s.handleLogout)
	authed("GET /api/me", s.handleMe)
	authed("POST /api/password", s.handlePassword)
	authed("GET /api/meta", s.handleMeta)
	authed("GET /api/status", s.handleStatus)
	authed("GET /api/overview", s.handleOverview)
	authed("GET /api/users", s.handleUsers)
	authed("GET /api/users/{id}", s.handleUser)
	authed("GET /api/users/{id}/info", s.handleUserInfo)
	authed("GET /api/users/{id}/timeline", s.handleTimeline)
	authed("GET /api/users/{id}/sites", s.handleSites)
	authed("GET /api/users/{id}/export", s.handleExport)
	authed("POST /api/users/{id}/note", s.handleNote)
	authed("DELETE /api/users/{id}", s.handleDeleteUser)
	authed("GET /api/online", s.handleOnline)
	authed("GET /api/speed", s.handleSpeed)
	authed("GET /api/users/{id}/speed", s.handleUserSpeed)
	authed("GET /api/ips/{ip}", s.handleIP)
	authed("GET /api/events", s.handleEvents)
	authed("GET /api/node/info", s.handleNodeInfo)
	authed("GET /api/node/pull", s.handleNodePull)
	authed("GET /api/node/speed", s.handleNodeSpeed)
	authed("GET /api/nodes", s.handleNodes)
	authed("POST /api/nodes", s.handleCreateNode)
	authed("DELETE /api/nodes/{id}", s.handleDeleteNode)
	authed("GET /api/settings", s.handleGetSettings)
	authed("PUT /api/settings", s.handlePutSettings)
	authed("POST /api/telegram/test", s.handleTelegramTest)
	authed("POST /api/rules/reclassify", s.handleReclassify)
	authed("POST /api/geo/refresh", s.handleGeoRefresh)
	authed("GET /api/backups", s.handleBackups)
	authed("POST /api/backups", s.handleCreateBackup)
	authed("GET /api/backups/{name}", s.handleDownloadBackup)
	authed("DELETE /api/backups/{name}", s.handleDeleteBackup)
	authed("POST /api/backups/restore", s.handleRestore)
	authed("POST /api/restart", s.handleRestart)
	authed("GET /api/update", s.handleUpdateCheck)
	authed("POST /api/update", s.handleUpdateApply)

	mux.HandleFunc("GET /", s.handleStatic)
}

// ---------------------------------------------------------------- helpers

type apiError struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, apiError{msg})
}

func (s *Server) serverError(w http.ResponseWriter, r *http.Request, err error) {
	s.log.Error("request failed", "path", r.URL.Path, "err", err)
	fail(w, http.StatusInternalServerError, "internal error")
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(v); err != nil {
		fail(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}

// clientIP returns the caller address, honouring proxy headers only when
// the administrator enabled trust_proxy.
func (s *Server) clientIP(r *http.Request) string {
	if s.app.Cfg.TrustProxy {
		if v := r.Header.Get("X-Real-IP"); v != "" {
			return strings.TrimSpace(v)
		}
		if v := r.Header.Get("X-Forwarded-For"); v != "" {
			first, _, _ := strings.Cut(v, ",")
			return strings.TrimSpace(first)
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return s.app.Cfg.TrustProxy && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// cached returns the JSON for key, recomputing at most once per ttl.
func (s *Server) cached(key string, ttl time.Duration, build func() (any, error)) ([]byte, error) {
	s.cacheMu.Lock()
	if e, ok := s.cache[key]; ok && time.Since(e.at) < ttl {
		s.cacheMu.Unlock()
		return e.body, nil
	}
	s.cacheMu.Unlock()
	v, err := build()
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	s.cacheMu.Lock()
	if len(s.cache) > 64 {
		for k, e := range s.cache {
			if time.Since(e.at) > ttl {
				delete(s.cache, k)
			}
		}
		if len(s.cache) > 64 {
			s.cache = map[string]cacheEntry{}
		}
	}
	s.cache[key] = cacheEntry{time.Now(), b}
	s.cacheMu.Unlock()
	return b, nil
}

func writeRaw(w http.ResponseWriter, b []byte) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(b)
}

// ---------------------------------------------------------------- middleware

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil && rec != http.ErrAbortHandler {
				s.log.Error("panic in handler", "path", r.URL.Path, "panic", rec)
				fail(w, http.StatusInternalServerError, "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		if s.isHTTPS(r) {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requireAuth(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(cookieName)
		if err != nil {
			fail(w, http.StatusUnauthorized, "login required")
			return
		}
		if _, ok := s.app.Auth.Verify(c.Value); !ok {
			fail(w, http.StatusUnauthorized, "login required")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get("X-XS") != "1" {
			fail(w, http.StatusForbidden, "missing request header")
			return
		}
		next(w, r)
	})
}

type gzipWriter struct {
	http.ResponseWriter
	zw      *gzip.Writer
	started bool
	use     bool
}

var gzPool = sync.Pool{New: func() any { return gzip.NewWriter(io.Discard) }}

func (g *gzipWriter) start() {
	if g.started {
		return
	}
	g.started = true
	h := g.Header()
	ct := h.Get("Content-Type")
	compressible := strings.HasPrefix(ct, "application/json") || strings.HasPrefix(ct, "text/") ||
		strings.Contains(ct, "javascript") || strings.Contains(ct, "svg") || strings.HasPrefix(ct, "text/csv")
	if compressible && h.Get("Content-Encoding") == "" && h.Get("Content-Range") == "" {
		g.use = true
		h.Set("Content-Encoding", "gzip")
		h.Add("Vary", "Accept-Encoding")
		h.Del("Content-Length")
		g.zw = gzPool.Get().(*gzip.Writer)
		g.zw.Reset(g.ResponseWriter)
	}
}

func (g *gzipWriter) WriteHeader(code int) {
	g.start()
	g.ResponseWriter.WriteHeader(code)
}

func (g *gzipWriter) Write(b []byte) (int, error) {
	g.start()
	if g.use {
		return g.zw.Write(b)
	}
	return g.ResponseWriter.Write(b)
}

func (g *gzipWriter) Flush() {
	if g.use {
		_ = g.zw.Flush()
	}
	if f, ok := g.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (g *gzipWriter) close() {
	if g.use {
		_ = g.zw.Close()
		gzPool.Put(g.zw)
	}
}

func gzipMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") || r.URL.Path == "/api/ingest" || strings.HasPrefix(r.URL.Path, "/api/backups/") && r.Method == http.MethodGet {
			next.ServeHTTP(w, r)
			return
		}
		g := &gzipWriter{ResponseWriter: w}
		defer g.close()
		next.ServeHTTP(g, r)
	})
}

// ---------------------------------------------------------------- static files

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	p := path.Clean(strings.TrimPrefix(r.URL.Path, "/"))
	if p == "." || p == "" {
		p = "index.html"
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		fail(w, http.StatusNotFound, "not found")
		return
	}
	b, err := fs.ReadFile(s.static, p)
	if err != nil {
		// single-page app: unknown paths without an extension get the index
		if path.Ext(p) != "" {
			http.NotFound(w, r)
			return
		}
		p = "index.html"
		if b, err = fs.ReadFile(s.static, p); err != nil {
			http.NotFound(w, r)
			return
		}
	}
	et := s.etags[p]
	w.Header().Set("ETag", et)
	w.Header().Set("Cache-Control", "no-cache")
	if s.dev {
		et = ""
		w.Header().Del("ETag")
		w.Header().Set("Cache-Control", "no-store")
	}
	if r.Header.Get("If-None-Match") == et && et != "" {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	switch path.Ext(p) {
	case ".html":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	case ".js":
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	case ".css":
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	case ".svg":
		w.Header().Set("Content-Type", "image/svg+xml")
	case ".json":
		w.Header().Set("Content-Type", "application/json")
	case ".woff2":
		w.Header().Set("Content-Type", "font/woff2")
	}
	_, _ = w.Write(b)
}

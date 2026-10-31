// Package store persists everything in a single SQLite database.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"xrayscope/internal/classify"

	_ "modernc.org/sqlite"
)

const schemaVersion = 2

// SchemaVersion is the database schema this build reads and writes.
const SchemaVersion = schemaVersion

// GeoFunc resolves an IP address to country code, ASN and organisation.
type GeoFunc func(ip string) (country string, asn int, org string)

// Store is the database handle. Writes are serialised; reads use a pool.
type Store struct {
	path string
	w    *sql.DB
	r    *sql.DB

	mu  sync.Mutex // guards writers and the caches below
	cls atomic.Pointer[classify.Classifier]
	geo atomic.Pointer[GeoFunc]

	users map[string]int64
	ips   map[string]int64
	hosts map[string]hostInfo
	sites map[string]int64
	svcs  map[svcKey]int64

	recent map[recentKey]int // requests per (minute, user, site) seen so far

	outboxOn     atomic.Bool
	outboxPushes atomic.Int64
}

type svcKey struct{ category, name string }

type hostInfo struct {
	domID int64
	svcID int64
	bg    bool
	hints []classify.Hint
}

// Open opens (and migrates) the database at path.
func Open(path string, cls *classify.Classifier) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, err
	}
	base := "?_pragma=busy_timeout(15000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=temp_store(MEMORY)&_pragma=cache_size(-32768)"
	w, err := sql.Open("sqlite", path+base+"&_txlock=immediate")
	if err != nil {
		return nil, err
	}
	w.SetMaxOpenConns(1)
	r, err := sql.Open("sqlite", path+base+"&_pragma=query_only(1)")
	if err != nil {
		w.Close()
		return nil, err
	}
	r.SetMaxOpenConns(4)
	s := &Store{
		path: path, w: w, r: r,
		users: map[string]int64{}, ips: map[string]int64{}, hosts: map[string]hostInfo{},
		sites: map[string]int64{}, svcs: map[svcKey]int64{}, recent: map[recentKey]int{},
	}
	if cls == nil {
		cls = classify.New(nil)
	}
	s.cls.Store(cls)
	if err := s.migrate(); err != nil {
		s.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

// Path returns the database file path.
func (s *Store) Path() string { return s.path }

// Close closes both connection pools.
func (s *Store) Close() error {
	s.r.Close()
	_, _ = s.w.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	return s.w.Close()
}

// SetClassifier swaps the classification rules and drops host caches.
func (s *Store) SetClassifier(c *classify.Classifier) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cls.Store(c)
	s.hosts = map[string]hostInfo{}
}

// Classifier returns the active classifier.
func (s *Store) Classifier() *classify.Classifier { return s.cls.Load() }

// SetGeo installs the IP geolocation resolver.
func (s *Store) SetGeo(f GeoFunc) {
	if f == nil {
		s.geo.Store(nil)
		return
	}
	s.geo.Store(&f)
}

func (s *Store) migrate() error {
	if _, err := s.w.Exec(`CREATE TABLE IF NOT EXISTS kv (k TEXT PRIMARY KEY, v TEXT NOT NULL) WITHOUT ROWID`); err != nil {
		return err
	}
	cur := 0
	if v, ok, _ := s.KV("schema"); ok {
		cur, _ = strconv.Atoi(v)
	}
	if cur > schemaVersion {
		return fmt.Errorf("database schema %d is newer than this build (%d)", cur, schemaVersion)
	}
	if cur < 1 {
		if _, err := s.w.Exec(schemaV1); err != nil {
			return err
		}
	}
	if cur < 2 {
		tx, err := s.w.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		for _, q := range schemaV2 {
			if _, err := tx.Exec(q); err != nil {
				return fmt.Errorf("%s: %w", q, err)
			}
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return s.SetKV("schema", strconv.Itoa(schemaVersion))
}

// KV reads a key from the key/value table.
func (s *Store) KV(key string) (string, bool, error) {
	var v string
	err := s.r.QueryRow(`SELECT v FROM kv WHERE k=?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return v, err == nil, err
}

// SetKV writes a key.
func (s *Store) SetKV(key, val string) error {
	_, err := s.w.Exec(`INSERT INTO kv(k,v) VALUES(?,?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, key, val)
	return err
}

// DeleteKV removes a key.
func (s *Store) DeleteKV(key string) error {
	_, err := s.w.Exec(`DELETE FROM kv WHERE k=?`, key)
	return err
}

// Event is an entry of the alert/event log.
type Event struct {
	ID     int64  `json:"id"`
	TS     int64  `json:"ts"`
	Kind   string `json:"kind"`
	UserID int64  `json:"user_id"`
	NodeID int64  `json:"node_id"`
	Text   string `json:"text"`
}

// AddEvent appends to the event log.
func (s *Store) AddEvent(kind string, userID, nodeID int64, text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.w.Exec(`INSERT INTO events(ts,kind,user_id,node_id,text) VALUES(?,?,?,?,?)`,
		time.Now().Unix(), kind, userID, nodeID, text)
	return err
}

// Events lists recent events, newest first.
func (s *Store) Events(ctx context.Context, userID int64, limit int) ([]Event, error) {
	q := `SELECT id,ts,kind,user_id,node_id,text FROM events`
	var args []any
	if userID > 0 {
		q += ` WHERE user_id=?`
		args = append(args, userID)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.r.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.TS, &e.Kind, &e.UserID, &e.NodeID, &e.Text); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Backup writes a consistent snapshot of the database to dest.
func (s *Store) Backup(ctx context.Context, dest string) error {
	_ = os.Remove(dest)
	_, err := s.w.ExecContext(ctx, `VACUUM INTO ?`, dest)
	return err
}

// Stats returns row counts and file size for the status page.
type Stats struct {
	SizeBytes int64 `json:"size_bytes"`
	Users     int64 `json:"users"`
	IPs       int64 `json:"ips"`
	Domains   int64 `json:"domains"`
	ActRows   int64 `json:"act_rows"`
	VisitRows int64 `json:"visit_rows"`
	FirstHour int64 `json:"first_hour"`
	LastHour  int64 `json:"last_hour"`
}

func (s *Store) Stats(ctx context.Context) (Stats, error) {
	var st Stats
	if fi, err := os.Stat(s.path); err == nil {
		st.SizeBytes = fi.Size()
		if wi, err := os.Stat(s.path + "-wal"); err == nil {
			st.SizeBytes += wi.Size()
		}
	}
	for q, dst := range map[string]*int64{
		`SELECT COUNT(*) FROM users`:   &st.Users,
		`SELECT COUNT(*) FROM ips`:     &st.IPs,
		`SELECT COUNT(*) FROM domains`: &st.Domains,
		`SELECT COUNT(*) FROM act`:     &st.ActRows,
		`SELECT COUNT(*) FROM visits`:  &st.VisitRows,
	} {
		if err := s.r.QueryRowContext(ctx, q).Scan(dst); err != nil {
			return st, err
		}
	}
	_ = s.r.QueryRowContext(ctx, `SELECT COALESCE(MIN(hour),0), COALESCE(MAX(hour),0) FROM act`).Scan(&st.FirstHour, &st.LastHour)
	return st, nil
}

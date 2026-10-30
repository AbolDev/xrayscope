package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// User is an account seen in the logs and/or synced from the panel.
type User struct {
	ID        int64  `json:"id"`
	Email     string `json:"email"`
	FirstSeen int64  `json:"first_seen"`
	LastSeen  int64  `json:"last_seen"`
	Enabled   bool   `json:"enabled"`
	Expiry    int64  `json:"expiry"`
	Quota     int64  `json:"quota"`
	UpTotal   int64  `json:"up_total"`
	DownTotal int64  `json:"down_total"`
	LimitIP   int    `json:"limit_ip"`
	Inbound   string `json:"inbound"`
	Note      string `json:"note"`
}

const userCols = `id,email,first_seen,last_seen,enabled,expiry,quota,up_total,down_total,limit_ip,inbound,note`

func scanUser(sc interface{ Scan(...any) error }) (User, error) {
	var u User
	var en int
	err := sc.Scan(&u.ID, &u.Email, &u.FirstSeen, &u.LastSeen, &en, &u.Expiry, &u.Quota, &u.UpTotal, &u.DownTotal, &u.LimitIP, &u.Inbound, &u.Note)
	u.Enabled = en != 0
	return u, err
}

// Users lists all users.
func (s *Store) Users(ctx context.Context) ([]User, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT `+userCols+` FROM users ORDER BY email`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// UserByID returns one user or sql.ErrNoRows.
func (s *Store) UserByID(ctx context.Context, id int64) (User, error) {
	return scanUser(s.r.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE id=?`, id))
}

// UserByEmail returns one user or sql.ErrNoRows.
func (s *Store) UserByEmail(ctx context.Context, email string) (User, error) {
	return scanUser(s.r.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE email=?`, email))
}

// SetUserNote stores a free-form administrator note.
func (s *Store) SetUserNote(id int64, note string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.w.Exec(`UPDATE users SET note=? WHERE id=?`, note, id)
	return err
}

// IP is a client address with optional geolocation.
type IP struct {
	ID      int64  `json:"id"`
	IP      string `json:"ip"`
	Country string `json:"country"`
	ASN     int    `json:"asn"`
	Org     string `json:"org"`
}

// IPsByID loads a set of IPs.
func (s *Store) IPsByID(ctx context.Context, ids []int64) (map[int64]IP, error) {
	out := map[int64]IP{}
	for len(ids) > 0 {
		n := len(ids)
		if n > 500 {
			n = 500
		}
		chunk := ids[:n]
		ids = ids[n:]
		q := `SELECT id,ip,country,asn,org FROM ips WHERE id IN (` + placeholders(len(chunk)) + `)`
		args := make([]any, len(chunk))
		for i, v := range chunk {
			args[i] = v
		}
		rows, err := s.r.QueryContext(ctx, q, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var ip IP
			if err := rows.Scan(&ip.ID, &ip.IP, &ip.Country, &ip.ASN, &ip.Org); err != nil {
				rows.Close()
				return nil, err
			}
			out[ip.ID] = ip
		}
		rows.Close()
	}
	return out, nil
}

// SetIPGeo updates geolocation of every stored IP using f. It returns the
// number of rows that changed.
func (s *Store) RefreshGeo(f GeoFunc) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.w.Query(`SELECT id, ip, country, asn, org FROM ips`)
	if err != nil {
		return 0, err
	}
	type upd struct {
		id      int64
		country string
		asn     int
		org     string
	}
	var ups []upd
	for rows.Next() {
		var id int64
		var ip, c, o string
		var a int
		if err := rows.Scan(&id, &ip, &c, &a, &o); err != nil {
			rows.Close()
			return 0, err
		}
		nc, na, no := f(ip)
		if nc != c || na != a || no != o {
			ups = append(ups, upd{id, nc, na, no})
		}
	}
	rows.Close()
	if len(ups) == 0 {
		return 0, nil
	}
	tx, err := s.w.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	for _, u := range ups {
		if _, err := tx.Exec(`UPDATE ips SET country=?, asn=?, org=? WHERE id=?`, u.country, u.asn, u.org, u.id); err != nil {
			return 0, err
		}
	}
	return len(ups), tx.Commit()
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat("?,", n-1) + "?"
}

// ActRow is a presence row: minutes a user was active from an IP in one hour.
type ActRow struct {
	Hour   int64
	UserID int64
	IPID   int64
	NodeID int64
	Mask   int64
	IMask  int64
	Reqs   int64
}

// Act returns presence rows in [fromHour, toHour). userID 0 means all users.
func (s *Store) Act(ctx context.Context, userID, fromHour, toHour int64) ([]ActRow, error) {
	q := `SELECT hour,user_id,ip_id,node_id,mask,imask,reqs FROM act WHERE hour>=? AND hour<?`
	args := []any{fromHour, toHour}
	if userID > 0 {
		q += ` AND user_id=?`
		args = append(args, userID)
	}
	q += ` ORDER BY hour`
	rows, err := s.r.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ActRow
	for rows.Next() {
		var a ActRow
		if err := rows.Scan(&a.Hour, &a.UserID, &a.IPID, &a.NodeID, &a.Mask, &a.IMask, &a.Reqs); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ActForIP returns presence rows of one IP in [fromHour, toHour).
func (s *Store) ActForIP(ctx context.Context, ipID, fromHour, toHour int64) ([]ActRow, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT hour,user_id,ip_id,node_id,mask,imask,reqs FROM act WHERE ip_id=? AND hour>=? AND hour<? ORDER BY hour`, ipID, fromHour, toHour)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ActRow
	for rows.Next() {
		var a ActRow
		if err := rows.Scan(&a.Hour, &a.UserID, &a.IPID, &a.NodeID, &a.Mask, &a.IMask, &a.Reqs); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// IPByAddr looks an IP up by its address.
func (s *Store) IPByAddr(ctx context.Context, addr string) (IP, error) {
	var ip IP
	err := s.r.QueryRowContext(ctx, `SELECT id,ip,country,asn,org FROM ips WHERE ip=?`, addr).Scan(&ip.ID, &ip.IP, &ip.Country, &ip.ASN, &ip.Org)
	return ip, err
}

// UserIPFirstHour returns the first hour slot in which a user was seen on an
// address (0 if never).
func (s *Store) UserIPFirstHour(ctx context.Context, userID int64, ip string) (int64, error) {
	var h sql.NullInt64
	err := s.r.QueryRowContext(ctx, `SELECT MIN(a.hour) FROM act a JOIN ips i ON i.id=a.ip_id WHERE a.user_id=? AND i.ip=?`, userID, ip).Scan(&h)
	return h.Int64, err
}

// UserCountryFirstHour returns the first hour slot in which a user was seen
// in a country (0 if never).
func (s *Store) UserCountryFirstHour(ctx context.Context, userID int64, country string) (int64, error) {
	var h sql.NullInt64
	err := s.r.QueryRowContext(ctx, `SELECT MIN(a.hour) FROM act a JOIN ips i ON i.id=a.ip_id WHERE a.user_id=? AND i.country=?`, userID, country).Scan(&h)
	return h.Int64, err
}

// SvcRow is time spent on one service in one hour.
type SvcRow struct {
	Hour      int64
	UserID    int64
	ServiceID int64
	Mask      int64
	Reqs      int64
}

// Service describes a (category, name) pair.
type Service struct {
	ID       int64  `json:"id"`
	Category string `json:"category"`
	Name     string `json:"name"`
}

// Services returns every known service by id.
func (s *Store) Services(ctx context.Context) (map[int64]Service, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT id,category,name FROM services`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]Service{}
	for rows.Next() {
		var sv Service
		if err := rows.Scan(&sv.ID, &sv.Category, &sv.Name); err != nil {
			return nil, err
		}
		out[sv.ID] = sv
	}
	return out, rows.Err()
}

// Svc returns service-time rows in [fromHour, toHour). userID 0 means all users.
func (s *Store) Svc(ctx context.Context, userID, fromHour, toHour int64) ([]SvcRow, error) {
	q := `SELECT hour,user_id,service_id,mask,reqs FROM svc WHERE hour>=? AND hour<?`
	args := []any{fromHour, toHour}
	if userID > 0 {
		q += ` AND user_id=?`
		args = append(args, userID)
	}
	rows, err := s.r.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SvcRow
	for rows.Next() {
		var v SvcRow
		if err := rows.Scan(&v.Hour, &v.UserID, &v.ServiceID, &v.Mask, &v.Reqs); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// VisitRow is one site seen by one user in one slot.
type VisitRow struct {
	Slot     int64
	UserID   int64
	DomainID int64
	Grain    int // 0 hourly (mask = minutes), 1 daily (mask = hours of day)
	Mask     int64
	Reqs     int64
}

// Domain is a stored site.
type Domain struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	ServiceID  int64  `json:"service_id"`
	Category   string `json:"category"`
	Background bool   `json:"background"`
}

// Visits returns visit rows overlapping [fromHour, toHour). userID 0 means all.
func (s *Store) Visits(ctx context.Context, userID, fromHour, toHour int64) ([]VisitRow, error) {
	// daily rows start at a day boundary; include the day containing fromHour
	q := `SELECT slot,user_id,domain_id,g,mask,reqs FROM visits WHERE ((g=0 AND slot>=?) OR (g=1 AND slot>=?)) AND slot<?`
	args := []any{fromHour, fromHour - fromHour%24, toHour}
	if userID > 0 {
		q += ` AND user_id=?`
		args = append(args, userID)
	}
	rows, err := s.r.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []VisitRow
	for rows.Next() {
		var v VisitRow
		if err := rows.Scan(&v.Slot, &v.UserID, &v.DomainID, &v.Grain, &v.Mask, &v.Reqs); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// Domains loads domains by id.
func (s *Store) Domains(ctx context.Context, ids []int64) (map[int64]Domain, error) {
	out := map[int64]Domain{}
	for len(ids) > 0 {
		n := len(ids)
		if n > 500 {
			n = 500
		}
		chunk := ids[:n]
		ids = ids[n:]
		args := make([]any, len(chunk))
		for i, v := range chunk {
			args[i] = v
		}
		rows, err := s.r.QueryContext(ctx, `SELECT id,name,service_id,category,bg FROM domains WHERE id IN (`+placeholders(len(chunk))+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var d Domain
			var bg int
			if err := rows.Scan(&d.ID, &d.Name, &d.ServiceID, &d.Category, &bg); err != nil {
				rows.Close()
				return nil, err
			}
			d.Background = bg != 0
			out[d.ID] = d
		}
		rows.Close()
	}
	return out, nil
}

// FPRow is a device fingerprint hit count.
type FPRow struct {
	Day    int64
	UserID int64
	IPID   int64
	Kind   string
	Value  string
	N      int64
}

// FP returns fingerprint rows for days in [fromDay, toDay). userID 0 means all.
func (s *Store) FP(ctx context.Context, userID, fromDay, toDay int64) ([]FPRow, error) {
	q := `SELECT day,user_id,ip_id,kind,value,n FROM fp WHERE day>=? AND day<?`
	args := []any{fromDay, toDay}
	if userID > 0 {
		q += ` AND user_id=?`
		args = append(args, userID)
	}
	rows, err := s.r.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FPRow
	for rows.Next() {
		var f FPRow
		if err := rows.Scan(&f.Day, &f.UserID, &f.IPID, &f.Kind, &f.Value, &f.N); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// TrafficPoint is upload/download in one slot.
type TrafficPoint struct {
	Slot   int64 `json:"slot"` // 5-minute or hour index depending on the call
	UserID int64 `json:"user_id"`
	NodeID int64 `json:"node_id"`
	Up     int64 `json:"up"`
	Down   int64 `json:"down"`
}

// TrafficHourly returns hourly traffic in [fromHour, toHour).
func (s *Store) TrafficHourly(ctx context.Context, userID, fromHour, toHour int64) ([]TrafficPoint, error) {
	return s.traffic(ctx, `SELECT hour,user_id,node_id,up,down FROM traffic_h WHERE hour>=? AND hour<?`, userID, fromHour, toHour)
}

// Traffic5 returns five-minute traffic in [from5, to5).
func (s *Store) Traffic5(ctx context.Context, userID, from5, to5 int64) ([]TrafficPoint, error) {
	return s.traffic(ctx, `SELECT slot,user_id,node_id,up,down FROM traffic5 WHERE slot>=? AND slot<?`, userID, from5, to5)
}

func (s *Store) traffic(ctx context.Context, q string, userID, from, to int64) ([]TrafficPoint, error) {
	args := []any{from, to}
	if userID > 0 {
		q += ` AND user_id=?`
		args = append(args, userID)
	}
	rows, err := s.r.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TrafficPoint
	for rows.Next() {
		var t TrafficPoint
		if err := rows.Scan(&t.Slot, &t.UserID, &t.NodeID, &t.Up, &t.Down); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// OldestTraffic5 returns the oldest 5-minute slot still stored (0 if none).
func (s *Store) OldestTraffic5(ctx context.Context) (int64, error) {
	var v sql.NullInt64
	err := s.r.QueryRowContext(ctx, `SELECT MIN(slot) FROM traffic5`).Scan(&v)
	return v.Int64, err
}

// Node is a remote collector connected to this server.
type Node struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Created  int64  `json:"created_at"`
	LastSeen int64  `json:"last_seen"`
	LastIP   string `json:"last_ip"`
	Version  string `json:"version"`
	Panel    string `json:"panel"`
	LinesIn  int64  `json:"lines_in"`
}

func hashToken(tok string) string {
	h := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(h[:])
}

// CreateNode registers a node and returns its one-time secret token.
func (s *Store) CreateNode(name string) (Node, string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 64 {
		return Node{}, "", errors.New("invalid node name")
	}
	var raw [24]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return Node{}, "", err
	}
	tok := "xs_" + hex.EncodeToString(raw[:])
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().Unix()
	res, err := s.w.Exec(`INSERT INTO nodes(name,token_hash,created_at) VALUES(?,?,?)`, name, hashToken(tok), now)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return Node{}, "", fmt.Errorf("a node named %q already exists", name)
		}
		return Node{}, "", err
	}
	id, _ := res.LastInsertId()
	return Node{ID: id, Name: name, Created: now}, tok, nil
}

// NodeByToken authenticates a node.
func (s *Store) NodeByToken(ctx context.Context, tok string) (Node, error) {
	var n Node
	err := s.r.QueryRowContext(ctx, `SELECT id,name,created_at,last_seen,last_ip,version,panel,lines_in FROM nodes WHERE token_hash=?`, hashToken(tok)).
		Scan(&n.ID, &n.Name, &n.Created, &n.LastSeen, &n.LastIP, &n.Version, &n.Panel, &n.LinesIn)
	return n, err
}

// TouchNode records node metadata on contact.
func (s *Store) TouchNode(id int64, ip, version, panel string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.w.Exec(`UPDATE nodes SET last_seen=?, last_ip=?, version=?, panel=? WHERE id=?`, time.Now().Unix(), ip, version, panel, id)
	return err
}

// Nodes lists registered nodes.
func (s *Store) Nodes(ctx context.Context) ([]Node, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT id,name,created_at,last_seen,last_ip,version,panel,lines_in FROM nodes ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Node
	for rows.Next() {
		var n Node
		if err := rows.Scan(&n.ID, &n.Name, &n.Created, &n.LastSeen, &n.LastIP, &n.Version, &n.Panel, &n.LinesIn); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// DeleteNode removes a node registration (its collected data is kept).
func (s *Store) DeleteNode(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.w.Exec(`DELETE FROM nodes WHERE id=?`, id)
	return err
}

// DeleteUserData removes every trace of a user from the database.
func (s *Store) DeleteUserData(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.w.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, t := range []string{"act", "svc", "visits", "fp", "traffic5", "traffic_h", "traffic_last", "events"} {
		if _, err := tx.Exec(`DELETE FROM `+t+` WHERE user_id=?`, id); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`DELETE FROM users WHERE id=?`, id); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.resetCaches()
	return nil
}

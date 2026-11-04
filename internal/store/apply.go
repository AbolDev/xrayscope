package store

import (
	"database/sql"
	"errors"
	"time"

	"xrayscope/internal/classify"
	"xrayscope/internal/model"
)

// Obs is the latest observed activity of one user from one IP.
type Obs struct {
	UserID int64
	Email  string
	IP     string
	Minute int64 // unix minute
}

type actKey struct{ hour, uid, ip int64 }
type actVal struct{ mask, imask, reqs int64 }
type svcRowKey struct{ hour, uid, svc int64 }
type visitKey struct{ hour, uid, dom int64 }
type maskVal struct{ mask, reqs int64 }
type fpKey struct {
	day, uid, ip int64
	kind, value  string
}
type obsKey struct {
	uid int64
	ip  string
}
type span struct{ first, last int64 }

const maxHostCache = 200000

// MinInteractiveReqs is how many requests to one site within a minute make
// that minute count as use of the site.
const MinInteractiveReqs = 2

type recentKey struct{ minute, user, dom int64 }

// pruneRecent forgets request counts of minutes that can no longer receive
// data.
func (s *Store) pruneRecent(now int64) {
	if len(s.recent) < 100000 {
		return
	}
	for k := range s.recent {
		if k.minute < now-10 {
			delete(s.recent, k)
		}
	}
}

// ApplyBatch folds aggregated access-log records into the database in a
// single transaction. kv entries (such as the log read offset) are written in
// the same transaction so a crash never loses or double-counts data.
func (s *Store) ApplyBatch(nodeID int64, recs []model.Rec, kv map[string]string, lines int64) ([]Obs, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.w.Begin()
	if err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if !committed {
			tx.Rollback()
			s.resetCaches() // cached ids may point at rolled-back rows
		}
	}()

	cls := s.cls.Load()
	act := map[actKey]*actVal{}
	svc := map[svcRowKey]*maskVal{}
	vis := map[visitKey]*maskVal{}
	fp := map[fpKey]int64{}
	seen := map[int64]*span{}
	obs := map[obsKey]int64{}
	emails := map[int64]string{}

	now := time.Now().Unix() / 60
	s.pruneRecent(now)
	for i := range recs {
		r := &recs[i]
		if r.M <= 0 || r.M > now+1440 || r.U == "" || r.N <= 0 {
			continue
		}
		uid, err := s.userID(tx, r.U)
		if err != nil {
			return nil, err
		}
		ipid, err := s.ipID(tx, r.IP)
		if err != nil {
			return nil, err
		}
		hi, err := s.hostInfo(tx, cls, r.D)
		if err != nil {
			return nil, err
		}
		emails[uid] = r.U
		hour, bit := r.M/60, int64(1)<<uint(r.M%60)
		n := int64(r.N)

		// A single stray request to a site in a minute is usually a widget,
		// a tracker or an app syncing in the background, not the person
		// using that site. Counting across batches, the minute only counts as
		// use of the site once it saw at least MinInteractiveReqs requests.
		rk := recentKey{r.M, uid, hi.domID}
		cum := s.recent[rk] + r.N
		s.recent[rk] = cum
		engaged := hi.bg || cum >= MinInteractiveReqs

		a := act[actKey{hour, uid, ipid}]
		if a == nil {
			a = &actVal{}
			act[actKey{hour, uid, ipid}] = a
		}
		a.mask |= bit
		if !hi.bg && engaged {
			a.imask |= bit
		}
		a.reqs += n

		if engaged {
			sv := svc[svcRowKey{hour, uid, hi.svcID}]
			if sv == nil {
				sv = &maskVal{}
				svc[svcRowKey{hour, uid, hi.svcID}] = sv
			}
			sv.mask |= bit
			sv.reqs += n
		}

		v := vis[visitKey{hour, uid, hi.domID}]
		if v == nil {
			v = &maskVal{}
			vis[visitKey{hour, uid, hi.domID}] = v
		}
		v.mask |= bit
		v.reqs += n

		for _, h := range hi.hints {
			fp[fpKey{r.M / 1440, uid, ipid, h.Kind, h.Value}] += n * int64(h.Weight)
		}
		sp := seen[uid]
		if sp == nil {
			seen[uid] = &span{r.M, r.M}
		} else {
			if r.M < sp.first {
				sp.first = r.M
			}
			if r.M > sp.last {
				sp.last = r.M
			}
		}
		if r.M > obs[obsKey{uid, r.IP}] {
			obs[obsKey{uid, r.IP}] = r.M
		}
	}

	if len(act) > 0 {
		st, err := tx.Prepare(`INSERT INTO act(hour,user_id,ip_id,node_id,mask,imask,reqs) VALUES(?,?,?,?,?,?,?)
			ON CONFLICT(hour,user_id,ip_id,node_id) DO UPDATE SET
			mask=mask|excluded.mask, imask=imask|excluded.imask, reqs=reqs+excluded.reqs`)
		if err != nil {
			return nil, err
		}
		for k, v := range act {
			if _, err := st.Exec(k.hour, k.uid, k.ip, nodeID, v.mask, v.imask, v.reqs); err != nil {
				st.Close()
				return nil, err
			}
		}
		st.Close()
	}
	if len(svc) > 0 {
		st, err := tx.Prepare(`INSERT INTO svc(hour,user_id,service_id,mask,reqs) VALUES(?,?,?,?,?)
			ON CONFLICT(hour,user_id,service_id) DO UPDATE SET mask=mask|excluded.mask, reqs=reqs+excluded.reqs`)
		if err != nil {
			return nil, err
		}
		for k, v := range svc {
			if _, err := st.Exec(k.hour, k.uid, k.svc, v.mask, v.reqs); err != nil {
				st.Close()
				return nil, err
			}
		}
		st.Close()
	}
	if len(vis) > 0 {
		st, err := tx.Prepare(`INSERT INTO visits(slot,user_id,domain_id,g,mask,reqs) VALUES(?,?,?,0,?,?)
			ON CONFLICT(slot,user_id,domain_id,g) DO UPDATE SET mask=mask|excluded.mask, reqs=reqs+excluded.reqs`)
		if err != nil {
			return nil, err
		}
		for k, v := range vis {
			if _, err := st.Exec(k.hour, k.uid, k.dom, v.mask, v.reqs); err != nil {
				st.Close()
				return nil, err
			}
		}
		st.Close()
	}
	if len(fp) > 0 {
		st, err := tx.Prepare(`INSERT INTO fp(day,user_id,ip_id,kind,value,n) VALUES(?,?,?,?,?,?)
			ON CONFLICT(day,user_id,ip_id,kind,value) DO UPDATE SET n=n+excluded.n`)
		if err != nil {
			return nil, err
		}
		for k, n := range fp {
			if _, err := st.Exec(k.day, k.uid, k.ip, k.kind, k.value, n); err != nil {
				st.Close()
				return nil, err
			}
		}
		st.Close()
	}
	for uid, sp := range seen {
		if _, err := tx.Exec(`UPDATE users SET
			first_seen = CASE WHEN first_seen=0 OR first_seen>? THEN ? ELSE first_seen END,
			last_seen  = MAX(last_seen, ?) WHERE id=?`,
			sp.first*60, sp.first*60, sp.last*60, uid); err != nil {
			return nil, err
		}
	}
	for k, v := range kv {
		if _, err := tx.Exec(`INSERT INTO kv(k,v) VALUES(?,?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, k, v); err != nil {
			return nil, err
		}
	}
	if nodeID > 0 {
		if _, err := tx.Exec(`UPDATE nodes SET last_seen=?, lines_in=lines_in+? WHERE id=?`, time.Now().Unix(), lines, nodeID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	committed = true

	out := make([]Obs, 0, len(obs))
	for k, m := range obs {
		out = append(out, Obs{UserID: k.uid, Email: emails[k.uid], IP: k.ip, Minute: m})
	}
	return out, nil
}

func (s *Store) userID(tx *sql.Tx, email string) (int64, error) {
	if id, ok := s.users[email]; ok {
		return id, nil
	}
	if _, err := tx.Exec(`INSERT INTO users(email) VALUES(?) ON CONFLICT(email) DO NOTHING`, email); err != nil {
		return 0, err
	}
	var id int64
	if err := tx.QueryRow(`SELECT id FROM users WHERE email=?`, email).Scan(&id); err != nil {
		return 0, err
	}
	s.users[email] = id
	return id, nil
}

func (s *Store) ipID(tx *sql.Tx, ip string) (int64, error) {
	if id, ok := s.ips[ip]; ok {
		return id, nil
	}
	var id int64
	err := tx.QueryRow(`SELECT id FROM ips WHERE ip=?`, ip).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		var country, org string
		var asn int
		if g := s.geo.Load(); g != nil {
			country, asn, org = (*g)(ip)
		}
		res, err := tx.Exec(`INSERT INTO ips(ip,country,asn,org) VALUES(?,?,?,?)`, ip, country, asn, org)
		if err != nil {
			return 0, err
		}
		id, err = res.LastInsertId()
		if err != nil {
			return 0, err
		}
	} else if err != nil {
		return 0, err
	}
	s.ips[ip] = id
	return id, nil
}

func (s *Store) serviceID(tx *sql.Tx, category, name string) (int64, error) {
	k := svcKey{category, name}
	if id, ok := s.svcs[k]; ok {
		return id, nil
	}
	if _, err := tx.Exec(`INSERT INTO services(category,name) VALUES(?,?) ON CONFLICT(category,name) DO NOTHING`, category, name); err != nil {
		return 0, err
	}
	var id int64
	if err := tx.QueryRow(`SELECT id FROM services WHERE category=? AND name=?`, category, name).Scan(&id); err != nil {
		return 0, err
	}
	s.svcs[k] = id
	return id, nil
}

// classifyHost classifies a host name or address. A bare address that no rule
// knows is tried against its network owner, so a connection without a domain
// name can still be told apart (Telegram, Netflix, game servers).
func (s *Store) classifyHost(cls *classify.Classifier, host string) classify.Info {
	info := cls.Classify(host)
	if info.Category != "ip" {
		return info
	}
	if g := s.geo.Load(); g != nil {
		if _, _, org := (*g)(host); org != "" {
			if cat, svc, ok := classify.OrgHint(org); ok {
				return classify.Info{Site: svc, Category: cat, Service: svc, Background: classify.IsBackground(cat)}
			}
		}
	}
	return info
}

func (s *Store) hostInfo(tx *sql.Tx, cls *classify.Classifier, host string) (hostInfo, error) {
	if hi, ok := s.hosts[host]; ok {
		return hi, nil
	}
	info := s.classifyHost(cls, host)
	svcID, err := s.serviceID(tx, info.Category, info.Service)
	if err != nil {
		return hostInfo{}, err
	}
	domID, ok := s.sites[info.Site]
	if !ok {
		bg := 0
		if info.Background {
			bg = 1
		}
		if _, err := tx.Exec(`INSERT INTO domains(name,service_id,category,bg) VALUES(?,?,?,?) ON CONFLICT(name) DO NOTHING`,
			info.Site, svcID, info.Category, bg); err != nil {
			return hostInfo{}, err
		}
		if err := tx.QueryRow(`SELECT id FROM domains WHERE name=?`, info.Site).Scan(&domID); err != nil {
			return hostInfo{}, err
		}
		s.sites[info.Site] = domID
	}
	if len(s.hosts) >= maxHostCache {
		s.hosts = map[string]hostInfo{}
	}
	hi := hostInfo{domID: domID, svcID: svcID, bg: info.Background, hints: cls.Hints(host)}
	s.hosts[host] = hi
	return hi, nil
}

// ApplyTraffic stores upload/download deltas. totals, when not nil, replaces
// the remembered cumulative counters used to compute the next deltas.
func (s *Store) ApplyTraffic(nodeID int64, deltas []model.TrafficRec, totals map[string][2]int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.w.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			tx.Rollback()
			s.resetCaches()
		}
	}()

	if len(deltas) > 0 {
		st5, err := tx.Prepare(`INSERT INTO traffic5(slot,user_id,node_id,up,down) VALUES(?,?,?,?,?)
			ON CONFLICT(slot,user_id,node_id) DO UPDATE SET up=up+excluded.up, down=down+excluded.down`)
		if err != nil {
			return err
		}
		defer st5.Close()
		sth, err := tx.Prepare(`INSERT INTO traffic_h(hour,user_id,node_id,up,down) VALUES(?,?,?,?,?)
			ON CONFLICT(hour,user_id,node_id) DO UPDATE SET up=up+excluded.up, down=down+excluded.down`)
		if err != nil {
			return err
		}
		defer sth.Close()
		for _, d := range deltas {
			if d.U == "" || (d.Up <= 0 && d.Down <= 0) || d.T <= 0 {
				continue
			}
			uid, err := s.userID(tx, d.U)
			if err != nil {
				return err
			}
			if _, err := st5.Exec(d.T/300, uid, nodeID, d.Up, d.Down); err != nil {
				return err
			}
			if _, err := sth.Exec(d.T/3600, uid, nodeID, d.Up, d.Down); err != nil {
				return err
			}
		}
	}
	if len(totals) > 0 {
		st, err := tx.Prepare(`INSERT INTO traffic_last(user_id,node_id,up,down) VALUES(?,?,?,?)
			ON CONFLICT(user_id,node_id) DO UPDATE SET up=excluded.up, down=excluded.down`)
		if err != nil {
			return err
		}
		defer st.Close()
		for email, t := range totals {
			uid, err := s.userID(tx, email)
			if err != nil {
				return err
			}
			if _, err := st.Exec(uid, nodeID, t[0], t[1]); err != nil {
				return err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (s *Store) resetCaches() {
	s.users = map[string]int64{}
	s.ips = map[string]int64{}
	s.hosts = map[string]hostInfo{}
	s.sites = map[string]int64{}
	s.svcs = map[svcKey]int64{}
	s.recent = map[recentKey]int{}
}

// TrafficLast returns the remembered cumulative counters per email.
func (s *Store) TrafficLast(nodeID int64) (map[string][2]int64, error) {
	rows, err := s.r.Query(`SELECT u.email, t.up, t.down FROM traffic_last t JOIN users u ON u.id=t.user_id WHERE t.node_id=?`, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][2]int64{}
	for rows.Next() {
		var e string
		var up, down int64
		if err := rows.Scan(&e, &up, &down); err != nil {
			return nil, err
		}
		out[e] = [2]int64{up, down}
	}
	return out, rows.Err()
}

// UpsertPanelUsers copies account state from the VPN panel.
func (s *Store) UpsertPanelUsers(users []model.PanelUser) error {
	if len(users) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.w.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, u := range users {
		if u.Email == "" {
			continue
		}
		en := 0
		if u.Enabled {
			en = 1
		}
		if _, err := tx.Exec(`INSERT INTO users(email,enabled,expiry,quota,up_total,down_total,limit_ip,inbound)
			VALUES(?,?,?,?,?,?,?,?)
			ON CONFLICT(email) DO UPDATE SET enabled=excluded.enabled, expiry=excluded.expiry, quota=excluded.quota,
			up_total=excluded.up_total, down_total=excluded.down_total, limit_ip=excluded.limit_ip, inbound=excluded.inbound`,
			u.Email, en, u.Expiry, u.Quota, u.Up, u.Down, u.LimitIP, u.Inbound); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Compact merges old hourly visit rows into daily rows and drops old
// five-minute traffic slots.
func (s *Store) Compact(visitsOlderThan time.Time, traffic5OlderThan time.Time) (merged int64, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := visitsOlderThan.Unix() / 86400 * 24 // first hour of the cutoff day
	tx, err := s.w.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`INSERT INTO visits(slot,user_id,domain_id,g,mask,reqs)
		SELECT (slot/24)*24, user_id, domain_id, 1, SUM(DISTINCT (1<<(slot%24))), SUM(reqs)
		FROM visits WHERE g=0 AND slot<? GROUP BY slot/24, user_id, domain_id
		ON CONFLICT(slot,user_id,domain_id,g) DO UPDATE SET mask=mask|excluded.mask, reqs=reqs+excluded.reqs`, cutoff)
	if err != nil {
		return 0, err
	}
	merged, _ = res.RowsAffected()
	if _, err := tx.Exec(`DELETE FROM visits WHERE g=0 AND slot<?`, cutoff); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`DELETE FROM traffic5 WHERE slot<?`, traffic5OlderThan.Unix()/300); err != nil {
		return 0, err
	}
	return merged, tx.Commit()
}

// Reclassify re-applies the active rules to every stored domain.
func (s *Store) Reclassify() (changed int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cls := s.cls.Load()
	rows, err := s.w.Query(`SELECT id,name,category,service_id FROM domains`)
	if err != nil {
		return 0, err
	}
	type row struct {
		id, svc   int64
		name, cat string
	}
	var all []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.name, &r.cat, &r.svc); err != nil {
			rows.Close()
			return 0, err
		}
		all = append(all, r)
	}
	rows.Close()
	tx, err := s.w.Begin()
	if err != nil {
		return 0, err
	}
	defer func() {
		tx.Rollback()
		s.resetCaches()
	}()
	for _, r := range all {
		info := s.classifyHost(cls, r.name)
		svcID, err := s.serviceID(tx, info.Category, info.Service)
		if err != nil {
			return 0, err
		}
		if info.Category == r.cat && svcID == r.svc {
			continue
		}
		bg := 0
		if info.Background {
			bg = 1
		}
		if _, err := tx.Exec(`UPDATE domains SET category=?, service_id=?, bg=? WHERE id=?`, info.Category, svcID, bg, r.id); err != nil {
			return 0, err
		}
		changed++
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	s.hosts = map[string]hostInfo{}
	return changed, nil
}

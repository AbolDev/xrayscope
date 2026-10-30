package store

// Time is stored as hour slots (unix/3600). Within an hour, the set of active
// minutes is kept as a 60-bit mask, so one row per (hour, user, ip) captures
// minute-level presence with almost no storage cost.
const schemaV1 = `
CREATE TABLE IF NOT EXISTS kv (
	k TEXT PRIMARY KEY,
	v TEXT NOT NULL
) WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS nodes (
	id         INTEGER PRIMARY KEY,
	name       TEXT NOT NULL UNIQUE,
	token_hash TEXT NOT NULL,
	created_at INTEGER NOT NULL,
	last_seen  INTEGER NOT NULL DEFAULT 0,
	last_ip    TEXT NOT NULL DEFAULT '',
	version    TEXT NOT NULL DEFAULT '',
	panel      TEXT NOT NULL DEFAULT '',
	lines_in   INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS users (
	id         INTEGER PRIMARY KEY,
	email      TEXT NOT NULL UNIQUE,
	first_seen INTEGER NOT NULL DEFAULT 0,
	last_seen  INTEGER NOT NULL DEFAULT 0,
	enabled    INTEGER NOT NULL DEFAULT 1,
	expiry     INTEGER NOT NULL DEFAULT 0,
	quota      INTEGER NOT NULL DEFAULT 0,
	up_total   INTEGER NOT NULL DEFAULT 0,
	down_total INTEGER NOT NULL DEFAULT 0,
	limit_ip   INTEGER NOT NULL DEFAULT 0,
	inbound    TEXT NOT NULL DEFAULT '',
	note       TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS ips (
	id      INTEGER PRIMARY KEY,
	ip      TEXT NOT NULL UNIQUE,
	country TEXT NOT NULL DEFAULT '',
	asn     INTEGER NOT NULL DEFAULT 0,
	org     TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS services (
	id       INTEGER PRIMARY KEY,
	category TEXT NOT NULL,
	name     TEXT NOT NULL,
	UNIQUE (category, name)
);

CREATE TABLE IF NOT EXISTS domains (
	id         INTEGER PRIMARY KEY,
	name       TEXT NOT NULL UNIQUE,
	service_id INTEGER NOT NULL,
	category   TEXT NOT NULL,
	bg         INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS domains_category ON domains(category);

-- Presence per user and client IP. mask: minutes with any traffic,
-- imask: minutes with interactive (non-background) traffic.
CREATE TABLE IF NOT EXISTS act (
	hour    INTEGER NOT NULL,
	user_id INTEGER NOT NULL,
	ip_id   INTEGER NOT NULL,
	node_id INTEGER NOT NULL DEFAULT 0,
	mask    INTEGER NOT NULL,
	imask   INTEGER NOT NULL,
	reqs    INTEGER NOT NULL,
	PRIMARY KEY (hour, user_id, ip_id, node_id)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS act_user ON act(user_id, hour);
CREATE INDEX IF NOT EXISTS act_ip ON act(ip_id, hour);

-- Time spent per service (minute masks per hour).
CREATE TABLE IF NOT EXISTS svc (
	hour       INTEGER NOT NULL,
	user_id    INTEGER NOT NULL,
	service_id INTEGER NOT NULL,
	mask       INTEGER NOT NULL,
	reqs       INTEGER NOT NULL,
	PRIMARY KEY (hour, user_id, service_id)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS svc_user ON svc(user_id, hour);

-- Sites visited. g=0: hourly rows (mask = minutes); g=1: compacted daily
-- rows (slot = first hour of the UTC day, mask = hours of the day).
CREATE TABLE IF NOT EXISTS visits (
	slot      INTEGER NOT NULL,
	user_id   INTEGER NOT NULL,
	domain_id INTEGER NOT NULL,
	g         INTEGER NOT NULL DEFAULT 0,
	mask      INTEGER NOT NULL,
	reqs      INTEGER NOT NULL,
	PRIMARY KEY (slot, user_id, domain_id, g)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS visits_user ON visits(user_id, slot);
CREATE INDEX IF NOT EXISTS visits_domain ON visits(domain_id, slot);

-- Device fingerprint hits per day, user and IP.
CREATE TABLE IF NOT EXISTS fp (
	day     INTEGER NOT NULL,
	user_id INTEGER NOT NULL,
	ip_id   INTEGER NOT NULL,
	kind    TEXT NOT NULL,
	value   TEXT NOT NULL,
	n       INTEGER NOT NULL,
	PRIMARY KEY (day, user_id, ip_id, kind, value)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS fp_user ON fp(user_id, day);

-- Upload / download. traffic5: 5-minute slots (short retention),
-- traffic_h: hourly slots (kept forever).
CREATE TABLE IF NOT EXISTS traffic5 (
	slot    INTEGER NOT NULL,
	user_id INTEGER NOT NULL,
	node_id INTEGER NOT NULL DEFAULT 0,
	up      INTEGER NOT NULL,
	down    INTEGER NOT NULL,
	PRIMARY KEY (slot, user_id, node_id)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS traffic5_user ON traffic5(user_id, slot);

CREATE TABLE IF NOT EXISTS traffic_h (
	hour    INTEGER NOT NULL,
	user_id INTEGER NOT NULL,
	node_id INTEGER NOT NULL DEFAULT 0,
	up      INTEGER NOT NULL,
	down    INTEGER NOT NULL,
	PRIMARY KEY (hour, user_id, node_id)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS traffic_h_user ON traffic_h(user_id, hour);

-- Last cumulative counters read from the panel, to compute deltas.
CREATE TABLE IF NOT EXISTS traffic_last (
	user_id INTEGER NOT NULL,
	node_id INTEGER NOT NULL DEFAULT 0,
	up      INTEGER NOT NULL,
	down    INTEGER NOT NULL,
	PRIMARY KEY (user_id, node_id)
) WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS events (
	id      INTEGER PRIMARY KEY,
	ts      INTEGER NOT NULL,
	kind    TEXT NOT NULL,
	user_id INTEGER NOT NULL DEFAULT 0,
	node_id INTEGER NOT NULL DEFAULT 0,
	text    TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS events_ts ON events(ts);
CREATE INDEX IF NOT EXISTS events_user ON events(user_id, ts);
`

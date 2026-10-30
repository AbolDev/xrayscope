package panel

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func fixtureDB(t *testing.T, template string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "x-ui.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stmts := []string{
		`CREATE TABLE inbounds (id INTEGER PRIMARY KEY, remark TEXT, port INTEGER, settings TEXT)`,
		`CREATE TABLE client_traffics (id INTEGER PRIMARY KEY, inbound_id INTEGER, enable NUMERIC, email TEXT, up INTEGER, down INTEGER, expiry_time INTEGER, total INTEGER)`,
		`CREATE TABLE settings (id INTEGER PRIMARY KEY, key TEXT, value TEXT)`,
		`INSERT INTO inbounds VALUES (1, 'main', 443, '{"clients":[{"email":"alice","limitIp":2,"totalGB":100},{"email":"bob"}]}')`,
		`INSERT INTO inbounds VALUES (2, '', 8443, '{"clients":[]}')`,
		`INSERT INTO client_traffics VALUES (1, 1, 1, 'alice', 1000, 5000, 1893456000000, 107374182400)`,
		`INSERT INTO client_traffics VALUES (2, 1, 0, 'bob', 1, 2, 0, 0)`,
		`INSERT INTO client_traffics VALUES (3, 2, 1, 'carol', 7, 8, 0, 0)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatal(err)
		}
	}
	if template != "" {
		if _, err := db.Exec(`INSERT INTO settings(key,value) VALUES('xrayTemplateConfig', ?)`, template); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestXUIUsers(t *testing.T) {
	x := NewXUI(fixtureDB(t, ""))
	users, err := x.Users(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 3 {
		t.Fatalf("users = %+v", users)
	}
	a := users[0]
	if a.Email != "alice" || !a.Enabled || a.Up != 1000 || a.Down != 5000 || a.LimitIP != 2 || a.Expiry != 1893456000 || a.Quota != 107374182400 || a.Inbound != "main" {
		t.Fatalf("alice = %+v", a)
	}
	if users[1].Enabled {
		t.Fatalf("bob must be disabled: %+v", users[1])
	}
	if users[2].Inbound != "port 8443" {
		t.Fatalf("carol inbound = %q", users[2].Inbound)
	}
}

func TestXUIAccessLogDisabled(t *testing.T) {
	x := NewXUI(fixtureDB(t, `{"log":{"access":"none","loglevel":"warning"}}`))
	info, err := x.AccessLog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !info.Disabled {
		t.Fatalf("info = %+v", info)
	}
}

func TestXUIAccessLogAbsolute(t *testing.T) {
	log := filepath.Join(t.TempDir(), "access.log")
	writeFile(t, log)
	x := NewXUI(fixtureDB(t, `{"log":{"access":"`+filepath.ToSlash(log)+`"}}`))
	info, err := x.AccessLog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Disabled || filepath.ToSlash(info.Path) != filepath.ToSlash(log) {
		t.Fatalf("info = %+v", info)
	}
}

func TestXUIAccessLogPrefersRuntimeConfig(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "access.log")
	writeFile(t, real)
	rt := filepath.Join(dir, "config.json")
	if err := os.WriteFile(rt, []byte(`{"log":{"access":"`+filepath.ToSlash(real)+`"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// the template still says "./access.log", as 3x-ui stores it
	x := NewXUI(fixtureDB(t, `{"log":{"access":"./access.log"}}`))
	x.runtimeConfigs = []string{rt}
	info, err := x.AccessLog(context.Background())
	if err != nil || filepath.ToSlash(info.Path) != filepath.ToSlash(real) {
		t.Fatalf("info = %+v err=%v", info, err)
	}
	// "none" in the config Xray runs with disables collection even if the template names a file
	os.WriteFile(rt, []byte(`{"log":{"access":"none"}}`), 0o600)
	info, _ = x.AccessLog(context.Background())
	if !info.Disabled {
		t.Fatalf("expected disabled: %+v", info)
	}
}

func TestMissingDatabase(t *testing.T) {
	if _, err := NewXUI(filepath.Join(t.TempDir(), "none.db")).Users(context.Background()); err == nil {
		t.Fatal("expected an error for a missing database")
	}
}

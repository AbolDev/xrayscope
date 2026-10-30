package panel

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"xrayscope/internal/model"

	_ "modernc.org/sqlite"
)

// XUI reads the 3x-ui SQLite database (read-only).
type XUI struct {
	dbPath         string
	runtimeConfigs []string // generated Xray configs to consult first (tests)
}

// NewXUI creates the adapter. dbPath is usually /etc/x-ui/x-ui.db.
func NewXUI(dbPath string) *XUI { return &XUI{dbPath: dbPath} }

// Name implements Panel.
func (x *XUI) Name() string { return "3x-ui" }

func (x *XUI) open() (*sql.DB, error) {
	if _, err := os.Stat(x.dbPath); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", x.dbPath+"?_pragma=busy_timeout(5000)&_pragma=query_only(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

type xuiClient struct {
	Email      string `json:"email"`
	LimitIP    int    `json:"limitIp"`
	TotalGB    int64  `json:"totalGB"`
	ExpiryTime int64  `json:"expiryTime"`
	Enable     *bool  `json:"enable"`
}

// Users implements Panel.
func (x *XUI) Users(ctx context.Context) ([]model.PanelUser, error) {
	db, err := x.open()
	if err != nil {
		return nil, err
	}
	defer db.Close()

	// per-client settings (limitIp lives in the inbound's settings JSON)
	type inb struct {
		remark string
		port   int
	}
	inbounds := map[int64]inb{}
	limits := map[string]int{}
	rows, err := db.QueryContext(ctx, `SELECT id, remark, port, settings FROM inbounds`)
	if err != nil {
		return nil, fmt.Errorf("read inbounds: %w", err)
	}
	for rows.Next() {
		var id int64
		var remark, settings sql.NullString
		var port sql.NullInt64
		if err := rows.Scan(&id, &remark, &port, &settings); err != nil {
			rows.Close()
			return nil, err
		}
		inbounds[id] = inb{remark.String, int(port.Int64)}
		var s struct {
			Clients []xuiClient `json:"clients"`
		}
		if json.Unmarshal([]byte(settings.String), &s) == nil {
			for _, c := range s.Clients {
				if c.Email != "" && c.LimitIP > 0 {
					limits[c.Email] = c.LimitIP
				}
			}
		}
	}
	rows.Close()

	rows, err = db.QueryContext(ctx, `SELECT inbound_id, enable, email, up, down, expiry_time, total FROM client_traffics`)
	if err != nil {
		return nil, fmt.Errorf("read client_traffics: %w", err)
	}
	defer rows.Close()
	var out []model.PanelUser
	for rows.Next() {
		var inboundID, up, down, expiry, total sql.NullInt64
		var enable sql.NullBool
		var email sql.NullString
		if err := rows.Scan(&inboundID, &enable, &email, &up, &down, &expiry, &total); err != nil {
			return nil, err
		}
		if email.String == "" {
			continue
		}
		u := model.PanelUser{
			Email:   email.String,
			Enabled: !enable.Valid || enable.Bool,
			Up:      up.Int64,
			Down:    down.Int64,
			Quota:   total.Int64,
			LimitIP: limits[email.String],
		}
		if expiry.Int64 > 0 {
			u.Expiry = expiry.Int64 / 1000 // stored in milliseconds
		}
		if in, ok := inbounds[inboundID.Int64]; ok {
			u.Inbound = in.remark
			if u.Inbound == "" {
				u.Inbound = fmt.Sprintf("port %d", in.port)
			}
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// AccessLog implements Panel. 3x-ui keeps the Xray configuration template in
// its settings table; log.access holds the access log path.
func (x *XUI) AccessLog(ctx context.Context) (LogInfo, error) {
	var info LogInfo
	db, err := x.open()
	if err != nil {
		return info, err
	}
	defer db.Close()
	var tmpl sql.NullString
	err = db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key='xrayTemplateConfig'`).Scan(&tmpl)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return info, err
	}
	var cfg struct {
		Log struct {
			Access string `json:"access"`
		} `json:"log"`
	}
	if tmpl.Valid {
		_ = json.Unmarshal([]byte(tmpl.String), &cfg)
	}
	info.Setting = cfg.Log.Access
	// The config Xray was actually started with is authoritative: 3x-ui
	// rewrites relative paths of the template (to /var/log/x-ui, ...).
	if rt, ok := x.runtimeAccessLog(); ok {
		info.Setting = rt
	}
	if info.Setting == "" || strings.EqualFold(info.Setting, "none") {
		info.Disabled = true
		return info, nil
	}
	info.Path, info.Tried = resolveLogPath(info.Setting)
	return info, nil
}

// runtimeAccessLog reads log.access from the generated Xray config.
func (x *XUI) runtimeAccessLog() (string, bool) {
	paths := x.runtimeConfigs
	if len(paths) == 0 {
		paths = []string{"/usr/local/x-ui/bin/config.json"}
		if dir := os.Getenv("XUI_BIN_FOLDER"); dir != "" {
			paths = append([]string{filepath.Join(dir, "config.json")}, paths...)
		}
	}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var c struct {
			Log struct {
				Access string `json:"access"`
			} `json:"log"`
		}
		if json.Unmarshal(b, &c) == nil {
			return c.Log.Access, true
		}
	}
	return "", false
}

// resolveLogPath finds an access log named by a (possibly relative) setting.
func resolveLogPath(setting string) (string, []string) {
	var cands []string
	if filepath.IsAbs(setting) {
		cands = []string{setting}
	} else {
		rel := strings.TrimPrefix(filepath.ToSlash(setting), "./")
		for _, dir := range []string{"/var/log/x-ui", "/usr/local/x-ui/bin", "/usr/local/x-ui", "/etc/x-ui"} {
			cands = append(cands, filepath.Join(dir, rel))
		}
	}
	for _, c := range cands {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c, cands
		}
	}
	return "", cands
}

// Package config loads the bootstrap configuration file. Everything that an
// administrator edits at runtime lives in the settings stored in the database.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Config is the bootstrap configuration.
type Config struct {
	// Role is "server" (collector + web panel, default) or "agent" (a node
	// that ships its log to a main server).
	Role string `json:"role"`

	Listen   string `json:"listen"`    // web panel address, default 0.0.0.0:8686
	BasePath string `json:"base_path"` // URL prefix when served behind a reverse proxy
	DataDir  string `json:"data_dir"`
	TLSCert  string `json:"tls_cert"`
	TLSKey   string `json:"tls_key"`
	// TrustProxy makes the server read the client address from
	// X-Forwarded-For / X-Real-IP (only enable behind your own reverse proxy).
	TrustProxy bool `json:"trust_proxy"`

	Panel Panel `json:"panel"`

	// LogPath is the Xray access log. "auto" asks the panel adapter.
	LogPath string `json:"log_path"`
	// LogTimezone is the zone of the timestamps in the access log, "Local"
	// (the server zone) by default.
	LogTimezone string `json:"log_timezone"`

	// Proxy is the default outbound proxy (http://, https://, socks5://).
	Proxy string `json:"proxy"`

	GeoDB    string `json:"geo_db"` // optional MaxMind/DB-IP country mmdb
	GeoASNDB string `json:"asn_db"` // optional ASN mmdb

	// Agent settings (role = "agent").
	ServerURL      string `json:"server_url"`
	Token          string `json:"token"`
	NodeName       string `json:"node_name"`
	InsecureTLS    bool   `json:"insecure_tls"`
	ServerProxy    string `json:"server_proxy"` // overrides Proxy for the main server connection
	ServerHostname string `json:"server_sni"`   // TLS server name override (SNI)
}

// Panel describes the VPN panel installed next to this process.
type Panel struct {
	Type string `json:"type"` // "3x-ui" (default), "none"
	// DBPath is the panel database, /etc/x-ui/x-ui.db for 3x-ui.
	DBPath string `json:"db_path"`
	// PollSeconds is how often traffic counters and accounts are read.
	PollSeconds int `json:"poll_seconds"`
}

// Default returns the default configuration.
func Default() Config {
	return Config{
		Role:        "server",
		Listen:      "0.0.0.0:8686",
		DataDir:     "/var/lib/xrayscope",
		LogPath:     "auto",
		LogTimezone: "Local",
		Panel:       Panel{Type: "3x-ui", DBPath: "/etc/x-ui/x-ui.db", PollSeconds: 30},
	}
}

// Load reads path (JSON). A missing file yields defaults. Environment
// variables XRAYSCOPE_LISTEN, XRAYSCOPE_DATA_DIR override the file.
func Load(path string) (Config, error) {
	c := Default()
	if path != "" {
		b, err := os.ReadFile(path)
		switch {
		case err == nil:
			if err := json.Unmarshal(b, &c); err != nil {
				return c, fmt.Errorf("%s: %w", path, err)
			}
		case errors.Is(err, os.ErrNotExist):
		default:
			return c, err
		}
	}
	if v := os.Getenv("XRAYSCOPE_LISTEN"); v != "" {
		c.Listen = v
	}
	if v := os.Getenv("XRAYSCOPE_DATA_DIR"); v != "" {
		c.DataDir = v
	}
	return c, c.Validate()
}

// Validate normalises and checks the configuration.
func (c *Config) Validate() error {
	if c.Role == "" {
		c.Role = "server"
	}
	if c.Role != "server" && c.Role != "agent" {
		return fmt.Errorf("role must be \"server\" or \"agent\", got %q", c.Role)
	}
	if c.Listen == "" {
		c.Listen = "0.0.0.0:8686"
	}
	c.BasePath = strings.Trim(c.BasePath, "/")
	if c.BasePath != "" {
		c.BasePath = "/" + c.BasePath
	}
	if c.DataDir == "" {
		return errors.New("data_dir is required")
	}
	if c.Panel.Type == "" {
		c.Panel.Type = "3x-ui"
	}
	if c.Panel.PollSeconds < 5 {
		c.Panel.PollSeconds = 30
	}
	if c.LogTimezone == "" {
		c.LogTimezone = "Local"
	}
	if c.Role == "agent" {
		if c.ServerURL == "" || c.Token == "" {
			return errors.New("agent role needs server_url and token")
		}
		if c.NodeName == "" {
			h, _ := os.Hostname()
			c.NodeName = h
		}
	}
	if (c.TLSCert == "") != (c.TLSKey == "") {
		return errors.New("tls_cert and tls_key must be set together")
	}
	return nil
}

// LogLocation resolves LogTimezone.
func (c *Config) LogLocation() (*time.Location, error) {
	if c.LogTimezone == "" || strings.EqualFold(c.LogTimezone, "local") {
		return time.Local, nil
	}
	return time.LoadLocation(c.LogTimezone)
}

// DBFile is the main database path.
func (c *Config) DBFile() string { return filepath.Join(c.DataDir, "xrayscope.db") }

// BackupDir is where scheduled backups are written.
func (c *Config) BackupDir() string { return filepath.Join(c.DataDir, "backups") }

// Save writes the configuration as JSON.
func (c *Config) Save(path string) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o600)
}

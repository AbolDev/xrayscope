// Package settings holds the runtime settings edited from the web panel.
package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"xrayscope/internal/classify"
)

const kvKey = "settings"

// Telegram configures the notification bot.
type Telegram struct {
	Enabled bool    `json:"enabled"`
	Token   string  `json:"token"`
	ChatIDs []int64 `json:"chat_ids"`
	Proxy   string  `json:"proxy"` // empty = use the global proxy
	// Commands lets the listed chats query the bot (/online, /user ...).
	Commands bool `json:"commands"`
	// APIBase allows a self-hosted Bot API server or relay.
	APIBase string `json:"api_base"`
}

// Alert is one alert rule.
type Alert struct {
	Enabled bool `json:"enabled"`
	// Value meaning depends on the rule (see below).
	Value    int `json:"value"`
	Cooldown int `json:"cooldown_min"`
}

// Alerts groups all alert rules.
type Alerts struct {
	// MaxIPs: Value = allowed simultaneous IPs, 0 = use the limit set in the
	// panel for each account.
	MaxIPs Alert `json:"max_ips"`
	// MinOverlapMin is how long IPs must overlap to count as simultaneous.
	MinOverlapMin int `json:"min_overlap_min"`
	// NewIP: notify when a user connects from an IP never seen before.
	NewIP Alert `json:"new_ip"`
	// NewCountry: notify when a user appears in a new country.
	NewCountry Alert `json:"new_country"`
	// TrafficSpike: Value = MB transferred by one user within an hour.
	TrafficSpike Alert `json:"traffic_spike"`
	// QuotaWarn: Value = percent of the quota used.
	QuotaWarn Alert `json:"quota_warn"`
	// ExpiryWarn: Value = days left.
	ExpiryWarn Alert `json:"expiry_warn"`
	// NodeOffline: Value = minutes without contact.
	NodeOffline Alert `json:"node_offline"`
	// LogStale: Value = minutes without any new log line.
	LogStale Alert `json:"log_stale"`
	// DailyDigest: Value = local hour to send the summary.
	DailyDigest Alert `json:"daily_digest"`
}

// Backup configures scheduled backups.
type Backup struct {
	Enabled       bool `json:"enabled"`
	IntervalHours int  `json:"interval_hours"`
	Keep          int  `json:"keep"`
	SendTelegram  bool `json:"send_telegram"`
}

// Retention controls compaction of high-resolution data.
type Retention struct {
	// VisitsHourlyDays: hourly site rows older than this are merged into
	// daily rows.
	VisitsHourlyDays int `json:"visits_hourly_days"`
	// Traffic5Days: five-minute traffic slots older than this are dropped
	// (hourly traffic is kept forever).
	Traffic5Days int `json:"traffic5_days"`
}

// Accuracy holds the thresholds behind time, presence and device figures, so
// they can be tuned against what is known about one's own use.
type Accuracy struct {
	// BridgeMin: a pause of up to this many minutes between two visits of a
	// site still counts as time on it (0 = off).
	BridgeMin int `json:"bridge_min"`
	// TrafficBridgeMax: longer pauses, up to this many minutes, count while
	// the user's connection kept moving data (0 = off) ...
	TrafficBridgeMax int `json:"traffic_bridge_max"`
	// TrafficShare: ... in at least this percent of the pause.
	TrafficShare int `json:"traffic_share"`
	// ActiveKB: data within one minute that makes it a minute of real use.
	ActiveKB int `json:"active_kb"`
	// HeavyKB: data within one minute that means sustained transfer (video).
	HeavyKB int `json:"heavy_kb"`
	// MinRequests: requests to one site within a minute before it counts as
	// using it (a lone request is usually an embedded widget or tracker).
	MinRequests int `json:"min_requests"`
	// CountPolling: count machine-regular background polling as time spent.
	CountPolling bool `json:"count_polling"`
	// SeparateIPChanges: treat a device's new address after a network change
	// as a separate device.
	SeparateIPChanges bool `json:"separate_ip_changes"`
}

// DefaultAccuracy returns the built-in thresholds.
//
//   - BridgeMin 5: browsers and apps keep HTTP/2 and QUIC connections open and
//     reuse them, so a page being read or a feed being scrolled can go a few
//     minutes without a new log line.
//   - TrafficBridgeMax 30: video and music players open new connections every
//     few minutes anyway; a longer silent stretch with traffic is more often
//     another app (a download, a backup) than the same site.
//   - TrafficShare 70: a stream keeps almost every minute busy; background
//     syncs do not.
//   - ActiveKB 30: push channels and keep-alives move a few KB a minute.
//   - HeavyKB 1024: about 140 kbit/s, the lowest video quality and most music
//     streams; text browsing stays well below it.
//   - MinRequests 2: one page view touches a site at least twice (page plus
//     resources, or TCP and QUIC tried together); an embedded widget or
//     tracker usually once.
func DefaultAccuracy() Accuracy {
	return Accuracy{BridgeMin: 5, TrafficBridgeMax: 30, TrafficShare: 70, ActiveKB: 30, HeavyKB: 1024, MinRequests: 2}
}

// previousDefaults are defaults of earlier versions. Settings that still hold
// one of them unchanged follow the current defaults.
var previousDefaults = []Accuracy{
	{BridgeMin: 4, TrafficBridgeMax: 45, TrafficShare: 70, ActiveKB: 30, HeavyKB: 1024, MinRequests: 2},
}

func (a *Accuracy) validate() error {
	if *a == (Accuracy{}) {
		*a = DefaultAccuracy() // settings saved before these existed
	}
	switch {
	case a.BridgeMin < 0 || a.BridgeMin > 15:
		return errors.New("accuracy: short pause must be 0-15 minutes")
	case a.TrafficBridgeMax < 0 || a.TrafficBridgeMax > 180:
		return errors.New("accuracy: pause with traffic must be 0-180 minutes")
	case a.TrafficShare < 30 || a.TrafficShare > 100:
		return errors.New("accuracy: traffic share must be 30-100 percent")
	case a.ActiveKB < 1 || a.ActiveKB > 10240:
		return errors.New("accuracy: active minute must be 1-10240 KB")
	case a.HeavyKB < a.ActiveKB || a.HeavyKB > 102400:
		return errors.New("accuracy: heavy minute must be at least the active minute and at most 102400 KB")
	case a.MinRequests < 1 || a.MinRequests > 10:
		return errors.New("accuracy: requests per minute must be 1-10")
	}
	return nil
}

// Settings is the whole runtime configuration.
type Settings struct {
	// Language of messages generated by the server (Telegram): "fa" or "en".
	Language        string          `json:"language"`
	DisplayTimezone string          `json:"display_timezone"`
	SessionGapMin   int             `json:"session_gap_min"`
	OnlineWindowMin int             `json:"online_window_min"`
	Telegram        Telegram        `json:"telegram"`
	Alerts          Alerts          `json:"alerts"`
	Backup          Backup          `json:"backup"`
	Retention       Retention       `json:"retention"`
	Accuracy        Accuracy        `json:"accuracy"`
	CustomRules     []classify.Rule `json:"custom_rules"`
}

// Defaults returns the default settings.
func Defaults() Settings {
	return Settings{
		Language:        "en",
		DisplayTimezone: "Local",
		SessionGapMin:   5,
		OnlineWindowMin: 3,
		Telegram:        Telegram{Commands: true},
		Alerts: Alerts{
			MaxIPs:        Alert{Enabled: true, Value: 0, Cooldown: 60},
			MinOverlapMin: 3,
			NewIP:         Alert{Enabled: false, Cooldown: 0},
			NewCountry:    Alert{Enabled: true, Cooldown: 0},
			TrafficSpike:  Alert{Enabled: false, Value: 2048, Cooldown: 180},
			QuotaWarn:     Alert{Enabled: true, Value: 90, Cooldown: 1440},
			ExpiryWarn:    Alert{Enabled: true, Value: 3, Cooldown: 1440},
			NodeOffline:   Alert{Enabled: true, Value: 10, Cooldown: 60},
			LogStale:      Alert{Enabled: true, Value: 30, Cooldown: 120},
			DailyDigest:   Alert{Enabled: false, Value: 9},
		},
		Backup:    Backup{Enabled: true, IntervalHours: 24, Keep: 7},
		Retention: Retention{VisitsHourlyDays: 30, Traffic5Days: 14},
		Accuracy:  DefaultAccuracy(),
	}
}

// Validate checks values and fills defaults for zero values.
func (s *Settings) Validate() error {
	d := Defaults()
	if s.Language == "" {
		s.Language = d.Language
	}
	if s.Language != "fa" && s.Language != "en" {
		return errors.New("language must be \"fa\" or \"en\"")
	}
	if s.DisplayTimezone == "" {
		s.DisplayTimezone = d.DisplayTimezone
	}
	if _, err := s.Location(); err != nil {
		return fmt.Errorf("display_timezone: %w", err)
	}
	if s.SessionGapMin < 1 || s.SessionGapMin > 120 {
		return errors.New("session_gap_min must be between 1 and 120")
	}
	if s.OnlineWindowMin < 1 || s.OnlineWindowMin > 60 {
		return errors.New("online_window_min must be between 1 and 60")
	}
	if s.Alerts.MinOverlapMin < 1 {
		s.Alerts.MinOverlapMin = 1
	}
	if s.Backup.IntervalHours < 1 {
		s.Backup.IntervalHours = 24
	}
	if s.Backup.Keep < 1 {
		s.Backup.Keep = 7
	}
	if s.Retention.VisitsHourlyDays < 2 {
		s.Retention.VisitsHourlyDays = d.Retention.VisitsHourlyDays
	}
	if s.Retention.Traffic5Days < 2 {
		s.Retention.Traffic5Days = d.Retention.Traffic5Days
	}
	if err := s.Accuracy.validate(); err != nil {
		return err
	}
	if s.Alerts.DailyDigest.Value < 0 || s.Alerts.DailyDigest.Value > 23 {
		return errors.New("daily digest hour must be 0-23")
	}
	if p := strings.TrimSpace(s.Telegram.Proxy); p != "" {
		if err := ValidateProxy(p); err != nil {
			return fmt.Errorf("telegram proxy: %w", err)
		}
	}
	if b := strings.TrimSpace(s.Telegram.APIBase); b != "" {
		u, err := url.Parse(b)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return errors.New("telegram api_base must be an http(s) URL")
		}
	}
	for i := range s.CustomRules {
		r := &s.CustomRules[i]
		r.Domain = strings.ToLower(strings.TrimSpace(r.Domain))
		r.Service = strings.TrimSpace(r.Service)
		if r.Domain == "" || !classify.IsKnownCategory(r.Category) {
			return fmt.Errorf("custom rule %d: needs a domain and a known category", i+1)
		}
	}
	return nil
}

// ValidateProxy accepts http://, https://, socks5:// and socks5h:// URLs.
func ValidateProxy(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	switch u.Scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return fmt.Errorf("unsupported proxy scheme %q", u.Scheme)
	}
	if u.Host == "" {
		return errors.New("proxy host is missing")
	}
	return nil
}

// Location resolves the display timezone.
func (s *Settings) Location() (*time.Location, error) {
	if s.DisplayTimezone == "" || strings.EqualFold(s.DisplayTimezone, "local") {
		return time.Local, nil
	}
	return time.LoadLocation(s.DisplayTimezone)
}

// KV is the subset of the store needed to persist settings.
type KV interface {
	KV(key string) (string, bool, error)
	SetKV(key, val string) error
}

// Manager keeps the current settings in memory and notifies listeners.
type Manager struct {
	kv   KV
	mu   sync.RWMutex
	cur  Settings
	subs []func(Settings)
}

// NewManager loads settings from the store.
func NewManager(kv KV) (*Manager, error) {
	m := &Manager{kv: kv, cur: Defaults()}
	raw, ok, err := kv.KV(kvKey)
	if err != nil {
		return nil, err
	}
	if ok {
		if err := json.Unmarshal([]byte(raw), &m.cur); err != nil {
			return nil, fmt.Errorf("stored settings are corrupt: %w", err)
		}
		if err := m.cur.Validate(); err != nil {
			m.cur = Defaults()
		}
		for _, old := range previousDefaults {
			if m.cur.Accuracy == old {
				m.cur.Accuracy = DefaultAccuracy()
			}
		}
	}
	return m, nil
}

// Get returns a copy of the current settings.
func (m *Manager) Get() Settings {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s := m.cur
	s.CustomRules = append([]classify.Rule(nil), m.cur.CustomRules...)
	s.Telegram.ChatIDs = append([]int64(nil), m.cur.Telegram.ChatIDs...)
	return s
}

// Set validates, stores and publishes new settings.
func (m *Manager) Set(s Settings) error {
	if err := s.Validate(); err != nil {
		return err
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if err := m.kv.SetKV(kvKey, string(b)); err != nil {
		return err
	}
	m.mu.Lock()
	m.cur = s
	subs := append([]func(Settings){}, m.subs...)
	m.mu.Unlock()
	for _, f := range subs {
		f(s)
	}
	return nil
}

// OnChange registers a callback invoked after every successful Set.
func (m *Manager) OnChange(f func(Settings)) {
	m.mu.Lock()
	m.subs = append(m.subs, f)
	m.mu.Unlock()
}

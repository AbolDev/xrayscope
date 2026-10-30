package settings

import (
	"testing"

	"xrayscope/internal/classify"
)

type memKV map[string]string

func (m memKV) KV(k string) (string, bool, error) { v, ok := m[k]; return v, ok, nil }
func (m memKV) SetKV(k, v string) error           { m[k] = v; return nil }

func TestDefaultsAreValid(t *testing.T) {
	s := Defaults()
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejects(t *testing.T) {
	cases := map[string]func(*Settings){
		"language":   func(s *Settings) { s.Language = "de" },
		"timezone":   func(s *Settings) { s.DisplayTimezone = "Mars/Base" },
		"gap":        func(s *Settings) { s.SessionGapMin = 0 },
		"window":     func(s *Settings) { s.OnlineWindowMin = 999 },
		"proxy":      func(s *Settings) { s.Telegram.Proxy = "ftp://x" },
		"proxy host": func(s *Settings) { s.Telegram.Proxy = "socks5://" },
		"api base":   func(s *Settings) { s.Telegram.APIBase = "not a url" },
		"digest":     func(s *Settings) { s.Alerts.DailyDigest.Value = 30 },
		"rule":       func(s *Settings) { s.CustomRules = []classify.Rule{{Domain: "x.com", Category: "bogus"}} },
	}
	for name, mut := range cases {
		s := Defaults()
		mut(&s)
		if err := s.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestManagerPersistsAndNotifies(t *testing.T) {
	kv := memKV{}
	m, err := NewManager(kv)
	if err != nil {
		t.Fatal(err)
	}
	var got Settings
	m.OnChange(func(s Settings) { got = s })

	s := m.Get()
	s.SessionGapMin = 9
	s.CustomRules = []classify.Rule{{Domain: " Example.COM ", Category: "work", Service: "Intranet"}}
	if err := m.Set(s); err != nil {
		t.Fatal(err)
	}
	if got.SessionGapMin != 9 || got.CustomRules[0].Domain != "example.com" {
		t.Fatalf("subscriber got %+v", got)
	}
	m2, err := NewManager(kv)
	if err != nil {
		t.Fatal(err)
	}
	if m2.Get().SessionGapMin != 9 {
		t.Fatal("settings not persisted")
	}
	bad := m.Get()
	bad.SessionGapMin = -1
	if m.Set(bad) == nil || m.Get().SessionGapMin != 9 {
		t.Fatal("invalid settings must not replace the current ones")
	}
}

func TestGetReturnsCopies(t *testing.T) {
	m, _ := NewManager(memKV{})
	s := m.Get()
	s.Telegram.ChatIDs = append(s.Telegram.ChatIDs, 1)
	if len(m.Get().Telegram.ChatIDs) != 0 {
		t.Fatal("Get leaked internal state")
	}
}

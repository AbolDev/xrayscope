package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaultsAndOverrides(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"listen":"127.0.0.1:9000","base_path":"scope/","data_dir":"`+filepath.ToSlash(dir)+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != "127.0.0.1:9000" || c.BasePath != "/scope" || c.Panel.Type != "3x-ui" || c.Panel.PollSeconds != 30 {
		t.Fatalf("config = %+v", c)
	}
	t.Setenv("XRAYSCOPE_LISTEN", "0.0.0.0:1234")
	c, _ = Load(path)
	if c.Listen != "0.0.0.0:1234" {
		t.Fatalf("env override ignored: %s", c.Listen)
	}
}

func TestMissingFileGivesDefaults(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "none.json"))
	if err != nil || c.Role != "server" || c.Listen == "" {
		t.Fatalf("c=%+v err=%v", c, err)
	}
}

func TestValidation(t *testing.T) {
	c := Default()
	c.Role = "agent"
	if err := c.Validate(); err == nil {
		t.Fatal("agent without server/token must be rejected")
	}
	c.ServerURL, c.Token = "https://panel.example.com", "xs_x"
	if err := c.Validate(); err != nil || c.NodeName == "" {
		t.Fatalf("valid agent rejected: %v (name %q)", err, c.NodeName)
	}
	c = Default()
	c.Role = "weird"
	if c.Validate() == nil {
		t.Fatal("unknown role accepted")
	}
	c = Default()
	c.TLSCert = "a.pem"
	if c.Validate() == nil {
		t.Fatal("cert without key accepted")
	}
}

func TestLogLocation(t *testing.T) {
	c := Default()
	if loc, err := c.LogLocation(); err != nil || loc == nil {
		t.Fatalf("local: %v", err)
	}
	c.LogTimezone = "Not/AZone"
	if _, err := c.LogLocation(); err == nil {
		t.Fatal("bad zone accepted")
	}
}

func TestSaveRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "c.json")
	c := Default()
	c.DataDir = "/x"
	c.Proxy = "socks5://127.0.0.1:1080"
	if err := c.Save(p); err != nil {
		t.Fatal(err)
	}
	got, err := Load(p)
	if err != nil || got.Proxy != c.Proxy || got.DataDir != "/x" {
		t.Fatalf("round trip: %+v %v", got, err)
	}
}

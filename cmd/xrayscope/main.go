// Command xrayscope is the traffic analytics panel for Xray based VPN panels.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"syscall"
	"time"
	_ "time/tzdata"

	"xrayscope/internal/agent"
	"xrayscope/internal/app"
	"xrayscope/internal/auth"
	"xrayscope/internal/config"
	"xrayscope/internal/server"
	"xrayscope/internal/store"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const usage = `XrayScope %s

Usage:
  xrayscope [serve]   [-c config.json]   run the panel (or the node agent, depending on "role")
  xrayscope agent     [-c config.json]   run as a node agent
  xrayscope init      [options]          write a configuration file
  xrayscope passwd    [-c config.json] [-user admin]   reset the administrator password
  xrayscope doctor    [-c config.json]   check the installation
  xrayscope version

Run "xrayscope <command> -h" for the options of a command.
`

func defaultConfigPath() string {
	if runtime.GOOS == "windows" {
		return "config.json"
	}
	return "/etc/xrayscope/config.json"
}

func main() {
	args := os.Args[1:]
	cmd := "serve"
	if len(args) > 0 && len(args[0]) > 0 && args[0][0] != '-' {
		cmd, args = args[0], args[1:]
	}
	var err error
	switch cmd {
	case "serve", "run":
		err = cmdServe(args, false)
	case "agent":
		err = cmdServe(args, true)
	case "init":
		err = cmdInit(args)
	case "passwd":
		err = cmdPasswd(args)
	case "doctor":
		err = cmdDoctor(args)
	case "version", "-v", "--version":
		fmt.Println("xrayscope", version)
	case "help", "-h", "--help":
		fmt.Printf(usage, version)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
		fmt.Fprintf(os.Stderr, usage, version)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func newLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

func cmdServe(args []string, forceAgent bool) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	cfgPath := fs.String("c", defaultConfigPath(), "configuration file")
	_ = fs.Parse(args)
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	if forceAgent {
		cfg.Role = "agent"
		if err := cfg.Validate(); err != nil {
			return err
		}
	}
	log := newLogger()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if cfg.Role == "agent" {
		if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
			return err
		}
		a, err := agent.New(cfg, version, log)
		if err != nil {
			return err
		}
		log.Info("xrayscope agent started", "version", version, "node", cfg.NodeName, "server", cfg.ServerURL)
		a.Run(ctx)
		return nil
	}

	a, err := app.New(cfg, version, log)
	if err != nil {
		return err
	}
	defer a.Close()

	var restarting atomic.Bool
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	a.RestartFn = func() {
		restarting.Store(true)
		cancel()
	}

	if a.InitialPassword != "" {
		announceCredentials(log, cfg, a.InitialPassword)
	}
	srv, err := server.New(a)
	if err != nil {
		return err
	}
	log.Info("xrayscope started", "version", version, "data", cfg.DataDir)

	done := make(chan struct{})
	go func() {
		defer close(done)
		a.Run(runCtx)
	}()
	serveErr := srv.Serve(runCtx)
	cancel()
	<-done
	if serveErr != nil {
		return serveErr
	}
	if restarting.Load() {
		log.Info("exiting for restart (the service manager starts it again)")
	}
	return nil
}

func announceCredentials(log *slog.Logger, cfg config.Config, password string) {
	file := filepath.Join(cfg.DataDir, "initial-credentials.txt")
	_ = os.WriteFile(file, []byte("username: admin\npassword: "+password+"\n\nChange it in Settings and delete this file.\n"), 0o600)
	log.Info("administrator account created", "username", "admin", "password", password, "also_saved_in", file)
}

func cmdInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	out := fs.String("o", defaultConfigPath(), "output file")
	listen := fs.String("listen", "0.0.0.0:8686", "web panel address")
	data := fs.String("data", "/var/lib/xrayscope", "data directory")
	panelType := fs.String("panel", "3x-ui", "panel type (3x-ui or none)")
	panelDB := fs.String("panel-db", "/etc/x-ui/x-ui.db", "panel database path")
	logPath := fs.String("log", "auto", "access log path or auto")
	base := fs.String("base-path", "", "URL prefix when behind a reverse proxy")
	role := fs.String("role", "server", "server or agent")
	serverURL := fs.String("server", "", "main server URL (agent)")
	token := fs.String("token", "", "node token (agent)")
	name := fs.String("name", "", "node name (agent)")
	proxy := fs.String("proxy", "", "outbound proxy URL")
	force := fs.Bool("f", false, "overwrite an existing file")
	_ = fs.Parse(args)

	if _, err := os.Stat(*out); err == nil && !*force {
		return fmt.Errorf("%s already exists (use -f to overwrite)", *out)
	}
	cfg := config.Default()
	cfg.Role, cfg.Listen, cfg.DataDir, cfg.BasePath = *role, *listen, *data, *base
	cfg.Panel.Type, cfg.Panel.DBPath, cfg.LogPath = *panelType, *panelDB, *logPath
	cfg.ServerURL, cfg.Token, cfg.NodeName, cfg.Proxy = *serverURL, *token, *name, *proxy
	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := cfg.Save(*out); err != nil {
		return err
	}
	fmt.Println("wrote", *out)
	return nil
}

func cmdPasswd(args []string) error {
	fs := flag.NewFlagSet("passwd", flag.ExitOnError)
	cfgPath := fs.String("c", defaultConfigPath(), "configuration file")
	user := fs.String("user", "admin", "administrator username")
	_ = fs.Parse(args)
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	st, err := store.Open(cfg.DBFile(), nil)
	if err != nil {
		return err
	}
	defer st.Close()
	a, err := auth.New(st)
	if err != nil {
		return err
	}
	pw := fs.Arg(0)
	if pw == "" {
		pw = auth.GeneratePassword(14)
	}
	if err := a.SetAdmin(*user, pw); err != nil {
		return err
	}
	fmt.Printf("username: %s\npassword: %s\n", *user, pw)
	return nil
}

func cmdDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	cfgPath := fs.String("c", defaultConfigPath(), "configuration file")
	_ = fs.Parse(args)
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	ok := true
	check := func(name string, err error, detail string) {
		if err != nil {
			ok = false
			fmt.Printf("  [FAIL] %s: %v\n", name, err)
			return
		}
		fmt.Printf("  [ ok ] %s %s\n", name, detail)
	}
	fmt.Println("XrayScope", version, "- configuration", *cfgPath)
	check("data directory", os.MkdirAll(cfg.DataDir, 0o750), cfg.DataDir)
	if cfg.Role == "agent" {
		fmt.Println("  role: agent ->", cfg.ServerURL)
	}
	if cfg.Panel.Type != "none" {
		_, err := os.Stat(cfg.Panel.DBPath)
		check("panel database", err, cfg.Panel.DBPath)
		if err == nil {
			doctorPanel(cfg, check)
		}
	}
	if cfg.LogPath != "auto" && cfg.LogPath != "" {
		_, err := os.Stat(cfg.LogPath)
		check("access log", err, cfg.LogPath)
	}
	if !ok {
		return errors.New("some checks failed")
	}
	fmt.Println("everything looks fine")
	return nil
}

func doctorPanel(cfg config.Config, check func(string, error, string)) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p, err := newPanel(cfg)
	if err != nil || p == nil {
		return
	}
	users, err := p.Users(ctx)
	check("panel accounts", err, fmt.Sprintf("(%d accounts)", len(users)))
	info, err := p.AccessLog(ctx)
	switch {
	case err != nil:
		check("panel log settings", err, "")
	case info.Disabled:
		check("access log", errors.New("access logging is disabled in the panel - enable the Xray access log (panel: Xray configs -> log -> access path, e.g. /var/log/xray/access.log) and restart Xray"), "")
	case info.Path == "":
		check("access log", fmt.Errorf("configured as %q but the file does not exist yet", info.Setting), "")
	default:
		check("access log", nil, info.Path)
	}
}

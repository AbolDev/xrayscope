package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"xrayscope/internal/report"
	"xrayscope/internal/telegram"
)

func rangeLast(d time.Duration) report.Range {
	now := time.Now().Unix()
	return report.Range{From: now - int64(d/time.Second), To: now}
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func humanMinutes(m int64) string {
	if m < 60 {
		return fmt.Sprintf("%dm", m)
	}
	return fmt.Sprintf("%dh%02dm", m/60, m%60)
}

// botHandler answers Telegram commands from the allowed chats.
func (a *App) botHandler(ctx context.Context, cmd string, args []string) string {
	env := a.Env()
	switch cmd {
	case "start", "help":
		return a.msg("bot.help")
	case "online":
		online := a.Live.Online(env.OnlineWin)
		if len(online) == 0 {
			return a.msg("bot.none_online")
		}
		var b strings.Builder
		for _, o := range online {
			var ips []string
			for _, c := range o.Conns {
				ips = append(ips, telegram.Escape(c.IP))
			}
			fmt.Fprintf(&b, "• %s — %s\n", telegram.Escape(o.Email), strings.Join(ips, ", "))
		}
		return a.msg("bot.online", len(online), b.String())
	case "user":
		if len(args) == 0 {
			return a.msg("bot.usage_user")
		}
		u, err := a.Store.UserByEmail(ctx, args[0])
		if err != nil {
			return a.msg("bot.no_user")
		}
		now := time.Now().In(env.Loc)
		midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, env.Loc)
		rg := report.Range{From: midnight.Unix(), To: now.Unix()}
		r, err := env.User(ctx, u.ID, rg)
		if err != nil {
			return a.msg("bot.no_user")
		}
		state := a.msg("state.offline")
		ips := 0
		if r.Online != nil {
			state, ips = a.msg("state.online"), len(r.Online.Conns)
		}
		last := "-"
		if u.LastSeen > 0 {
			last = time.Unix(u.LastSeen, 0).In(env.Loc).Format("2006-01-02 15:04")
		}
		return a.msg("bot.user", telegram.Escape(u.Email), state, humanMinutes(r.Activity.OnlineMinutes), humanBytes(r.Traffic.Up), humanBytes(r.Traffic.Down), last, ips)
	case "top":
		now := time.Now().In(env.Loc)
		midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, env.Loc)
		o, err := env.Overview(ctx, report.Range{From: midnight.Unix(), To: now.Unix()})
		if err != nil {
			return err.Error()
		}
		var b strings.Builder
		for i, u := range o.TopTraffic {
			fmt.Fprintf(&b, "%d. %s — %s\n", i+1, telegram.Escape(u.Email), humanBytes(u.Up+u.Down))
		}
		return a.msg("bot.top", b.String())
	case "status":
		st, _ := a.Store.Stats(ctx)
		return a.msg("bot.status", a.Version, len(a.Live.Online(env.OnlineWin)), st.Users, humanBytes(st.SizeBytes), a.LogState().State)
	}
	return ""
}

package app

import "fmt"

// msg returns a server-generated message in the configured language.
func (a *App) msg(key string, args ...any) string {
	lang := a.Settings.Get().Language
	table := textsEN
	if lang == "fa" {
		table = textsFA
	}
	f, ok := table[key]
	if !ok {
		f = textsEN[key]
	}
	return fmt.Sprintf(f, args...)
}

var textsEN = map[string]string{
	"alert.max_ips":      "⚠️ <b>%s</b> is connected from %d IPs at once (limit %d):\n%s",
	"alert.new_ip":       "🆕 <b>%s</b> connected from a new IP <code>%s</code> %s",
	"alert.new_country":  "🌍 <b>%s</b> appeared in a new country: <b>%s</b> (<code>%s</code>)",
	"alert.spike":        "📈 <b>%s</b> transferred %s in the last hour",
	"alert.quota":        "💾 <b>%s</b> used %d%% of the quota (%s of %s)",
	"alert.expiry":       "⏰ <b>%s</b> expires in %d day(s)",
	"alert.expired":      "⛔ <b>%s</b> has expired",
	"alert.node_offline": "🔌 Node <b>%s</b> has not reported for %d minutes",
	"alert.node_online":  "✅ Node <b>%s</b> is reporting again",
	"alert.log_stale":    "📄 No new access-log lines for %d minutes. Check that access logging is enabled in the panel.",
	"digest.title":       "📊 <b>Daily summary</b> (%s)",
	"digest.line":        "Active users: %d of %d\nTraffic: ↑ %s  ↓ %s\nTop users:\n%s",
	"backup.done":        "💾 Backup created: %s (%s)",
	"backup.failed":      "❌ Backup failed: %s",
	"bot.help":           "/online – who is connected\n/user &lt;email&gt; – summary of a user\n/top – top users by traffic today\n/status – system status",
	"bot.none_online":    "Nobody is online.",
	"bot.online":         "🟢 <b>%d online</b>\n%s",
	"bot.no_user":        "User not found.",
	"bot.usage_user":     "Usage: /user &lt;email&gt;",
	"bot.user":           "👤 <b>%s</b>\nStatus: %s\nToday: %s active, ↑ %s ↓ %s\nLast seen: %s\nIPs now: %d",
	"bot.top":            "🏆 <b>Top users today</b>\n%s",
	"bot.status":         "ℹ️ <b>XrayScope %s</b>\nOnline: %d\nUsers: %d\nDatabase: %s\nAccess log: %s",
	"state.online":       "online",
	"state.offline":      "offline",
}

var textsFA = map[string]string{
	"alert.max_ips":      "⚠️ کاربر <b>%s</b> هم‌زمان با %d آی‌پی متصل است (سقف %d):\n%s",
	"alert.new_ip":       "🆕 کاربر <b>%s</b> از آی‌پی جدید <code>%s</code> متصل شد %s",
	"alert.new_country":  "🌍 کاربر <b>%s</b> از یک کشور جدید متصل شد: <b>%s</b> (<code>%s</code>)",
	"alert.spike":        "📈 کاربر <b>%s</b> در یک ساعت گذشته %s ترافیک مصرف کرد",
	"alert.quota":        "💾 کاربر <b>%s</b> %d%% از حجم خود را مصرف کرده (%s از %s)",
	"alert.expiry":       "⏰ اشتراک <b>%s</b> تا %d روز دیگر تمام می‌شود",
	"alert.expired":      "⛔ اشتراک <b>%s</b> منقضی شده است",
	"alert.node_offline": "🔌 نود <b>%s</b> به مدت %d دقیقه گزارشی نفرستاده است",
	"alert.node_online":  "✅ نود <b>%s</b> دوباره گزارش می‌دهد",
	"alert.log_stale":    "📄 به مدت %d دقیقه هیچ خط جدیدی در access.log نیامده است. فعال بودن لاگ دسترسی در پنل را بررسی کنید.",
	"digest.title":       "📊 <b>خلاصه روزانه</b> (%s)",
	"digest.line":        "کاربران فعال: %d از %d\nترافیک: ↑ %s  ↓ %s\nپرمصرف‌ترین‌ها:\n%s",
	"backup.done":        "💾 پشتیبان ساخته شد: %s (%s)",
	"backup.failed":      "❌ ساخت پشتیبان ناموفق بود: %s",
	"bot.help":           "/online – کاربران متصل\n/user &lt;email&gt; – خلاصه یک کاربر\n/top – پرمصرف‌ترین‌های امروز\n/status – وضعیت سیستم",
	"bot.none_online":    "هیچ کاربری آنلاین نیست.",
	"bot.online":         "🟢 <b>%d نفر آنلاین</b>\n%s",
	"bot.no_user":        "کاربر پیدا نشد.",
	"bot.usage_user":     "استفاده: /user &lt;email&gt;",
	"bot.user":           "👤 <b>%s</b>\nوضعیت: %s\nامروز: %s فعالیت، ↑ %s ↓ %s\nآخرین اتصال: %s\nتعداد آی‌پی‌های فعلی: %d",
	"bot.top":            "🏆 <b>پرمصرف‌ترین‌های امروز</b>\n%s",
	"bot.status":         "ℹ️ <b>XrayScope %s</b>\nآنلاین: %d\nکاربران: %d\nپایگاه داده: %s\nلاگ دسترسی: %s",
	"state.online":       "آنلاین",
	"state.offline":      "آفلاین",
}

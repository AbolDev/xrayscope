<div align="center">

<img src="internal/server/web/favicon.svg" width="84" alt="XrayScope logo">

# XrayScope

**Traffic and behaviour analytics for Xray-based VPN panels.**
Install it next to [3x-ui](https://github.com/MHSanaei/3x-ui), read `access.log`, and get a fast, good-looking web panel that shows who is online, from which IPs and devices, what they visit, how long they spend on AI tools or social media, and when they sleep.

[فارسی](README.fa.md) · [Architecture](docs/ARCHITECTURE.md) · [Privacy & legal notes](docs/PRIVACY.md)

</div>

---

## Features

| | |
|---|---|
| **Per-user analysis** | Sessions, active hours, interactive vs. background time, daily and hourly activity, weekday × hour heat map |
| **What they visit** | Every site/service seen in the log, grouped into categories (AI, social, messaging, video, music, gaming, dev, news, shopping, finance, …) with first/last seen and active minutes |
| **Interests** | "Spent 3 h on ChatGPT, 40 min on Claude, mostly Instagram + Telegram" – favourite services per area |
| **Upload / download** | Per user over time (5-minute resolution for recent data, hourly forever), read from the panel's counters |
| **IPs & devices** | Full IP history with country/ISP (optional GeoIP), simultaneous-IP detection with overlap timeline, per-day minute-by-minute view per IP, guesses of OS / device vendor / browser from traffic fingerprints |
| **Sleep & wake pattern** | Estimates bedtime, wake time and duration from interactive activity and tells *"asleep but VPN/phone still connected"* apart from *"offline"* |
| **Live view** | Who is online right now, from how many IPs, with over-limit highlighting |
| **Multi-node** | Install a tiny agent on each node; it ships logs to the main server over HTTPS (optionally through an HTTP/SOCKS5 proxy), with an on-disk queue so nothing is lost when the link drops |
| **Telegram** | Alerts (too many IPs, new IP/country, traffic spike, quota/expiry, node offline, log stalled), daily digest, backups and bot commands (`/online`, `/user`, `/top`, `/status`) – optionally via proxy |
| **Data safety** | Permanent storage in one SQLite file, scheduled compressed backups with retention, download / upload / one-click restore |
| **UI** | Persian (RTL, Jalali calendar) and English, light and dark themes, responsive, no external requests (fonts and charts are bundled) |

## Why it is light

- One static Go binary (~19 MB), no runtime, no Docker needed, pure-Go SQLite.
- The access log is parsed with zero allocations per line and aggregated per minute **before** it touches the database.
- Time is stored as 60-bit minute masks per hour, so *one row per user, IP and hour* captures minute-level presence. A 3 million line / 390 MB log becomes a ~9 MB database.
- Measured on a laptop: **3 million log lines in about 6 s using ~64 MB of RAM** (see [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md#performance)). On a small VPS the panel idles at a few MB.

## Install (3x-ui server)

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/OWNER/xrayscope/main/install.sh)
```

The script installs the binary to `/usr/local/bin`, writes `/etc/xrayscope/config.json`, creates a systemd service, runs a health check and prints the panel URL and the first login. Re-run it to update; `bash install.sh uninstall [--purge]` removes it.

### 1. Turn on the Xray access log

XrayScope reads the Xray **access log**, which 3x-ui disables by default. In the panel go to **Xray Configs** and set:

```json
"log": {
  "access": "/var/log/xray/access.log",
  "loglevel": "warning"
}
```

Then restart Xray. Also keep **Sniffing** enabled on your inbounds (3x-ui does by default) – then Xray logs the real domain (the TLS SNI / HTTP host) instead of just an IP address.

Check everything with:

```bash
xrayscope doctor -c /etc/xrayscope/config.json
```

The panel page **System & backups** shows the same checks, with plain-language hints when something is missing.

### 2. Open the panel

`http://SERVER_IP:8686/` – the first administrator password is printed by the installer (and saved in `/var/lib/xrayscope/initial-credentials.txt`). Reset it any time:

```bash
xrayscope passwd -c /etc/xrayscope/config.json
```

> **Expose it safely.** Put it behind HTTPS: either a reverse proxy (set `"trust_proxy": true` and `"base_path"` if you serve it under a path) or set `tls_cert` / `tls_key` in the config. Logins are rate limited, sessions are signed and `HttpOnly`/`SameSite=Strict`.

## Multiple nodes

1. On the main server open **Nodes → Add node**, give it a name and copy the command it shows.
2. Run it on the node:

   ```bash
   bash <(curl -fsSL https://raw.githubusercontent.com/OWNER/xrayscope/main/install.sh) agent \
     --server https://panel.example.com --token xs_… --name de-1
   ```

3. The node tails its own log and sends compact per-minute records every few seconds. If the node can only reach the main server through a proxy add `"server_proxy": "socks5://host:1080"` (or `http://…`) to its config; `"server_sni"` overrides the TLS server name when you connect through a CDN or fronting domain.

Nodes keep working when the main server is down: batches are spooled on disk (up to 2 GB) and delivered later, exactly once.

## Telegram

*Settings → Telegram*: paste the bot token (from `@BotFather`), the chat IDs and optionally a **proxy** (`http://`, `https://`, `socks5://`) – or leave it empty to use the global `"proxy"` of the config file. *Save & send test* verifies everything. Alerts are always recorded in **Alerts & events** even when Telegram is off.

## Configuration

Only a few things live in the config file; everything else is edited in the panel.

```json
{
  "role": "server",
  "listen": "0.0.0.0:8686",
  "base_path": "",
  "data_dir": "/var/lib/xrayscope",
  "tls_cert": "", "tls_key": "",
  "trust_proxy": false,
  "log_path": "auto",
  "log_timezone": "Local",
  "proxy": "",
  "geo_db": "", "asn_db": "",
  "panel": { "type": "3x-ui", "db_path": "/etc/x-ui/x-ui.db", "poll_seconds": 30 }
}
```

| Key | Meaning |
|---|---|
| `role` | `server` (collector + panel) or `agent` (node) |
| `log_path` | Path of the Xray access log, or `auto` to read it from the 3x-ui settings |
| `log_timezone` | Time zone of the timestamps in the log (the server's zone by default) |
| `proxy` | Default outbound proxy for Telegram |
| `geo_db`, `asn_db` | Optional MaxMind-format `.mmdb` files (GeoLite2 / DB-IP lite) to show country and ISP |
| `panel.type` | `3x-ui` or `none` (then only the log is used and traffic counters are unavailable) |
| `trust_proxy` | Believe `X-Forwarded-For` (only behind your own reverse proxy) |

Agent keys: `server_url`, `token`, `node_name`, `server_proxy`, `server_sni`, `insecure_tls`.

## How it works – and its limits

- **Traffic volume** is not in `access.log`; XrayScope polls the panel's per-user counters (`client_traffics` in the 3x-ui database, read-only) and stores the deltas.
- **"Time spent"** is the number of minutes in which the user's connections touched a service. Background traffic (updates, push, connectivity checks, CDN, telemetry) is tracked separately and never counts as interactive use.
- **Sleep** = the longest night-time gap without interactive traffic. Isolated blips (a push notification waking an app) are ignored. If background traffic continues during the gap the night is labelled *connected while inactive* – the phone/VPN stayed on – otherwise *no traffic at all*.
- **Device guesses** use well-known OS and browser housekeeping domains (Windows telemetry, Android connectivity checks, Apple services, Chrome/Firefox/Edge updaters, …). Several OS families on one IP are reported as several devices. These are *heuristics*; the panel always shows a confidence value.
- Anything that hides the destination (no sniffing, ECH, a pure-IP service) shows up as an address instead of a site.

## Development

```bash
make test            # unit + end-to-end tests
make demo            # synthetic data in .demo/ (login admin / demo12345)
make run             # serve .demo with the UI loaded from disk
make release         # linux amd64 + arm64 archives in dist/
```

Requires Go 1.26+. The web UI is plain ES modules in `internal/server/web` – no build step. Classification rules live in `internal/classify/rules.txt`; adding a service is a one-line change. A new panel type implements the three-method `panel.Panel` interface.

## Security

See [docs/PRIVACY.md](docs/PRIVACY.md). In short: only the administrator can log in, the Telegram token is never sent back to the browser, uploads are validated before a restore, all rendered text is escaped, and the service runs with a restricted systemd profile.

## License

[MIT](LICENSE). The bundled Vazirmatn font is under the SIL Open Font License (see `internal/server/web/fonts/OFL.txt`).

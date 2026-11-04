# Architecture

```
                         ┌──────────────────────────── main server ───────────────────────────┐
  Xray access.log ──tail──► parser ──► aggregator (per minute) ──► SQLite ◄── report/analytics ──► web panel
  3x-ui x-ui.db  ──poll──► traffic deltas ─────────────────────────┘   ▲            ▲
                                                                        │            └── Telegram bot / alerts
  node: log + panel ──► agent ──► spool (disk) ──HTTPS(+proxy)──► /api/ingest ──┘
```

One binary, two roles: `server` (collector + analytics + web panel) and `agent` (a node that only collects and ships).

## Packages

| Package | Responsibility |
|---|---|
| `parser` | Zero-allocation parser for Xray access-log lines (IPv4/IPv6, both tag styles). |
| `classify` | Domain → site / category / service rules (`rules.txt`, embedded, longest-suffix match), IP ranges (Telegram, public DNS), OS/browser fingerprint hints. |
| `collect` | `Tailer` (rotation, copy-truncate, resume by file fingerprint), `Aggregator` (per-minute counters), `Pipeline` (batching), `TrafficPoller` (cumulative → delta). |
| `store` | SQLite schema, transactional `ApplyBatch`, queries, compaction, snapshots. |
| `analytics` | Sessions, simultaneous-IP overlaps, sleep analysis, heat maps, device guesses, time-by-category. Pure functions over presence masks. |
| `report` | Assembles the datasets of each screen (overview, users, user, timeline, sites, IP). |
| `app` | Wiring: log supervisor, panel poller, alert engine, digest, backups, retention, Telegram commands. |
| `server` | HTTP API, auth middleware, gzip, embedded UI. |
| `agent` | Node role: spool queue, retrying sender. |
| `panel` | Adapter interface + 3x-ui implementation (accounts, quota, expiry, IP limit, access-log setting). |
| `telegram`, `netutil` | Bot API client; HTTP clients through HTTP(S)/SOCKS5 proxies with SNI override. |
| `backup`, `auth`, `settings`, `config`, `live`, `geo` | Snapshots & restore, signed sessions, runtime settings, bootstrap config, in-memory online view, optional GeoIP. |

## Data model

Everything is keyed by *hour slots* (`unix / 3600`). Inside an hour, activity is a **60-bit mask** – bit *m* is set if there was traffic in minute *m*. SQLite `OR`s masks on conflict, so ingestion is an idempotent upsert.

| Table | Row | Content |
|---|---|---|
| `act` | (hour, user, ip, node) | `mask` any traffic · `imask` interactive traffic · request count |
| `svc` | (hour, user, service) | `mask` minutes in which the service was used |
| `visits` | (hour, user, site) | `mask`, requests. Rows older than the retention window are merged into daily rows (`g=1`, mask = hours of the day) – nothing is deleted. |
| `fp` | (day, user, ip, kind, value) | device-fingerprint score |
| `traffic5`, `traffic_h` | (slot, user, node) | upload / download; 5-minute rows are dropped after N days, hourly rows are kept forever |
| `users`, `ips`, `domains`, `services`, `nodes`, `events`, `kv` | | dimensions, alert log, settings, read offset |

Why masks? "Minutes online per day", sessions, concurrency and sleep all need minute resolution, but storing a row per minute would cost ~100× more. The mask keeps that resolution at one row per hour.

### Consistency

The read offset of the log is written in the **same transaction** as the data derived from it, so a crash neither loses nor double counts lines. Agent batches carry an id; the server ignores retries.

### Interactive vs. background

Each site belongs to a category; `update`, `system`, `cdn`, `ads`, `telemetry`, `push`, `connectivity` and `dns` are *background*. A minute counts as *interactive* when at least one non-background site was touched. This is what separates a person using the phone from a phone that is merely connected.

## Accuracy

The access log records only when a connection opens, so a video or a page being read leaves silent minutes. Three measures keep the numbers close to reality:

- **Time on a site** counts the minutes between two visits of the same site when the pause is short (up to 5 minutes, `analytics.BridgeMinutes`) or when the user's connection kept moving data (up to 30 minutes, at least 70 % of the pause with traffic). The traffic minutes come from Xray's statistics API: every second the per-user counters are sampled, and a minute with at least 30 KB moved is stored in `tact` (one 60-bit mask per user and hour; nodes send them with their batches). Without the statistics API only the 5-minute rule applies.
- **Who is online** also asks Xray (`GetStatsOnlineIpList`, needs `"statsUserOnline": true` in the policy). Long-lived connections no longer drop off the list. If the policy is off, the log alone is used.
- **Addresses without a domain** are classified by the network owner in the ASN database when it is one-purpose (Telegram, Netflix, game publishers); other addresses stay "Direct IP".

- **Machine-like polling** (short pulses at a near-constant interval for hours, `analytics.IsPeriodic`) is kept as requests but not counted as time spent, and the site is marked "automatic".
- **A changed address is not a second device.** When a new address of the same operator (same ASN, or the same /16 without a GeoIP ASN database) starts within two minutes of the old one going quiet, both count as one device, in the online list, in IP-limit alerts and in the concurrency history.
- **Data per site** is an estimate: each five-minute slot of the user's measured traffic is shared among the sites active in it, in proportion to their requests (hourly once the five-minute detail has expired).
- **Sleep:** minutes of sustained transfer (at least 1 MB, stored as `tact.hmask`) count as awake when a video, music, social or game service was opened shortly before; a night-time update download does not.

The **Data quality** card in System shows, per inbound, the share of connections without a domain name (sniffing off) and of clients that are CDN edges (real address not passed through), whether the statistics and online APIs answer, and the log's time zone. The time zone is checked by comparing log timestamps with the moment lines are read while the collector keeps up; once the offset is stable for a few minutes, times are corrected automatically (remembered in `kv` as `log:shift`).

The card also counts QUIC (UDP 443) connections without a site name per inbound; a high share means "quic" is missing from the sniffing `destOverride`, which hides much of YouTube, Instagram and Google.

All thresholds above can be tuned under **Settings → Accuracy** (short pause, pause with traffic and its share, requests per minute, active and heavy minute in KB, counting polling, keeping address changes apart). Time and device figures are recalculated with the new values; the request and data thresholds apply to data collected afterwards. Nodes that push data measure traffic minutes with the default thresholds.

Sites no rule knows are listed in Settings, where one click files them under a category.

## Sleep analysis

For each night (noon → noon, in the display time zone):

1. take the interactive minutes; keep only those with ≥ 3 interactive minutes within ±10 min (drops isolated blips);
2. find the gaps between consecutive confirmed minutes that last ≥ 3 h;
3. choose the gap overlapping the 00:00–08:00 window the most (otherwise the longest, flagged *unusual time*);
4. count background-traffic minutes inside the gap → *connected while inactive* (≥ 4 such minutes) or *no traffic at all*; count short wake-ups.

Typical bedtime/wake time are medians over nights; confidence combines the number of nights (7 → full) and the regularity of the bedtime.

## Device guessing

`classify/rules.txt` lists housekeeping domains per OS family and browser with a weight. Hits are stored per (day, user, IP). `analytics.GuessDevices` groups them by family, merges vendor variants (Samsung, Xiaomi …), reports every family reaching 20 % of the strongest one as a separate device, and scales confidence by the amount of evidence.

## Performance

Measured with `cmd/xrayscope-demo -log-out` (3 000 000 lines, 393 MB, 900 users, 4 000 hosts) on a laptop:

| | |
|---|---|
| Parse | ≈ 90 ns/line, 0 allocations |
| Aggregate | ≈ 50 ns/line, 0 allocations on repeated keys |
| End-to-end ingest | 3 M lines in ≈ 6 s |
| Peak RAM | ≈ 64 MB (flushed every 4 k lines / 50 k keys) |
| Database | 9 MB |

Reads are served from per-request aggregation over the mask tables with a 5 s response cache.

## Adding a panel

Implement `panel.Panel`:

```go
type Panel interface {
    Name() string
    Users(ctx context.Context) ([]model.PanelUser, error) // accounts + cumulative counters
    AccessLog(ctx context.Context) (LogInfo, error)       // where the core writes its log
}
```

and register it in `panel.New`. Panels whose log format differs need a parser next to `parser.ParseLine`.

# XrayScope

An open-source analytics panel that runs next to 3x-ui (and other VPN panels later) on the same server. It reads Xray `access.log` and turns it into a visual dashboard: per-user browsing, online sessions, traffic, devices and IPs, plus Telegram alerts, backups, and multi-node support.

## Status

Name selected. Project scaffold created. No implementation yet.

## Planned scope (phase 1: 3x-ui)

- Parse Xray `access.log` incrementally and store it durably
- Per-user timeline: visited sites, online hours, upload/download per time window
- Connection history: IPs, number of concurrent devices/IPs over time
- Traffic and behavior analysis, including AI, social media, and other category breakdowns
- Sleep/wake inference from activity patterns
- Device / OS / browser guessing from connection metadata
- Web dashboard on the same server as the VPN panel
- Multi-node connection from a main server
- Telegram bot for alerts and notifications (proxy support)
- Scheduled backups of stored data
- Proxy support for outbound connections (Telegram, nodes)
- Optimized for low resource usage

## Later

- Support for other VPN panels beyond 3x-ui, via a pluggable adapter layer

# Privacy, security and legal notes

XrayScope shows which sites individual people visited, when they were online and where from. That is sensitive personal data.

## Your responsibility

- Tell the people whose traffic you analyse what is recorded. Many jurisdictions require notice and a legal basis, and some forbid this kind of monitoring altogether.
- Collect only what you need. You can stop collecting by turning off the Xray access log; you can erase a person with **Users → (user) → trash icon** which deletes all their rows.
- Treat the database and its backups as personal data: restrict access, encrypt disks, and delete old backups.

## What is stored

| Stored | Not stored |
|---|---|
| Per user and client IP: which *minutes* had traffic, request counts | Individual requests with exact timestamps |
| Per user: site / service names, minutes of use, request counts | URLs, page content, cookies, request bodies (Xray does not log them) |
| Upload / download totals over time | Traffic content |
| Client IPs, optional country / ISP | Browser User-Agent strings (not available to a proxy) |

## Network use

XrayScope works fully offline. The web panel loads nothing from other sites (fonts, scripts and icons are built in, and the Content-Security-Policy forbids external resources); GeoIP uses local database files and CDN detection uses a built-in table. The only outbound connections are ones you switch on or press:

- **Telegram** – only when you configure a bot token.
- **Update check / update** – when you open the System page (and when you press the buttons there). It talks to `api.github.com` and `github.com`, through the configured proxy if there is one. Offline, copy the binary to the server and run `XRAYSCOPE_BIN=/path/to/xrayscope bash install.sh`.
- **Pulled nodes** – only the node addresses you entered.

## Application security

- One administrator account; passwords are stored with bcrypt, sessions are HMAC-signed, `HttpOnly`, `SameSite=Strict` (and `Secure` over HTTPS); 6 failed logins lock an address for 10 minutes.
- State-changing API calls require a custom header, so another website cannot trigger them from your browser.
- Nodes authenticate with random 192-bit tokens that are stored hashed (SHA-256); the token is shown once.
- The Telegram token is never returned by the API; error messages are scrubbed of it.
- All values from traffic (domains, e-mails) are rendered as text, never as HTML. A strict Content-Security-Policy is sent.
- Restores validate the file (`PRAGMA quick_check`, schema version) and are applied only on the next start; the replaced database is kept in `backups/`.
- The systemd unit uses `NoNewPrivileges`, `ProtectSystem=full`, `ProtectHome`, `PrivateTmp`.
- The 3x-ui database is opened read-only (`query_only`); XrayScope never changes your panel.

## Recommended setup

1. Serve the panel over HTTPS only.
2. Restrict the port to your admin IPs with the firewall if you can.
3. Use a long unique password (`xrayscope passwd` generates one).
4. Keep the Telegram chat private; it receives user e-mails and IPs in alerts.

## Reporting a vulnerability

Please open a private security advisory on the repository instead of a public issue.

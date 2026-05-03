# Security Policy

## Supported versions

| Version | Security updates |
|---------|------------------|
| `:dev` (early access) | ✅ Yes — fixes land on every dev build |
| `:latest` (once published) | ✅ Yes |

Resolvarr is currently in early access — the only published image is `:dev`. Once `:latest` is cut, security updates land on both channels.

## Reporting a vulnerability

**Please do NOT open a public GitHub issue for security bugs.** Even describing an attack path in a public forum before a fix ships puts other users at risk.

### Preferred channel

Email: **27929995+ProphetSe7en@users.noreply.github.com** with subject line `[Resolvarr Security] <brief summary>`.

### Fallback

If email fails or you need pseudonymous submission, use GitHub's private **Report a vulnerability** link on the [repository security tab](https://github.com/prophetse7en/resolvarr/security/advisories/new).

### What to include

- Resolvarr version (visible in the page header next to the logo, or `GET /api/version`).
- Clear reproduction steps (command + request body + expected vs actual response is ideal).
- Impact assessment — what data/access can the attacker obtain?
- Your disclosure timeline preference.

### What to expect

- **Acknowledgement within 72 hours** of receipt (usually faster — solo maintainer, best-effort).
- **Triage and severity assessment within 7 days.** I'll confirm whether I accept the finding, classify severity, and propose a fix + disclosure timeline.
- **Fix within 14 days** for Critical/High findings. Medium/Low may take a release cycle.
- **Coordinated disclosure** — I'll ship a patched release first, then credit you in the CHANGELOG and this document (unless you prefer anonymity). Please do not publish details before the patch ships.

### How I handle reports

- Reporter credit in CHANGELOG + this document by default (anonymous on request).
- Honest acknowledgement when a report is valid — including in the CHANGELOG.
- Open to public discussion of a finding after the patch ships.

## Security model

Resolvarr is a **local admin tool** that talks to your Radarr / Sonarr instances over the Arr v3 API. The design assumes:

- You control the host where it runs.
- You do not expose port 6075 directly to the internet without a reverse proxy.
- You protect `/config/` the same way you protect Radarr/Sonarr's `config.xml` (file permissions, backup encryption, LUKS on the host).

### What Resolvarr does

- **Login required by default.** First-run setup forces you to create an admin account — there are no default credentials. Passwords are hashed with bcrypt (cost 12) and stored in `/config/auth.json`. Long passwords (16+ characters) skip the upper/lower/digit/symbol class check, so passphrases are welcome.
- **Brute-force protection on login.** After 5 failed login attempts from the same IP within a minute, further attempts are blocked for the rest of the window with HTTP 429 + a `Retry-After` header. Same protection applies to `/setup` and the change-password endpoint. Failed attempts are logged with the source IP so you can wire them up to fail2ban or similar if you want to ban the source at the firewall.
- **CSRF protection** on every state-changing request (login, save, run-scan, save-rule, delete). Browsers can't be tricked into submitting a request from another site without also possessing your session cookie *and* the matching token from this site.
- **Security headers**: `X-Frame-Options: DENY` (no embedding in iframes — defeats clickjacking), `X-Content-Type-Options: nosniff`, `Referrer-Policy: same-origin`, `Content-Security-Policy` with no `'unsafe-inline'` for scripts.
- **Outbound notification URLs are checked.** Discord and Pushover (the two providers where the user typically pastes a URL from a third party) go through an HTTP client that refuses to connect to internal IPs (RFC1918, link-local, loopback) — defeats DNS-rebinding attacks where a malicious webhook hostname secretly resolves to a LAN address. Re-checked on every request, not cached. Self-hosted Gotify / NTFY / Apprise use a standard HTTP client because the user controls the destination directly.
- **Secrets are masked in API responses.** Arr API keys, Discord/Gotify/Pushover/NTFY/Apprise tokens. Editing without changing a secret keeps the stored value (the field stays empty in the form, you don't have to re-enter).
- **Sessions survive restarts.** Stored on disk in `/config/sessions.json`, written atomically. Container restart doesn't kick everyone out.
- **File permissions are tight.** `/config/resolvarr.json` (mode 0600) and `/config/auth.json` (0600 in dir 0700) — readable only by the container user. Writes are atomic (temp file + rename) so a crash mid-write can't leave a half-written config.
- **Scan history dumps are symlink-hardened.** The Activity tab lists JSON files under `/config/logs/scan-*.json`. The list-and-serve endpoints both `os.Lstat` each entry and reject anything with the symlink bit set, so a user-writable `/config/logs/` can't be used to make resolvarr serve files outside `/config/` (e.g., a symlink pointing at `/etc/passwd`).
- **Reverse-proxy headers are honored only from configured proxies.** `X-Forwarded-For` and `X-Forwarded-Proto` are trusted only when the direct peer IP matches your Trusted Proxies list. Stops other containers on the same Docker bridge from spoofing client IPs.
- **Lock trusted-network and trusted-proxy lists from the container template.** When `TRUSTED_NETWORKS` / `TRUSTED_PROXIES` are set as env vars, the matching UI fields are read-only — an attacker who got into a session can't widen the rules without host access.
- **Bounded scope on Arr writes.** The container only ever calls Arr endpoints needed for tag-mode operations: create/delete tag definitions, batch tag add/remove via `/api/v3/movie/editor` or `/api/v3/series/editor` (with `applyTags` hard-validated to `add` / `remove` — never `replace`), and patch the `releaseGroup` field on a movie file or episode file via read-modify-write (the rest of the record is preserved byte-for-byte). The optional `RenameFiles` command on Recover only fires when the user ticks "Trigger rename after fix" in the UI. No code path can delete a movie/episode/file, trigger search/grab/refresh, or modify quality profiles, custom formats, indexers, or download clients.
- **Optional Dolby Vision tooling installs from a pinned upstream.** When `ENABLE_DV_TOOLS=true`, the entrypoint downloads `dovi_tool` from the project's own GitHub releases at a pinned version. The download is over HTTPS with TLS verification on; if the download fails, DV-detail scanning is disabled rather than running with a substituted binary. ffmpeg comes from Alpine's signed package repository.

### What Resolvarr does NOT do (by design)

- **Encrypt secrets at rest.** Arr API keys and notification tokens live as plaintext in `/config/resolvarr.json` (mode 0600 — readable only by the container user). This matches Radarr/Sonarr themselves: both store their API keys as plaintext in `config.xml`. If an attacker has read access to `/config/`, no local-only key can meaningfully protect the file — any encryption key has to live on the same filesystem. A future opt-in env-var-derived key is on the roadmap if you want defense against backup-disk leaks or container-escape scenarios. Open an issue if you need it sooner.
- **Audit log of admin actions.** The Docker event stream and reverse-proxy access logs cover request-level history. A dedicated audit log per action is open to feature-request — open an issue.
- **Terminate TLS itself.** Runs plain HTTP on port 6075. Front it with SWAG / Traefik / Caddy / Nginx Proxy Manager for HTTPS, and add the proxy's IP to **Trusted Proxies** so `X-Forwarded-Proto: https` is honored (ensures Secure cookies are set).

## Security audit trail

Resolvarr's security implementation is backed by an internal review log — every finding from past code reviews is preserved with the fix and why it was flagged. This is a living internal document (not published to this repo) covering the hardening arc: authentication primitives, middleware wiring, sensitive-data redaction, CSRF, security headers, race conditions, information leakage, log injection, and supply-chain risks.

The first public release (`v0.3.0-dev`) was preceded by a focused safety audit of every Arr API mutation to confirm that no code path can perform destructive operations beyond the documented tag / recover scope. Requests for access to specific finding details can be made via the disclosure email above.

Current CI: `go test ./...` runs on every push to `main`.

## Changelog of security-relevant changes

See `CHANGELOG.md` — security-related changes are called out in the entry's overview line.

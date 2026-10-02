# tund — internal contract

This file is the contract between the three components. Change it together with the code.

```
 browser ──https──▶ tund-server (:443) ──yamux stream over wss──▶ tund client ──▶ localhost:3000
                     │  ├─ dashboard host → proxies to Next.js (:3000, internal)
                     │  └─ Postgres (shared with dashboard; LISTEN/NOTIFY for live updates)
 dashboard (Next.js) ─┴─ internal API (:4040, bearer TUND_INTERNAL_SECRET) for replay / stop
```

## Repository layout

| path | what |
| --- | --- |
| `cmd/tund` | client binary |
| `cmd/tund-server` | edge server binary |
| `internal/protocol` | wire protocol shared by client and server |
| `internal/client` | client implementation |
| `internal/server` | edge implementation; `migrations/*.sql` embedded and applied on startup |
| `internal/pwhash` | scrypt password hash format (mirrors `dashboard/src/lib/password.ts`) |
| `dashboard` | Next.js 16 management UI |
| `docker-compose.yml`, `.env.example` | deployment |

Go module path: `tund`.

## Hostnames

* `TUND_BASE_DOMAIN` e.g. `tund.example.com`. Tunnels get `<label>.<base>`; one label only.
* `TUND_DASHBOARD_HOST` default `dashboard.<base>`. Served by the edge:
  * `/_tund/ws` — client control connection (WebSocket)
  * `/_tund/install.sh`, `/_tund/install.ps1`, `/_tund/downloads/tund-<os>-<arch>[.exe]`, `…/checksums.txt`, `…/version.txt` (the newest client release, e.g. `0.4.0`; published with every release)
  * Client updates: the server reads `version.txt` from `TUND_DOWNLOADS_DIR` or `TUND_DOWNLOAD_BASE_URL` at start and hourly. A client connecting with an older release version (`X-Tund-Client-Version`) gets `{"type":"notice","update_version":"<latest>","error":"<text with the installer one-liner>"}` right after `welcome`; clients that know `update_version` show their own hint, older ones print `error`. Development builds (`dev`, `main-…`) never get it. `tund update` downloads `tund-<os>-<arch>` through these endpoints, checks it against `checksums.txt`, runs `<new> version` once and renames it over the running executable (Windows: the old one becomes `tund.exe.old`, removed on a later run).
  * `/_tund/oidc/start`, `/_tund/oidc/callback` — OIDC for protected tunnels
  * everything else → reverse proxy to `TUND_DASHBOARD_UPSTREAM` (Next.js)
* Reserved labels that can never be tunnel subdomains: `dashboard www api admin app connect edge tund mail smtp imap ftp ns ns1 ns2 status docs static assets cdn` plus the first label of the dashboard host.
* Custom domains: a verified `domains` row (`kind='custom'`). `*.dev.example.com` allows any single label below it.
* Ownership monitoring (migration 0018): verified custom domains keep proving ownership. Every node re-checks the domains that are due (claimed with `FOR UPDATE SKIP LOCKED` on `domains.checked_at`, so each check runs once in the cluster): hourly while the `_tund-challenge.<domain>` TXT record still contains `tund-verify=<token>`, every 10 minutes after a failed check, through the public resolvers 1.1.1.1/8.8.8.8/9.9.9.9. NXDOMAIN, no TXT record, or a TXT record without the value counts as a failure (`check_failures`); DNS errors (timeouts, SERVFAIL) don't count either way. After 3 failures in a row (about 20 minutes) the server withdraws the verification: `verified_at = NULL`, `unverified_at` and `unverified_reason` set, audit entry `domain.unverified` (actor `system`), `NOTIFY tund_config {"kind":"domain"}` (every node ends tunnels on the domain: "… lost its verification: its _tund-challenge TXT record is gone or changed; restore it and verify the domain again") and `NOTIFY tund_domain {"id"}`. A dashboard emails the domain's owner once (it claims `unverified_notified_at`; on start it also catches up on withdrawals of the last day). The domain shows "Verification lost" with the reason; verifying again restores it and resets the monitoring state. Binds and certificates already require a verified domain.

## Password hash format (users + tunnel passwords)

`scrypt$16384$8$1$<salt>$<hash>` — salt 16 random bytes, key length 32, both base64url without padding.
Node: `crypto.scryptSync(pw, salt, 32, {N:16384, r:8, p:1, maxmem: 64*1024*1024})`. Go: `scrypt.Key(pw, salt, 16384, 8, 1, 32)`.

## Tokens

* Client authtoken: `tund_` + 40 lowercase hex chars (20 random bytes). Stored as sha256 hex in `authtokens.token_hash`; `token_prefix` = first 13 chars (`tund_` + 8).
* Dashboard session cookie `tund_session`: 32 random bytes base64url; `sessions.id` = sha256 hex of it. 30 day expiry.

## Postgres notifications

| channel | sender | payload (JSON) |
| --- | --- | --- |
| `tund_requests` | server, after a request row is inserted | `{"id","user_id","tunnel_id","hostname"}` |
| `tund_tunnels` | server, when a tunnel goes online/offline | `{"id","user_id","hostname","event":"online"\|"offline"}` |
| `tund_config` | dashboard, after changing domains / providers / tokens | `{"kind":"domain"\|"oidc_provider"\|"authtoken_revoked","id":"<uuid>"}` |

On `tund_config` the server re-reads access policies of live tunnels; on `authtoken_revoked` it disconnects sessions using that token.

## Internal API (server, `TUND_INTERNAL_ADDR`, default `:4040`, not published)

All requests need `Authorization: Bearer $TUND_INTERNAL_SECRET`. JSON in, JSON out, errors as `{"error": "..."}`.

* `POST /internal/replay` `{"request_id","user_id","override"?:{"hostname","method","path","headers":{"K":["v"]},"body" | "body_base64"}}` → `200 {"request_id":"<new>"}`; `404` unknown request; `409` no online HTTP tunnel the user may replay through for that hostname. `hostname` sends the request through another online tunnel instead (one of the user's, or on a team domain they belong to); `headers` replaces all headers.
* `POST /internal/tunnels/stop` `{"tunnel_id","user_id"}` → `200 {}` (tells the client and unbinds).
* `GET /internal/health` → `200 {"ok":true}`.

## Client ↔ server wire protocol

1. Client dials `wss://<dashboard host>/_tund/ws` with headers
   `Authorization: Bearer <authtoken>`, `X-Tund-Version`, `X-Tund-OS` (`GOOS/GOARCH`), `X-Tund-Hostname`.
   Invalid token → HTTP 401 `{"error"}` before upgrade.
2. The WebSocket (binary messages) is wrapped as a `net.Conn` and runs yamux; the client is the yamux client.
3. The client opens the first stream: the **control stream**, newline-delimited JSON (`protocol.Message`).
   * S→C `welcome` `{session_id, account, server_version, dashboard_url}` (first message)
   * C→S `bind` `{id, bind:{name, subdomain, hostname, local_addr, host_header, auth}}`
   * S→C `bound` `{id, tunnel_id, url, auth_mode, expires_at?}` or `bind_error` `{id, error}` (`expires_at`: the server closes the tunnel then, see "Tunnel lifetime")
   * S→C `request` `{id, request:{request_id, method, path, status, duration_ms, remote_addr, error}}` (for the CLI log)
   * S→C `closed` `{id, error, code?}` — tunnel stopped from the dashboard, the API or the server (`code: "lifetime"`: maximum lifetime reached; the hostname is already free, so the client may bind again right away)
   * C→S `unbind` `{id}`
   * either side `error` `{error}` then close
4. For every upstream TCP connection the server opens a **data stream**, writes a frame
   `uint16 big-endian length + JSON {"tunnel": "<bind id>"}`, and waits for 1 status byte from the client:
   `0` = connected to local service, `1` = followed by a `uint16 length + message` error. After `0` the stream carries raw HTTP/1.1 bytes.
   The client dials `local_addr` (TLS with verification disabled when the scheme is `https`) and pipes both directions.

### Bind semantics

* `subdomain` empty and `hostname` empty → random `adjective-noun-1234` label. The client remembers the label per local address and re-requests it on reconnect so certificates are reused.
* `subdomain` set → allowed if valid, not reserved by another user, not in use by another online tunnel. If the user owns a `domains` row for it, that row's access policy applies.
* `hostname` set → must be a verified domain of this user (exact, or a label under a wildcard domain of this user), or a subdomain of the base domain (treated like `subdomain`).
* `host_header`: `""`/`preserve` (public host), `rewrite` (host of local_addr), anything else = literal value.
* `auth` (optional, overrides the domain policy): `{"mode":"password","password":"..."}` or `{"mode":"oidc","provider":"<slug>","allow":["a@b.com","@b.com"]}`.

## Access policies (visitor side, enforced by the edge)

* `none` — public.
* `password` — unauthenticated browsers get a login page (`/_tund/auth/login`); `Authorization: Basic` with any user name and the password also works for scripts.
* `oidc` — browsers are redirected via `https://<dashboard>/_tund/oidc/start` to the IdP; the callback (`https://<dashboard>/_tund/oidc/callback`, **the only redirect URI to register at the IdP**) hands a short-lived signed token back to `https://<tunnel>/_tund/auth/complete`, which sets the host cookie. Emails are checked against the allow list (`a@b.com` exact, `@b.com` domain; empty = anyone).
* Visitor cookie `_tund_auth` (HMAC-signed with `TUND_SECRET`, host-only, 7 days, bound to a fingerprint of the policy so changing it logs everybody out). The edge strips it before forwarding.
* Paths under `/_tund/` on tunnel hosts belong to the edge.

## Load-balanced tunnels (migration 0019)

`bind.pool: true` (CLI `--pool`, `tund.yml` `pool: true`; HTTP only, not with `random`) lets several tunnels of one account share a hostname. Members must agree on what visitors see: the `pool_key` is a keyed hash (HMAC with `TUND_SECRET`) of the client's `auth`, the IP allow list and the traffic rules; local address and host header may differ. A bind is refused when the hostname is held by a single tunnel, by another account, or by a pool with another key ("… is load-balanced by your other tunnels with different settings"); a single tunnel is refused while a pool holds the hostname ("add --pool to join them").

* Registry: a hostname maps to its members; each request goes to the next member on this node (round robin). Pool members on this node share one rate limiter.
* Nodes: visitors are served by the members on the node they reach; a node without members relays to a random live node that has some (`tunnels.node` of the online rows).
* `tunnels.pool_key` (`''` = single tunnel); the online-hostname unique index only covers single tunnels. `CreateTunnel` takes a transaction-scoped advisory lock on the hostname so pool members and single tunnels cannot race in from different nodes; it does not end this node's rows of the same pool.
* Passwords given by the client get a `PasswordTag` (HMAC of the password) that replaces the salted hash in the policy fingerprint, so visitor cookies are valid on every member and survive restarts.
* A member joining from the same machine with the same tunnel name replaces members whose session no longer answers (a client that reconnected).
* `bound.pool_size`: online members on all nodes, this one included. The dashboard marks members "Pool of N".

## Traffic rules (HTTP tunnels)

`bind.rules` (`protocol.Rules`), applied by the node holding the tunnel; TCP/TLS binds with rules are refused. Clients refuse to send rules to servers older than 0.5.0 (which would ignore them).

* `request_headers` / `response_headers`: `{"set":{"Name":"value"},"remove":["Name"]}` — removals first, then sets (replace). Request rules apply after the edge strips its own cookies and `X-Tund-*` headers and before the identity headers are set; response rules apply before the response is recorded, so the inspector shows what visitors got. `Host`, `Content-Length`, `Transfer-Encoding`, `Connection`, `Upgrade`, `Keep-Alive`, `Te`, `Trailer`, `Proxy-Connection` and `X-Tund-*` cannot be changed. At most 32 rules per direction.
* `cors`: `{"origins":["https://app.example.com" | "*"],"methods","headers","expose","credentials","max_age"}`. Preflights (`OPTIONS` with `Origin` and `Access-Control-Request-Method`) are answered by the edge with 204 right after the IP allow list and rate limit, before the browser warning and the access policy (preflights carry no cookies), and recorded. Responses get the local service's `Access-Control-*` headers replaced: `Access-Control-Allow-Origin` echoes an allowed origin (or `*`), plus `Allow-Credentials`/`Expose-Headers` when set, and `Vary: Origin`. Defaults: methods `GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS`, allowed headers = whatever the browser asks for, `max_age` 600. `credentials` needs explicit origins.
* `rate_limit`: `"<n>/s|m|h"` per visitor IP (token bucket of size n). Over the limit the edge answers 429 with `Retry-After` before anything else (also before the password form), without recording the request; the client gets a `notice` at most once a minute with the number of rejected requests. Up to 50 000 visitors are tracked per tunnel.
* `routes`: `[{"path":"/api","local_addr":"http://localhost:8080","strip_prefix":false}]` (at most 16). The longest matching prefix wins (`/api` matches `/api` and `/api/…`), everything else goes to `local_addr`. The data stream header carries `"route": n` (1-based index into `routes`, 0/absent = `local_addr`) and the client dials that address; `host_header: rewrite` uses the route's host. `strip_prefix` removes the prefix before forwarding.

CLI: `--route /api=8080`, `--request-header "Name: value"`, `--request-header-remove Name`, `--response-header …`, `--response-header-remove …`, `--cors <origin>`, `--rate-limit 100/m`; `tund.yml` keys `routes` (`path`, `addr`, `strip_prefix`), `request_headers`/`response_headers` (`set`, `remove`), `cors`, `rate_limit`.

## Server environment

| var | default | |
| --- | --- | --- |
| `TUND_BASE_DOMAIN` | required | |
| `TUND_DASHBOARD_HOST` | `dashboard.<base>` | |
| `TUND_DATABASE_URL` | required | |
| `TUND_SECRET` | required | HMAC key for visitor cookies / OIDC state |
| `TUND_INTERNAL_SECRET` | required | shared with dashboard |
| `TUND_DASHBOARD_UPSTREAM` | `http://dashboard:3000` | |
| `TUND_HTTP_ADDR` / `TUND_HTTPS_ADDR` / `TUND_INTERNAL_ADDR` | `:80` / `:443` / `:4040` | |
| `TUND_TLS_MODE` | `acme` | `acme`, `manual`, `off` (plain HTTP on HTTP_ADDR, for dev or behind another proxy) |
| `TUND_PUBLIC_SCHEME` / `TUND_PUBLIC_PORT` | `https` / `` | for building public URLs (dev: `http` / `8080`) |
| `TUND_ACME_EMAIL`, `TUND_ACME_CA` | | CA directory URL (e.g. LE staging) |
| `TUND_CERT_DIR` | `/data/certs` | certmagic storage |
| `TUND_DNS_PROVIDER`, `TUND_DNS_API_TOKEN` | | `hetzner` (Cloud DNS API), `hetzner-legacy`, `cloudflare`, `bunny` → wildcard cert for the base domain via DNS-01 |
| `TUND_TLS_CERT_FILE`, `TUND_TLS_KEY_FILE` | | `manual` mode: wildcard cert for the base domain |
| `TUND_CAPTURE_MAX_BODY` | `262144` | bytes stored per body |
| `TUND_RETENTION_DAYS` | `7` | |
| `TUND_DOWNLOADS_DIR` | `/downloads` | cross-compiled clients |
| `TUND_REDIRECT_HOSTS` | | comma-separated old dashboard hosts (after a domain move): pages 308-redirect to the dashboard, `/_tund/*` edge endpoints keep working in place for already-configured clients |

## Dashboard environment

`TUND_DATABASE_URL`, `TUND_BASE_DOMAIN`, `TUND_DASHBOARD_HOST`, `TUND_PUBLIC_SCHEME`, `TUND_PUBLIC_PORT`, `TUND_INTERNAL_URL` (`http://server:4040`), `TUND_INTERNAL_SECRET`, `TUND_SERVER_IP` (shown in custom-domain DNS instructions next to the public addresses the cluster nodes report; the instructions recommend a CNAME to the dashboard host, and the routing check accepts a CNAME to it or addresses of any node), `TUND_ALLOW_SIGNUP` (`false`; the first account is always allowed and becomes admin).

## Hosted service mode (migration 0002)

The same code runs a public service (e.g. tund.io) or a private self-hosted install. Service behaviour is switched on by env vars, not a separate build.

### Default server

Every client binary talks to the hosted cloud instance by default: `tund/internal/client.DefaultServer` (source default `https://tund.io`, overridable via ldflags for forks). To use a self-hosted instance you log into it once — `tund login https://tund.example.com` — which saves that server **and** the token in the config; every later command uses it automatically. Precedence: `--server` > `TUND_SERVER` > config file > `DefaultServer`. `tund logout` removes the token and the saved server (back to the cloud default). The install script served by a self-hosted instance runs `tund config set-server` for it, so its users land on that instance too.

### `tund login` — device authorization

`tund login [server-url]` — with a URL it targets (and on success saves) that server, otherwise the effective server from the precedence above. With a browser on the same machine the login finishes through a **loopback callback** (one click, nothing to compare); otherwise (`--no-browser`, SSH, Linux without a display) it is a plain device flow with a code.

1. The CLI generates its authtoken locally (`tund_` + 40 hex) and calls
   `POST <server>/_tund/device/code` with JSON `{"token_hash": sha256hex(token), "token_prefix": token[:13], "client_hostname", "client_os"}`
   → `200 {"device_code", "user_code", "verification_url", "verification_url_complete", "interval", "expires_in"}`.
   `user_code` is 8 characters from `BCDFGHJKLMNPQRSTVWXZ` formatted `XXXX-XXXX`; `verification_url` is `<dashboard>/device`, `…_complete` adds `?code=<user_code>`. Codes expire after 10 minutes. `429 {"error"}` when an IP creates too many codes.
   For a callback login the CLI first listens on `127.0.0.1:<random port>` and adds `"callback_port"` (1024–65535) and `"callback_state"` (16–128 URL-safe characters, random); the response then also carries `"callback_done_url"` (`<dashboard>/device/done`). A server that doesn't send it doesn't support callbacks, and the CLI shows the code instead.
2. The CLI prints the code and URL (callback login: just the URL), opens the browser, and polls `POST <server>/_tund/device/token` `{"device_code"}` every `interval` seconds:
   * `428 {"error":"authorization_pending"}` — keep polling (also while a callback login is `authorized` but not yet redeemed)
   * `200 {"status":"approved","account":"<email>"}` — save the token locally; the row is deleted
   * `403 {"error":"access_denied"}` / `410 {"error":"expired_token"}` — stop
3. The dashboard page `/device` (login required; preserves `?code=` through login and sign-up via `next`) shows the pending request (client hostname, OS, IP, time). **Deny** sets `status='denied'`.
   * Device flow: the page asks the user to check that the code matches their terminal. **Approve** runs in one transaction: lock the pending, unexpired `device_codes` row by `user_code`; insert `authtokens (user_id, name = 'CLI on <client_hostname>', token_hash, token_prefix)`; set `status='approved', user_id, authtoken_id`.
   * Callback login (`callback_port` set): no code to compare. **Approve** sets `status='authorized', user_id` and `callback_code_hash = sha256hex(c)` for a fresh random `c` (32 bytes, base64url), and sends the browser to `http://127.0.0.1:<callback_port>/callback?state=<callback_state>&code=<c>`. Approving again (same user, e.g. after a failed redirect) issues a new `c`. The CLI checks `state`, then redeems `POST /_tund/device/token {"device_code", "callback_code": c}`: the server locks the row, checks the hash, inserts the authtoken, sets `status='approved', authtoken_id` and answers like the poll (`200`, `403`, `410`), or `400 {"error":"invalid_callback_code"}` for a wrong or stale code (the CLI keeps waiting). Redeeming an already approved row with the right code answers `200` again. On success the CLI redirects the browser (`303`) to `callback_done_url`; failures get a small page from the CLI. The token only exists once the code has reached the CLI, so a login link sent to someone else is useless to the sender: approving it hands the code to the approver's own machine.
4. `tund http …` without an authtoken on an interactive terminal runs the login flow first, then starts the tunnel. Non-interactive: exit with a hint to run `tund login`.

### Accounts

* `users.disabled_at` set → the dashboard refuses logins and deletes the user's sessions; the server rejects the user's authtokens (`403 {"error":"account disabled"}`) and, on `tund_config` `{"kind":"user_disabled","id":"<user id>"}`, disconnects their clients.
* Sign-up (`TUND_ALLOW_SIGNUP=true`) also shows a public landing page on `/` for signed-out visitors; otherwise `/` redirects to `/login`.

### Two-factor authentication and passkeys (migration 0020)

Dashboard sign-in only (authtokens and the CLI are unchanged; `tund login` approves in a signed-in browser).

* **Authenticator app (TOTP, RFC 6238):** SHA-1, 6 digits, 30 s, one step of drift either side. Settings → Two-factor: "Turn on" creates a secret (kept in an `auth_flows` row of purpose `totp_setup`, 15 min) and shows a QR code (`otpauth://totp/<instance>:<email>?issuer=<instance>`) plus the key; the first valid code turns it on and returns 10 recovery codes (`xxxxx-xxxxx`, stored as SHA-256, shown once). `users.totp_secret` is encrypted like the SMTP password (AES-256-GCM, key from `TUND_INTERNAL_SECRET`); `totp_last_step` makes every code single-use. New recovery codes need an authenticator code; turning it off needs an authenticator or recovery code. Admins can turn it off for a user (Admin → Users → user), who gets an email.
* **Signing in with two-factor on:** password and Google/GitHub sign-ins don't create a session; they start a `second_factor` flow (cookie `__Host-tund_second_factor`, 10 min, 5 wrong codes) and redirect to `/login/two-factor`, which takes an authenticator code, a recovery code, or a passkey. Wrong codes in a row are counted on the user (`second_factor_failures`): from 10 on, the second step locks for 15 min, doubling every further 10 (max 24 h; passkeys still work); at 5 the owner gets an email that someone has their password. A success resets the count.
* **Passkeys (WebAuthn, `@simplewebauthn`):** relying party ID = `TUND_DASHBOARD_HOST`, origin = the dashboard URL exactly (so pages on tunnel subdomains can't use assertions). Registration (`POST /api/auth/passkey/register`, signed in) asks for discoverable credentials (`residentKey: required`); `user_passkeys` keeps the credential id, COSE public key, counter, transports, name and backup state. `POST /api/auth/passkey/login` `{step:"options", mode:"login"|"second_factor"}` / `{step:"verify", response}`: on its own (login page, no email typed) a passkey needs user verification and signs in directly; as the second step it needs presence and finishes the `second_factor` flow. A signature counter that doesn't increase is refused. Challenges live in `auth_flows` (5 min) behind a cookie, so any dashboard node can finish them.
* The last way to sign in (password, Google/GitHub account, passkey) can't be removed. Turning two-factor on or off, adding or removing a passkey and using a recovery code are written to the audit log and emailed to the owner when email is configured.

### Sign-in with Google and GitHub (migration 0014)

Admins enable providers under `/admin/sign-in`; the `settings` row `oauth` holds `{"google"|"github": {"enabled", "client_id", "client_secret_enc"}}` (secret AES-256-GCM like the SMTP password, key from `TUND_INTERNAL_SECRET`). A provider appears on `/login` and `/signup` ("Continue with …") when it is enabled and its secret decrypts. Redirect URI to register: `<dashboard>/auth/oauth/<google|github>/callback`.

* `GET /auth/oauth/<p>/start?next=…` (or `?intent=link`, signed in) sets the `tund_oauth` cookie (path `/auth/oauth`, HttpOnly, SameSite=Lax, 10 min, one use: provider, random `state`, PKCE verifier, `next`, intent) and redirects to the provider. Google: `openid email profile`, PKCE S256, `prompt=select_account`; GitHub: `read:user user:email`.
* The callback checks `state` against the cookie, exchanges the code with the client secret and reads the account: Google `userinfo` (`sub`; email only when `email_verified`), GitHub `/user` (numeric `id`) and `/user/emails` (the primary verified address, else any verified one).
* `user_identities (user_id, provider, subject, email)`, unique per `(provider, subject)` and `(user_id, provider)`. Sign-in: a known identity signs its user in; otherwise an account with the same (verified, lowercased) email is connected and signed in (`email_verified_at` set if missing); otherwise a new account is created under the sign-up rules: `signup_mode` (invite: `next` must be a valid invite whose email matches), disposable addresses, `signup_rate_limit`; Turnstile is not asked. New accounts get `password_hash = NULL` (they may set a password without the current one under Settings) and a verified email; "Continuing … you agree to the Terms and the Acceptable Use Policy" is shown next to the buttons while sign-up is possible. Disabled accounts are refused.
* Errors go back to `/login?error=<code>` (link: `/settings?oauth_error=<code>`) with fixed messages: `state`, `cancelled`, `failed`, `unavailable`, `no_email`, `disabled`, `signup_closed`, `signup_invite`, `invite_email`, `disposable`, `rate`, `taken`, `last_method`.
* Settings → *Sign-in methods*: connect (`intent=link`; an identity already on another user → `taken`) and disconnect (refused with `last_method` unless a password or another identity remains). Audit: `user.signup` (with provider), `user.identity_link`, `user.identity_unlink`.

### Limits (0 = unlimited)

| var | enforced by | |
| --- | --- | --- |
| `TUND_MAX_TUNNELS_PER_USER` | server | concurrent online tunnels per account; bind_error `"tunnel limit reached (N online tunnels per account)"` |
| `TUND_MAX_DOMAINS_PER_USER` | dashboard | custom domains per account (static hostnames: `TUND_MAX_PINNED_PER_USER`, see below) |

Admins are exempt from both limits.

## Static hostnames (migration 0003)

A static hostname is a subdomain of the base domain pinned to an account: a `domains` row with `kind='subdomain'` (verified on creation). In the UI they are called **static hostnames**; custom domains stay separate. `domains.is_default` marks at most one per account (partial unique index) as the **default**.

### Bind resolution (server)

In order:

1. `hostname` set → as before (custom domain, or a base subdomain treated like `subdomain`).
2. `subdomain` set and `auto` false → explicit request, as before.
3. `random` false and the account has a default static hostname that is not online → use it.
4. `random` false, the account has **no** static hostnames yet, `TUND_AUTO_PIN` is on (default `true`) and the static-hostname limit allows → create a random static hostname with `is_default = true` and use it (first run of a new account gets a stable URL).
5. `subdomain` set with `auto` true (remembered label) → use it if free and not pinned by another account.
6. Otherwise a random throwaway label.

`pin: true` → after resolving, if the hostname is a base subdomain not yet pinned by this account, insert it as a static hostname (becoming the default if the account has none). If the limit is reached the tunnel still binds and `bound.warning` explains why it was not pinned. `bound.static` is true when the hostname is one of the account's static hostnames.

### Limits

`TUND_MAX_PINNED_PER_USER` (server + dashboard) caps static hostnames per account; `TUND_MAX_DOMAINS_PER_USER` (dashboard) now caps **custom** domains only. 0 = unlimited; admins exempt. With 0 (unlimited) auto-pinning always happens unless `TUND_AUTO_PIN=false`.

### Client

* `tund http 3000` → default static hostname (created on first use).
* `tund http 3000 --subdomain myapp --pin` → claim `myapp` and keep it.
* `tund http 3000 --random` → throwaway hostname (skip the default).
* The remembered-label state file is still used, sent with `auto: true`.
* The UI marks static hostnames (e.g. `[static]`) and prints `bound.warning`.

### Dashboard

* `/domains`: "Static hostnames" section — list (default star, online status, access policy), "Make default", "Claim random" and "Choose a name" (label rules + reserved labels), remove; usage vs `TUND_MAX_PINNED_PER_USER`. "Custom domains" section separately with `TUND_MAX_DOMAINS_PER_USER`.
* `/tunnels`: "Pin" on tunnels whose base-domain hostname is not pinned by the user → creates the static hostname (fails with a friendly error if another account pinned it meanwhile or the limit is reached).
* Overview / get-started: show the account's default static hostname URL when it exists.
* After any change: `NOTIFY tund_config {"kind":"domain","id"}` as before.

## Inspector (dashboard)

* **Body search**: the "Bodies" toggle next to the path filter (`/api/requests?q=…&body=1`) also matches request and response bodies: case-insensitive for ASCII (`encode(body, 'escape') ilike`), exact UTF-8 bytes otherwise. Compressed bodies are not searched. Live events can't be matched without bodies, so the list is reloaded (at most once a second) when one arrives.
* **HAR export**: `GET /api/requests/har?<filters>` (same filters as the list) or `?id=<uuid>` (repeatable) → HAR 1.2 download of the newest 200 matching requests, oldest first. Bodies are decoded (`Content-Encoding`) and included as text, or base64 (`content.encoding: "base64"`, request `postData._encoding`) when not UTF-8; beyond 32 MB of bodies they are left out with a `comment`. Timings: `wait` = time to first byte, `receive` = the rest. Custom fields `_id`, `_remoteAddress`, `_replayOf`.
* **Compare**: "Compare" on a request, then pick a second one; the detail pane shows both side by side (request, host, status, timing, sizes) and line diffs (Myers) of the request (request line, headers, body) and response, with long unchanged runs folded. Shareable as `/inspect?id=<a>&compare=<b>`.
* **Edit & replay**: method, path (a pasted full URL also picks the tunnel), headers, text body, and the tunnel to send it through (the user's online HTTP tunnels, including team-domain ones), via `override` on `/internal/replay`. Binary or truncated bodies are sent as captured.

## Public API (v1) and MCP

### HTTP API on the edge

Served by tund-server on the dashboard host under `/_tund/api/v1/` (so it also works on `TUND_REDIRECT_HOSTS`). Auth: `Authorization: Bearer <authtoken>` (same tokens as the client). JSON responses; errors `{"error": "..."}` with 400/401 (bad token)/403 (disabled account)/404/409 (tunnel offline)/429. Everything is scoped to the token's account.

| method + path | response |
| --- | --- |
| `GET /me` | `{"account":{"id","email","name","is_admin"},"server":{"base_domain","dashboard_url","version"},"limits":{"tunnels","pinned","domains"},"static_hostnames":[{"hostname","url","default"}]}` (limits: 0 = unlimited) |
| `GET /tunnels` | `{"tunnels":[{"id","name","hostname","url","local_addr","auth_mode","static","started_at","expires_at","node","client":{"hostname","os","version"}}]}` — online tunnels of the account on all machines (`tund status`); `expires_at` is null without a maximum lifetime |
| `POST /tunnels/{id}/stop` | `{}` |
| `GET /requests?hostname=&tunnel_id=&method=&status=2xx\|3xx\|4xx\|5xx&path=<substring>&limit=<1..200, default 50>&before=<RFC3339Nano>` | `{"requests":[Summary],"next_before":"<started_at of the last row or empty>"}` newest first |
| `GET /requests/{id}` | `Summary + {"proto","request":{"headers":{"K":["v"]},"body":Body},"response":{"headers","body":Body}}` |
| `POST /requests/{id}/replay` | body optional `{"hostname","method","path","headers":{"K":["v"]},"body":"<text>"}` or `"body_base64"`; → `{"request_id","status"}` (the new row is written within ~1 s). `hostname`: replay through another of your online tunnels; `headers` replaces all headers (the MCP tool merges its `headers` into the original ones) |

`Summary = {"id","tunnel_id","hostname","method","path","status","duration_ms","ttfb_ms","req_body_size","resp_body_size","remote_addr","error","replay_of","started_at"}` (`status` 0 = no response, see `error`).

`Body = {"size","truncated","content_type","content_encoding","decoded","text","base64"}`: the server undoes `Content-Encoding` gzip/deflate/br/zstd when it can (`decoded: true`); `text` holds the (decoded) body when it is valid UTF-8 text, otherwise `base64` holds the raw captured bytes. `size` is the full size on the wire; `truncated` means only the first `TUND_CAPTURE_MAX_BODY` bytes were kept.

### `tund mcp`

A Model Context Protocol server over stdio built into the CLI, so AI agents can expose what they build: `claude mcp add tund -- tund mcp`, or `{"mcpServers":{"tund":{"command":"tund","args":["mcp"]}}}` for other clients. It uses the normal client config (server + authtoken; `--server`/`--authtoken`/env work too). Tunnels it starts run inside the `tund mcp` process and stop when the agent session ends.

Flags: `--allow-ports 3000,5173` (only these local ports may be exposed), `--require-password` (every tunnel it starts gets a random password unless one is given), `--no-browser`.

Tools (names are stable API for prompts):

| tool | input | result |
| --- | --- | --- |
| `whoami` | — | server, account, static hostnames, limits, login state |
| `login` | `{wait_seconds?}` | starts the device flow (or reports "already logged in"); returns the code + URL for the user; polls in the background, `wait_seconds` (≤120) blocks until done |
| `start_tunnel` | `{target, subdomain?, pin?, random?, password?, host_header?, name?}` (`target`: `3000`, `host:port` or `http(s)://…`) | waits until bound (≤20 s) → `{id, url, static, auth, inspector_url, warning?}`; warns when nothing is listening on the target yet |
| `list_tunnels` | `{all?}` | tunnels started by this MCP session (with state); `all: true` adds the account's other online tunnels via `GET /tunnels` |
| `stop_tunnel` | `{id \| url}` | stops a tunnel of this session, or any account tunnel via the API |
| `list_requests` | `{tunnel? (id/url/hostname), method?, status?, path?, limit?}` | compact summaries, newest first |
| `get_request` | `{id, max_body_chars? (default 20000)}` | headers + decoded bodies (truncated to fit the agent's context) |
| `replay_request` | `{id, method?, path?, headers?, body?}` | replays through the tunnel, waits for the new capture, returns it like `get_request` |

Read-only tools carry `readOnlyHint`; `start_tunnel` descriptions tell the agent that the URL is public and to prefer `password` for anything sensitive.

## Browser warning page (migration 0004)

Anti-phishing interstitial for public services, like ngrok's. `TUND_BROWSER_WARNING=true` (default `false`) turns it on; `TUND_ABUSE_CONTACT` (email or URL, optional) adds a "Report abuse" link.

The edge shows the warning (HTTP 200, `Cache-Control: no-store`, `X-Tund-Warning: 1`) instead of proxying when **all** of these hold:

* the tunnel's hostname is under the base domain (custom domains never warn);
* the tunnel's access policy is `none` (password/OIDC-protected tunnels never warn);
* the tunnel owner is not an admin and not `users.trusted`;
* the request is `GET`/`HEAD`, `Accept` contains `text/html`, the `User-Agent` contains `Mozilla/`, and there is no `Tund-Skip-Browser-Warning` header (any value);
* the path is not under `/_tund/`;
* the visitor has no valid `_tund_warned` cookie for this host (HMAC-signed with `TUND_SECRET`, bound to the hostname, 7 days; all cookies of that name are checked because other tunnels can plant cookies on the parent domain).

"Visit site" is a form `POST /_tund/warning/accept` (`next=<original path>`) → sets the cookie → `303` to `next` (same-origin paths only). Warning pages are not recorded as requests.

`bound.browser_warning` tells the client whether visitors of that tunnel will see the page; the CLI shows a hint with the skip header, and the MCP `start_tunnel` result includes it. Admins toggle `users.trusted` in the dashboard (Admin → Users, "Trusted — no browser warning") and send `NOTIFY tund_config {"kind":"user_updated","id":"<user id>"}` so live tunnels pick it up. The edge also strips `Tund-Skip-Browser-Warning` before forwarding.

## Teams (migration 0005)

Teams let several accounts share **OIDC providers** and **domains** (static hostnames and custom domains). Resources keep `user_id` as their creator; a non-null `team_id` means the team owns them.

* Roles: `owner` (everything incl. delete team, manage owners), `admin` (members except owners, invites, team providers and domains), `member` (use team providers and domains, see traffic on team domains). A team always keeps at least one owner. Team `slug`: `^[a-z0-9](?:[a-z0-9-]{0,38}[a-z0-9])?$`, unique.
* Invites (`team_invites`): link `https://<dashboard>/invite/<token>` (token 32 random bytes base64url, stored as sha256 hex), 7 days, single use, optionally bound to an email (case-insensitive). Adding an **existing** user by email adds them directly (no invite needed). Accepting requires login/sign-up (`next` preserved).
* Limits: `TUND_MAX_TEAMS_PER_USER` (teams a non-admin user may **own**, default 3; 0 = unlimited). `TUND_MAX_PINNED_PER_USER` and `TUND_MAX_DOMAINS_PER_USER` apply per team for team-owned domains.
* Dashboard NOTIFY after membership/role changes, team deletion, or team domain/provider changes: `tund_config {"kind":"team","id":"<team id>"}` (plus the existing `domain` / `oidc_provider` kinds).

### Server rules

* **Binding team domains:** a domain row with `team_id` may be bound by any member of that team (custom domains: verified as before; base subdomains: like a static hostname). `bound.static` is true for team static hostnames too. `--pin` and auto-pin only create **personal** static hostnames; the default static hostname is personal only (`DefaultStatic`/`CountStatic` ignore team rows).
* **Losing access:** on `team`/`domain` notifications the server re-checks every live tunnel on a team-owned hostname and closes it (`closed`, "you are no longer a member of team <slug>") when its owner is no longer a member or the domain left the team.
* **OIDC provider references** from the client (`--oidc`): `<team-slug>/<provider-slug>` → that team's provider (caller must be a member); `<provider-slug>` → the caller's personal provider, else the unique match among providers of the caller's teams (ambiguous → bind_error naming the candidates as `team/slug`). Domain policies store the provider id; the dashboard only offers providers the domain's owner (user, or team for team domains) may use.

### Traffic visibility

Requests stay attributed to the tunnel owner (`requests.user_id`). Additionally, every member of a team can list/view requests whose `hostname` is a team-owned domain of that team (dashboard inspector, SSE stream, and the public API `/requests` + `/requests/{id}`; replay is allowed for team members too). `GET /_tund/api/v1/me` gains `"teams":[{"slug","name","role"}]`.

## OIDC identity headers

When a visitor passed an access policy, the edge adds identity headers to the request it forwards to the local app. **Any `X-Tund-*` request header sent by the visitor is always removed first**, on every tunnel, so apps can trust these headers on requests that arrive through tund.

| header | value |
| --- | --- |
| `X-Tund-Auth` | `oidc` or `password` |
| `X-Tund-User-Email` | `email` claim (OIDC) |
| `X-Tund-User-Email-Verified` | `true`/`false`, when the IdP sends `email_verified` |
| `X-Tund-User-Name` | `name` claim |
| `X-Tund-User-Username` | `preferred_username` claim (falls back to `nickname`) |
| `X-Tund-User-Id` | `sub` claim |
| `X-Tund-User-Groups` | `groups` claim (strings), comma-separated; commas inside names are dropped |
| `X-Tund-Idp` | issuer URL |

Values are UTF-8 with control characters removed; absent claims mean absent headers. The claims live in the (HMAC-signed) visitor cookie; at most 40 groups / 1500 bytes of group names are kept. Captured requests (inspector/API) include these headers as sent to the app.

Allow lists additionally accept `group:<name>` entries (exact, case-sensitive match against the `groups` claim). Providers need the right scopes for these claims (`profile` for name/username; many IdPs need a `groups` scope or claim mapping).

## Admin area, settings and email (migration 0006)

### Settings (`settings` table)

Key → JSON value. A stored value overrides the env default; deleting the row falls back to the env/built-in default. After a change the dashboard sends `NOTIFY tund_config {"kind":"settings"}`; tund-server reloads settings on that notification (and at start) — no restart.

| key | type | env default | used by |
| --- | --- | --- | --- |
| `signup_mode` | `"open"` \| `"invite"` \| `"closed"` | `TUND_ALLOW_SIGNUP` true → open, else closed | dashboard (invite = only with a valid team invite link; the very first account is always allowed) |
| `require_email_verification` | bool | false | dashboard (new accounts must click the emailed link before approving CLI logins / creating tokens) + server (tokens of unverified accounts get `403 {"error":"verify your email address first …"}`); only effective while SMTP is configured |
| `custom_domains` | bool | `TUND_CUSTOM_DOMAINS` (false) | server (binding a hostname outside the base domain) + dashboard (adding custom domains, UI copy): whether accounts may use custom domains. `users.custom_domains` (migration 0013; NULL = this setting, where admins count as on; true/false) overrides it per account. The server re-checks live custom-domain tunnels on `settings` and `user_updated` and ends them with `custom domains are not enabled for your account …`. `untrusted_custom_domains` still applies on top for non-trusted accounts |
| `passthrough` | bool | `TUND_PASSTHROUGH` (false) | server (binding `tcp` and `tls` tunnels) + dashboard (reserving static TCP ports, UI copy): whether accounts may open TCP and TLS passthrough tunnels. `users.passthrough` (migration 0016; NULL = this setting, where admins count as on; true/false) overrides it per account. The server refuses such binds before allocating anything (bind_error `TCP and TLS tunnels are not enabled for your account …`), re-checks live TCP/TLS tunnels on `settings` and `user_updated` and ends them with the same reason. `untrusted_tcp` / `untrusted_tls` still apply on top for non-trusted accounts |
| `limit_tunnels`, `limit_pinned`, `limit_domains`, `limit_teams` | int (0 = unlimited) | `TUND_MAX_TUNNELS_PER_USER`, `…_PINNED_…`, `…_DOMAINS_…`, `…_TEAMS_…` | server (tunnels, pinned) + dashboard (pinned, domains, teams) |
| `auto_pin` | bool | `TUND_AUTO_PIN` | server |
| `browser_warning` | bool | `TUND_BROWSER_WARNING` | server + dashboard badges |
| `abuse_contact` | string | `TUND_ABUSE_CONTACT` | server + dashboard |
| `retention_days` | int | `TUND_RETENTION_DAYS` | server |
| `capture_max_body` | int bytes (1024 … 10485760) | `TUND_CAPTURE_MAX_BODY` | server |
| `instance_name` | string | `"TUNd"` | dashboard (wordmark, page titles, link previews, sign-in copy, emails) + server (the edge's own pages: offline, 404, warning, …) |
| `notify_admins_on_signup` | bool | false | dashboard |
| `smtp` | `{"host","port","security":"starttls"\|"tls"\|"none","username","password_enc","from_email","from_name"}` | none | dashboard |

`smtp.password_enc` = base64url(nonce(12) ‖ AES-256-GCM ciphertext) with key `sha256("tund-settings\x00" + TUND_INTERNAL_SECRET)`; the password is never sent back to the browser (the form shows "unchanged" unless replaced).

### Email flows (dashboard, nodemailer)

* **Verification**: on sign-up (when required) send a link `/verify-email/<token>` (24 h); resend from a banner. Verified → `users.email_verified_at = now()`.
* **Password reset**: `/forgot-password` (always answers "if the address exists, we sent a link" — no account enumeration; rate limited per IP and per address) → `/reset-password/<token>` (1 h, single use) → new password, all sessions of the user deleted.
* **Team invites**: when a team admin invites an address, the invite link is also emailed (if SMTP is configured).
* **Admin notice** of new sign-ups when `notify_admins_on_signup`.
* Tokens: 32 random bytes base64url, stored as sha256 hex in `email_tokens`; one active token per user and kind (new token invalidates older ones).
* Plain, good-looking HTML + text emails branded with `instance_name`.

### Audit log

Every admin action and security-relevant account event is appended to `audit_log` by the dashboard: `settings.update` (details = changed keys, never secrets), `smtp.test`, `user.create|delete|disable|enable|admin|unadmin|trust|untrust|verify|reset_password|custom_domains|passthrough`, `tunnel.stop` (admin), `team.delete` (admin), `domain.delete` (admin), `auth.password_reset` (by the user). Admins see it paginated and filterable under `/admin/audit`.

### Internal API additions (tund-server)

* `GET /internal/status` → `{"version","started_at","base_domain","dashboard_host","tls_mode","dns_provider","tunnels","sessions","recorder_queue","certificates":[{"name","not_after","issuer"}]}` (certificates: the wildcard `*.<base>` and the dashboard host when present in the cache).
* `POST /internal/admin/tunnels/stop` `{"tunnel_id","reason"}` → stops any tunnel (the dashboard has checked that the caller is an admin); the client sees `reason` (default "stopped by an administrator").

### Admin pages (`/admin/*`, `users.is_admin` only; 404 for others)

`/admin` overview (edge status incl. certificate expiry, counts, requests/24h, top accounts by traffic, recent sign-ups), `/admin/users` (+ `/admin/users/[id]` detail), `/admin/tunnels` (all online tunnels, stop with reason), `/admin/teams`, `/admin/domains`, `/admin/settings`, `/admin/email` (SMTP form + "send test email" to any address + template previews), `/admin/sign-in` (Google/GitHub sign-in, see "Sign-in with Google and GitHub"), `/admin/audit`.

## TCP and TLS tunnels, IP allow lists (migration 0007)

`bind.proto`: `http` (default), `tcp`, `tls`. `tunnels.proto` / `tunnels.remote_port` record it.

### TCP

* Server env `TUND_TCP_PORTS` (e.g. `20000-20999`; empty = TCP tunnels disabled → bind_error "TCP tunnels are not enabled on this server") and `TUND_TCP_HOST` (hostname shown in URLs, default the dashboard host). The edge listens on the chosen port per tunnel (host networking; see deployment).
* Port choice: `remote_port` without `auto` → exactly that port (must be in range, not online, not reserved by someone else); with `auto` → preferred if available; otherwise a random free port. `pin: true` → reserve it (`tcp_reservations`, personal). Reserved ports of the account are used like static hostnames: a TCP bind without an explicit port prefers the account's **first reserved port that is not online** before falling back to auto/random. Team-owned reservations (team_id) are usable by all members.
* Static addresses limit: pinned TCP ports and static hostnames together count toward `limit_pinned` ("static addresses").
* TCP and TLS tunnels need the `passthrough` feature on the account (setting, default off, plus the per-account override; see Settings). Without it the bind fails before a port or hostname is allocated, and the dashboard doesn't offer static TCP ports.
* `bound.url = "tcp://<TUND_TCP_HOST>:<port>"`, `bound.remote_port = <port>`, `bound.static` = reserved by the account/team.

### TLS passthrough

* Hostname rules are exactly those of HTTP tunnels (random, static hostname, team domain, verified custom domain; `subdomain`/`hostname`/`auto`/`pin` as for http). Requires `TUND_TLS_MODE` acme or manual.
* The edge peeks the ClientHello on :443: SNI of an online `tls` tunnel → the raw TLS bytes (including the peeked ones) are piped through the tunnel; the edge never decrypts. Everything else → normal HTTPS handling. On :80 a `tls` hostname gets a redirect to https like other hosts.
* The edge does not obtain certificates for `tls` tunnels. Visitors see the certificate of the local service — use a custom domain with your own certificate, or terminate at the client: `tund tls 8443 --terminate-cert cert.pem --terminate-key key.pem` (client wraps the stream with that cert and forwards plaintext to the local address).
* `bound.url = "tls://<hostname>"`.

### IP allow lists (all protocols)

`bind.allow_ips` — IPs or CIDRs (v4/v6). The edge checks the visitor's address before anything else: HTTP → 403 page "your IP address is not allowed", TCP/TLS → connection closed. Invalid entries → bind_error. Access policies (password/OIDC), identity headers and the browser warning apply to HTTP only.

### Connection records

Every finished TCP/TLS connection → `connections` row (bytes visitor→local = `bytes_in`, local→visitor = `bytes_out`) + `NOTIFY tund_connections {"id","user_id","tunnel_id","address"}` + control message `connection` to the client. Retention like requests. Visibility for teams: connections on team-owned hostnames/ports are visible to team members.

Public API: `GET /_tund/api/v1/connections?tunnel_id=&address=&limit=&before=` → `{"connections":[{"id","tunnel_id","proto","address","remote_addr","bytes_in","bytes_out","duration_ms","error","started_at"}],"next_before"}`; `GET /tunnels` items gain `proto` and `remote_port`.

### Client

* `tund tcp <port|host:port>` (`--remote-port N`, `--pin`, `--name`, `--allow-ip …`), `tund tls <port|host:port>` (hostname flags like http: `--subdomain`, `--domain`, `--pin`, `--random`; plus `--terminate-cert/--terminate-key`, `--allow-ip`), `--allow-ip` also on `tund http`. tund.yml tunnels gain `proto`, `remote_port`, `allow_ips`, `terminate_cert`, `terminate_key`. The remembered-port state works like remembered labels (`auto: true`). The CLI prints `connection` events as log lines (remote, bytes, duration).
* MCP: `start_tunnel` gains `proto`, `remote_port`, `allow_ips`; new read-only tool `list_connections`.

## Edge nodes (migrations 0010, 0011)

Goal: serve clients and visitors from the nearest location with **one global domain** (GeoDNS or anycast, e.g. Bunny Magic Containers anycast endpoints). Every `tund-server` process is a **node**; all nodes share the Postgres database.

### Configuration

| var | default | |
| --- | --- | --- |
| `TUND_NODE_NAME` | hostname | unique node id, e.g. `fsn-1`, `ash-1` |
| `TUND_NODE_REGION` | `""` | free text shown in the dashboard (`eu-central`, `us-east`) |
| `TUND_ROLE` | `control` | `control`: next to the dashboard (proxies the dashboard host to `TUND_DASHBOARD_UPSTREAM`); `edge`: routing only — dashboard-host requests (except `/_tund/ws`, install scripts, downloads) are relayed to a control node |
| `TUND_RELAY_ADDR` | `:4443` | listener for node-to-node traffic (TLS) |
| `TUND_RELAY_URL` | `""` | how other nodes reach this node's relay, `host:port` (empty = single-node mode, relay disabled) |
| `TUND_PUBLIC_IP` | detected | the address DNS should publish for this node; detected from the outbound route when it is a public address (set it behind NAT) |
| `TUND_NODE_CAPACITY_MBPS` | `0` | uplink capacity, shown next to network usage (0 = unknown) |

Nodes need the same `TUND_SECRET`, `TUND_BASE_DOMAIN`, `TUND_DASHBOARD_HOST`, DNS provider settings and database. The relay port must be reachable between nodes only (firewall).

### Shared state

* `nodes(name pk, role, region, relay_url, relay_cert_sha256, version, started_at, last_seen)` — heartbeat every 10 s; a node is **alive** while `last_seen > now() - 45 s`.
* Host metrics (migration 0015), written with every heartbeat: `public_ip`, `capacity_mbps`, `cpus`, `cpu_pct` (busy share of all cores since the previous heartbeat), `load1`, `mem_total`/`mem_used` (bytes, used = total − available), `net_in_rate`/`net_out_rate` (bytes/s on the interface holding `public_ip`, or all physical interfaces), `tunnel_rate` (bytes/s through tunnels, both directions), `sessions`, `tunnels`, `metrics_at`. Read from `/proc` (host values: the server runs with host networking); NULL where not measured (non-Linux, first heartbeat). Admins see them under `/admin/nodes`, refreshed every 10 s. External tools (e.g. a DNS balancer) can read the same table.
* `tunnels.node` / `agent_sessions.node`: where a tunnel lives. On start a node ends only **its own** stale rows. The unique index on online hostnames (and a new one on online TCP ports) makes bindings exclusive across nodes: a bind that hits another node's tunnel for the same account asks that node to take over (the owner closes its session if it no longer answers pings, like the single-node reconnect case); a live tunnel of another account → "in use". Tunnels of dead nodes are ended by any node that finds them.
* Certificates: certmagic storage in Postgres (`certmagic_data`, `certmagic_locks`) so the wildcard, on-demand certificates and HTTP-01/TLS-ALPN challenges are shared by all nodes.
* `tund_tunnels` notifications gain `node`, `proto`, `remote_port`; nodes keep a short-lived cache hostname/port → node.

### Relay protocol

TLS on `TUND_RELAY_ADDR` with a per-node self-signed certificate whose SHA-256 is published in `nodes.relay_cert_sha256` (callers pin it). First frame (uint16 length + JSON): `{"kind","ts","nonce","mac", …}` with `mac = HMAC-SHA256(TUND_SECRET, "relay\x00" + kind + ts + nonce + target)`, ts within ±60 s, nonces not reused.

* `http` `{"target": "<host>", "remote": "ip:port", "replay_of"?: id}` — then the connection carries HTTP/1.1; the receiving node serves it exactly like a visitor request for `<host>` with the given client address (policies, warning page, capture, identity headers). `replay_of` marks internal replays (no policy/warning, recorded as replay).
* `raw` `{"proto": "tcp"|"tls", "target": "<port>"|"<host>", "remote"}` — then raw bytes (a peeked TLS ClientHello included) piped into the local tunnel.
* `cmd` `{"cmd": "stop"|"takeover"|"ping", "tunnel_id", "user_id", "reason"}` → JSON reply `{"ok","error"}`.

### Routing rules

* HTTP/HTTPS: the node terminating TLS for a visitor serves local tunnels itself; otherwise it relays (`http`) to the owning node. The dashboard host on an `edge` node is relayed to a live `control` node (`http` with target = dashboard host). `/_tund/oidc/*` is relayed to the node owning the tunnel named in the signed state.
* TCP: every node listens on the ports of all online TCP tunnels (learned via notifications) and relays connections to the owner.
* TLS passthrough: the SNI router relays connections for non-local `tls` tunnels.
* Internal API (dashboard → control): replay and stop go to the owning node via relay; `GET /internal/status` adds `nodes: [{name, role, region, alive, tunnels, version, last_seen}]`.
* The public API's `GET /tunnels` reads online tunnels from the database (all nodes).

## Plans and billing (migration 0021)

**Plans.** Free accounts get the instance limits (Admin → Settings → Limits). Pro accounts get the `pro_limit_*` settings (defaults: 10 online tunnels, 10 static addresses, 5 custom domains, 10 teams, no speed cap, 10× the Free transfer, unlimited lifetime), never less than Free (`0` = unlimited wins; Free's custom domain count only counts when Free has custom domains), plus custom domains and TCP/TLS tunnels. Per-account overrides (`users.bandwidth_kbps`, `custom_domains`, …) still win, and admins stay exempt as before. The edge (`plan.go`) and the dashboard (`lib/plans.ts`) both ask Postgres:

* `user_is_pro(uid)`: `users.pro_granted` (admin: "Pro without paying"), an active `pro` subscription, being the payer and owner of a team with an active `team` subscription, an owner of a team granted `team`, or a member of a team whose plan is `team_pro`.
* `team_plan(tid)`: `teams.plan_granted` (admin: billing exempt), else the best active team subscription (`team_pro` before `team`); null = no plan.
* `subscription_active(status)`: `active`, `trialing`, `past_due` (Stripe still retries).
* `user_is_paying(uid)`: an active subscription in the account's own name. **Paying accounts count as trusted** (like `users.trusted`): checkout requires a billing address, so they are identified; the browser warning, custom domain review and untrusted-account rules don't apply to them. Granted Pro and Team Pro members who don't pay stay untrusted unless an admin trusts them.

**Teams.** With billing on, a team without a plan is just its owner (no members, no team custom domains). Team and Team Pro include `team_seats` members (5) plus `team_seat_pack` (5) per extra seat pack, and `team_custom_domains` (1) custom domains that every member can bind, also on Free (the edge skips the account's custom-domain check for a team domain of a team with a plan). `teams.seats_override` (admin) replaces the seat count. Seats are checked when adding members, creating invites and accepting them (under the team row lock). With billing off nothing about teams changes.

**Stripe.** Admin → Billing stores the secret key and webhook secret encrypted (key from `TUND_INTERNAL_SECRET`), the currency, Stripe Tax and the prices (defaults: Pro $5/$50, Team $10/$100, Team seats $10/$100 per pack, Team Pro $25/$250, Team Pro seats $25/$250 per pack, monthly/yearly). "Set up Stripe" creates the products (metadata `tund_product`) and prices (lookup keys `tund_<plan>_<interval>`, `tund_<plan>_seats_<interval>`; a changed amount creates a new price that takes over the lookup key and archives the old one) and a webhook endpoint `https://<dashboard>/api/billing/webhook` for `checkout.session.completed` and `customer.subscription.*`, saving its signing secret. Billing is on when enabled and a key is set; then the sidebar shows Billing/Upgrade and team pages show the plan panel.

* Users subscribe on `/billing` (Stripe Checkout, `mode: subscription`, billing address required, promotion codes allowed); team owners on the team page (Team or Team Pro, monthly or yearly, extra seat packs). Subscription metadata `tund_plan`, `tund_user`, `tund_team` link it back. The Stripe customer is created once per account (`users.stripe_customer_id`). "Manage subscription" opens the Stripe customer portal (payment methods, invoices, cancelling; configure it in Stripe). The paying owner changes seat packs on the team page (`proration_behavior: always_invoice`); fewer seats than members are refused.
* Webhooks verify the signature and upsert `subscriptions` (status, plan, interval, seat packs from the seat item's quantity, period end, cancel at period end), then `NOTIFY tund_config {"kind":"plans"}`: every edge refreshes meters, browser warnings (trust) and re-checks custom-domain and TCP/TLS tunnels. Trust for new binds is read when a client connects.
* Admin → Billing shows active subscriptions and an MRR estimate; Admin → Users → user has "Pro without paying"; Admin → Teams → team sets a plan without billing and a seat override.

## Bandwidth limits (migration 0008)

Per **account** (the tunnel owner), across all of its tunnels:

* **Throughput cap** — `limit_bandwidth_kbps` setting (env `TUND_BANDWIDTH_KBPS`, default 0 = unlimited), in kilobits per second **per direction** (visitor→local and local→visitor each). Enforced with token buckets per account on each node (burst 256 KiB).
* **Monthly transfer quota** — `limit_transfer_gb` setting (env `TUND_TRANSFER_GB`, default 0 = unlimited), in GB (10^9 bytes) per calendar month (UTC), counting both directions.
* **Tunnel lifetime** (migration 0017) — `limit_tunnel_lifetime` setting (env `TUND_MAX_TUNNEL_LIFETIME`, default 0 = unlimited), in minutes; per-user override `users.tunnel_lifetime_minutes` (NULL = the setting, 0 = unlimited); admins are exempt unless overridden. Every node checks its own tunnels every 15 s and closes those older than the lifetime (control `closed` with "this tunnel reached the maximum lifetime of 2h on this server; start it again"); changed settings apply to running tunnels on the next check. The `bound` reply carries `expires_at`; clients older than 0.5.0 (which ignore it) get the limit in `warning` instead ("this server closes tunnels after 2h"). Five minutes before the end the server sends `{"type":"notice","id":"<bind id>","code":"lifetime","expires_at","error":"<url> closes in 5m (maximum tunnel lifetime of 2h)"}`, and closes with `code: "lifetime"`. The CLI shows "closes at 18:30 (in 2h)" under each tunnel; `--restart-on-expiry` (config `restart_on_expiry: true`) binds the tunnel again right after the close, so it keeps its hostname with a short interruption. `tund status` and `GET /tunnels` (`expires_at`, null when unlimited) show the closing time. The dashboard shows "closes in …" on the user's own online tunnels.
* Per-user overrides: `users.bandwidth_kbps`, `users.transfer_quota_gb` (NULL = default, 0 = unlimited). Admins are unlimited unless an override is set.

Metering wraps the tunnel data streams, so it counts everything that passes through a tunnel (HTTP headers + bodies, WebSockets, SSE, TCP, TLS). Nodes aggregate in memory and upsert `usage_daily` every 15 s; request/connection counts are added too. Each node re-reads the month's usage per active account every 60 s (multi-node accuracy is eventually consistent).

When the quota is exhausted: HTTP visitors get **509 Bandwidth Limit Exceeded** (edge page, plain text for non-browsers), TCP/TLS connections are closed, the client receives a control `error` message once ("monthly transfer quota of N GB used up; resets on <date>"), and new binds get bind_error with the same text. Raising the limit (settings or user override → `NOTIFY tund_config {"kind":"settings"}` / `{"kind":"user_updated","id"}`) lifts the block immediately.

API `GET /me` adds `"usage": {"month_bytes_in","month_bytes_out","month_requests","month_connections","period_start","period_end"}` and `limits` gains `"bandwidth_kbps"`, `"transfer_gb"`, `"tunnel_lifetime_minutes"` (effective; 0 = unlimited). Dashboard: users see this month's usage vs quota (overview + settings), a daily usage chart; admins see usage per account and can set overrides.

## Abuse protection (migration 0009)

### DNS (hosted instance)

`tund.io` has CAA records allowing only the instance's own Let's Encrypt accounts (production + staging `accounturi`s, `issue` and `issuewild`). Without them a TLS-passthrough tunnel could answer an HTTP-01 challenge for its own hostname and obtain a trusted certificate for `x.tund.io`, bypassing the warning page. **If the certificate storage (and with it the ACME account) is ever recreated, the CAA records must be updated**, and edge nodes must share the certificate storage (they do: Postgres).

### Settings (Admin → Settings → Abuse protection)

| key | default (env) | |
| --- | --- | --- |
| `untrusted_custom_domains` | `"review"` (`TUND_UNTRUSTED_CUSTOM_DOMAINS`: allow/review/deny) | custom domains of non-trusted accounts: `allow`, `review` (admin must approve each domain — `domains.approval`), `deny` (legacy booleans: true = allow, false = deny) |
| `warn_custom_domains` | true | the browser warning page also appears on custom domains of non-trusted accounts |
| `custom_domain_min_age_days` | 30 | review hint: domains registered more recently are marked high risk |
| `untrusted_tcp` | true (`TUND_UNTRUSTED_TCP`) | … TCP tunnels |
| `untrusted_tls` | true (`TUND_UNTRUSTED_TLS`) | … TLS passthrough tunnels |
| `blocked_hostname_words` | built-in list (brands, "login", "signin", "verify", "wallet", "password", …) | a label containing one of the words (case-insensitive, dashes ignored) cannot be requested/pinned by non-trusted accounts |
| `safe_browsing_api_key` | "" (`TUND_SAFE_BROWSING_API_KEY`) | Google Safe Browsing v4 key; online tunnel URLs are checked every 10 min |
| `phishing_heuristics` | true | analyse captured HTML of non-trusted accounts' tunnels |
| `phishing_auto_block` | false | heuristic hits also block the hostname (otherwise only report + flag) |
| `turnstile_site_key` / `turnstile_secret_key` | "" | Cloudflare Turnstile on sign-up, password reset and the abuse form (dashboard) |
| `block_disposable_emails` | true | reject sign-ups from disposable email domains (dashboard, built-in list) |
| `signup_rate_limit` | 5 | sign-ups per IP per hour (dashboard) |

"Trusted" = `users.trusted` or admin. Server refusals are bind_errors with a clear reason ("TCP tunnels need a trusted account on this server; ask the administrator").

### Blocked hostnames

`blocked_hosts` (exact hostnames). The edge refuses binds on them and answers requests with **451 "This site has been blocked"** (TCP/TLS connections are closed); blocking an online hostname stops its tunnel. The dashboard sends `NOTIFY tund_config {"kind":"blocked_hosts"}` after changes.

### Automatic detection (edge)

* **Safe Browsing**: every 10 min the edge sends the public URLs of all online HTTP/TLS tunnels to `threatMatches:find` (MALWARE, SOCIAL_ENGINEERING, UNWANTED_SOFTWARE, POTENTIALLY_HARMFUL_APPLICATION). A match → hostname blocked, tunnel stopped, account flagged (`users.flagged_at`, `flag_reason`), report with source `safe_browsing`. (Google's Lookup API is for non-commercial use; commercial operators should use the Web Risk API.)
* **Phishing heuristics**: for 200 `text/html` responses of non-trusted accounts the edge scores the captured body: password field, brand names in title/headings, phrases like "verify your account", "seed phrase", "credit card", "cvv", forms posting to other domains. Score ≥ threshold → report with source `heuristic` (details: signals, score, request id) and flagged account, at most once per hostname per 24 h; with `phishing_auto_block` also blocked + stopped.
* Every new report → `NOTIFY tund_abuse {"id","hostname","source"}`; the dashboard emails admins (debounced) and shows the queue.

### Dashboard

Public `/report?host=` form (category, URL, description, optional email; Turnstile when configured; rate limited) linked from the warning page, landing footer and blocked page. `/admin/abuse`: queue with filters; actions stop tunnel, block hostname, disable account, trust/untrust, flag/unflag, resolve/dismiss with a note (all audited). `/terms` and `/acceptable-use` pages (generic defaults with `instance_name`, editable in settings as markdown), accepted via a checkbox at sign-up. Custom-domain creation respects `custom_domains` (and the per-account override) first, then `untrusted_custom_domains`.

### Custom domain review

When a non-trusted account adds a custom domain under the `review` policy, the dashboard stores it with `approval = 'pending'` and computes risk signals into `domains.risk`: `registered_at` / age in days via RDAP (`https://rdap.org/domain/<registrable domain>`, events `registration`), `new_domain` (younger than `custom_domain_min_age_days`), `deceptive_words` (the blocked word list applied to every label), `idn` (any `xn--` label; show the Unicode form), `safe_browsing` (lookup of `https://<domain>/` when a key is set). It files an abuse report with source `admin` and category `other` titled "custom domain awaiting review" so it appears in the admin queue (and triggers the admin email). Admins approve/reject in `/admin/domains` (or from the report); the user sees "pending review" / "rejected" with the reason. The edge only binds custom domains with `approval = 'approved'` (bind_error "… is waiting for approval by the administrator" / "… was rejected"), obtains no certificates for pending/rejected or blocked domains, and — with `warn_custom_domains` — shows the browser warning on custom domains of non-trusted accounts too.

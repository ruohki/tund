# tund

Self-hosted tunnels: expose a local port on a public HTTPS URL, inspect every request in a dashboard, and optionally put a password or single sign-on (OIDC) in front of it.

```sh
curl -fsSL https://tund.io/install.sh | sh     # Windows: irm https://tund.io/install.ps1 | iex
tund http 3000                                         # first run opens the browser to log in
```

```
tund                                                    (Ctrl+C to quit)
Session     online
Account     you@example.com
Inspector   https://tund.io/inspect?host=brave-otter-4821.tund.io
Forwarding  https://brave-otter-4821.tund.io → http://localhost:3000
```

tund works in two ways from the same code:

* **Cloud.** The `tund` binary talks to the hosted instance at `https://tund.io` out of the box. Sign up in the browser the first time; nothing else to configure.
* **Self-hosted.** Run the server on your own box and domain (see [Deploy](#deploy)), then log into it once with `tund login https://tund.example.com`. The CLI saves that server with the token and uses it from then on. `tund logout` switches back to the cloud.

* **`tund` client**: a single static binary for Linux, macOS and Windows (amd64/arm64). It opens one outbound TLS WebSocket to the server and multiplexes streams over it (yamux). It needs no TUN/TAP device, no root and no inbound ports.
* **`tund-server`**: the edge. It terminates TLS with automatic Let's Encrypt certificates, routes `*.your-domain` and verified custom domains to connected clients, records requests and responses, and enforces access policies. It also serves the dashboard, installers and client downloads.
* **dashboard** (Next.js): accounts, auth tokens, reserved subdomains, custom domains with DNS verification, OIDC providers, access policies, a live request inspector with replay, and traffic metrics.

```
browser ──https──▶ tund-server :443 ──stream over wss──▶ tund client ──▶ localhost:3000
                   ├── dashboard.<base> ──▶ dashboard (Next.js, internal)
                   └── Postgres (captured traffic, config; LISTEN/NOTIFY for live updates)
```

## Deploy

Requirements: a server with Docker, ports 80 and 443 free, and DNS.

1. **DNS.** Create a wildcard record for the base domain, e.g. `*.tund.example.com  A  <server ip>`. The dashboard lives at `dashboard.tund.example.com` (covered by the wildcard); to put it on the bare domain instead, also add `tund.example.com  A  <server ip>` and set `TUND_DASHBOARD_HOST=tund.example.com`.
2. **Configure.** Run `cp .env.example .env`, then set `TUND_BASE_DOMAIN`, `TUND_SERVER_IP` and three secrets (`openssl rand -hex 32`).
3. **Start.** Run `docker compose pull && docker compose up -d`. This uses the published images `ghcr.io/ruohki/tund-server` and `ghcr.io/ruohki/tund-dashboard`; use `docker compose up -d --build` to build from a checkout. Server and dashboard run with host networking (the edge needs 80, 443 and the TCP port range). All internal ports bind to 127.0.0.1: dashboard 3000, internal API 4040, Postgres 55432.
4. Open `https://dashboard.<base>` and create the first account; it becomes the admin. Further sign-ups are disabled unless `TUND_ALLOW_SIGNUP=true`; admins can create users under *Admin*.
5. Users run `tund login https://dashboard.<base>` once, or install via `https://dashboard.<base>/install.sh`, which points the CLI at your server.

### Running it as a public service

Set `TUND_ALLOW_SIGNUP=true`. Signed-out visitors then get a landing page and can create accounts themselves, and the CLI's browser login leads new users through sign-up. Limit what each account can use with `TUND_MAX_TUNNELS_PER_USER`, `TUND_MAX_PINNED_PER_USER` (static hostnames) and `TUND_MAX_DOMAINS_PER_USER` (custom domains); 0 = unlimited and admins are exempt. Admins can disable abusive accounts under *Users*, which immediately disconnects their tunnels. **Bandwidth**: cap throughput per account (`limit_bandwidth_kbps`, per direction) and set a monthly transfer quota (`limit_transfer_gb`). When the quota is used up, visitors get HTTP 509 and the CLI is told why. Both have instance defaults in *Admin → Settings* and per-user overrides; env defaults are `TUND_BANDWIDTH_KBPS` and `TUND_TRANSFER_GB`. **Verified emails**: once SMTP is set up under *Admin → Email*, *Require email verification* keeps unverified accounts from opening tunnels. Most instance settings live under *Admin → Settings* and apply without a restart; the `TUND_*` variables only provide defaults. Turn on `TUND_BROWSER_WARNING=true` to show an ngrok-style warning page to browser visitors on your domain. It appears once per hostname per browser, never interrupts API or webhook traffic or requests carrying `Tund-Skip-Browser-Warning: 1`, and is skipped for custom domains, password/OIDC-protected tunnels and accounts an admin marks as trusted. `TUND_ABUSE_CONTACT` adds a "Report abuse" link to it. The public instance at tund.io runs exactly this setup; it is also the default server compiled into the CLI (`DEFAULT_SERVER` build arg).

### TLS certificates

| setup | how |
| --- | --- |
| default | Certificates are issued on demand per hostname via HTTP-01/TLS-ALPN-01. Only hostnames with a live tunnel, a reservation or a verified custom domain qualify, so scanners can't burn your rate limit. The client re-requests its last random subdomain so certificates get reused. |
| wildcard via DNS-01 (recommended) | Set `TUND_DNS_PROVIDER=hetzner` (Hetzner Cloud DNS API token), `hetzner-legacy` (dns.hetzner.com token) or `cloudflare`, plus `TUND_DNS_API_TOKEN`. One `*.base` certificate then covers every tunnel. |
| own certificate | Set `TUND_TLS_MODE=manual` with `TUND_TLS_CERT_FILE` / `TUND_TLS_KEY_FILE` (mount them into the `server` container). |
| behind another proxy | Set `TUND_TLS_MODE=off`. tund-server then speaks plain HTTP; set `TUND_PUBLIC_SCHEME=https` if the proxy terminates TLS. |

Custom domains always use on-demand certificates. Use `TUND_ACME_CA=https://acme-staging-v02.api.letsencrypt.org/directory` while experimenting.

## Client

```sh
# macOS / Linux
curl -fsSL https://tund.io/install.sh | sh
# Windows (PowerShell)
irm https://tund.io/install.ps1 | iex

tund http 3000                              # cloud; logs you in on first run
tund login https://tund.example.com         # or: use your self-hosted instance from now on
tund logout                                 # forget token + server (back to the cloud)
tund update                                 # install the latest release
```

When a newer release is out, `tund http` shows an *Update* line (older clients print a warning with the installer command). `tund update` downloads the new binary through your server, verifies its checksum and replaces itself; use `sudo tund update` if it lives in a root-owned directory like `/usr/local/bin`.

`tund login` opens the dashboard in your browser: click *Approve* and the browser hands the login straight back to the CLI (a one-time code on a `127.0.0.1` callback), with nothing to copy. Over SSH, without a display or with `--no-browser`, it shows a short code to approve on any device instead. Either way the CLI generates its token locally and only the token's hash is ever sent to the server. For CI and headless machines, create a token under *Auth tokens* and use `tund config add-authtoken <token>` or `TUND_AUTHTOKEN`. The installer served by a self-hosted instance points the CLI at that instance automatically.

| command | |
| --- | --- |
| `tund http 3000` | your account's **static hostname** → `http://localhost:3000`; the same URL every run (created on first use) |
| `tund http 8080 --subdomain myapp --pin` | claim `https://myapp.<base>` and keep it as another static hostname |
| `tund http 3000 --random` | a throwaway URL instead of your static hostname |
| `tund http https://localhost:8443` | local HTTPS target (certificate not verified) |
| `tund http 3000 --domain api.example.com` | custom domain (add and verify it in the dashboard first) |
| `tund http 3000 --host-header rewrite` | send `Host: localhost:3000` instead of the public host |
| `tund http 3000 --password s3cret` | visitors must enter a password; scripts can use `curl -u :s3cret` |
| `tund http 3000 --oidc google --oidc-allow @company.com` | visitors sign in with an OIDC provider configured in the dashboard |
| `tund start --all` | start every tunnel defined in `tund.yml` |

`tund config path` shows where the config file lives (`~/.config/tund/tund.yml`, `%AppData%\tund\tund.yml` on Windows):

```yaml
server: https://tund.example.com    # omit to use the cloud
authtoken: tund_…
tunnels:
  web:
    addr: 3000
    subdomain: myapp
  api:
    addr: 8080
    domain: api.example.com
    host_header: rewrite
    auth:
      oidc: google
      allow: ["@company.com"]
```

## TCP and TLS tunnels

```sh
tund tcp 22                                    # tcp://tund.io:20417 → localhost:22 (SSH, databases, game servers …)
tund tcp 5432 --remote-port 20432 --pin        # a fixed port, reserved for your account
tund tls 8443 --subdomain secure               # tls://secure.tund.io, TLS passthrough: the edge never decrypts
tund tls 3000 --domain app.example.com --terminate-cert cert.pem --terminate-key key.pem   # TLS ends on your machine
tund http 3000 --allow-ip 203.0.113.0/24       # IP allow lists work for every tunnel type
```

TCP tunnels get a port from the server's `TUND_TCP_PORTS` range (open it in your firewall). The client remembers its port, `--remote-port` asks for a specific one, and `--pin` reserves it. Pinned ports and static hostnames together count as "static addresses".

TLS tunnels route by SNI and pass the encrypted stream through untouched, so visitors see the certificate of your own service. That's end-to-end encryption, typically with a custom domain and your own certificate. Alternatively the client terminates TLS with `--terminate-cert/--terminate-key` and forwards plaintext locally.

TCP and TLS have no HTTP layer, so instead of requests the inspector records **connections**: client IP, bytes in/out, duration and errors. The API exposes them at `GET /_tund/api/v1/connections`.

## AI agents (MCP)

`tund mcp` is a Model Context Protocol server built into the CLI. It lets coding agents expose what they are building and debug the traffic that reaches it, for example webhooks, OAuth callbacks or a teammate clicking around.

```sh
claude mcp add tund -- tund mcp                      # Claude Code
```
```json
{ "mcpServers": { "tund": { "command": "tund", "args": ["mcp"] } } }
```
(Cursor, Windsurf, VS Code and other clients use this shape in their MCP config.)

| tool | |
| --- | --- |
| `start_tunnel` | expose a port/URL and return the public URL (supports subdomain, pin, random, password, host header) |
| `list_tunnels`, `stop_tunnel` | manage tunnels started in the session (or all of the account's) |
| `list_requests`, `get_request` | captured traffic with headers and decoded bodies |
| `replay_request` | re-send a captured request, optionally edited |
| `whoami`, `login` | account and limits; browser login if the CLI isn't logged in yet |

Tunnels started by an agent live as long as its `tund mcp` process. `--allow-ports 3000,5173` restricts which local ports an agent may expose, and `--require-password` puts a random password on every tunnel it opens.

## HTTP API

Auth tokens also authenticate a small JSON API on the dashboard host, `https://tund.io/_tund/api/v1` (Bearer token). It covers `GET /me`, `GET /tunnels`, `POST /tunnels/{id}/stop`, `GET /requests` (filters: hostname, tunnel_id, method, status, path, limit, before), `GET /requests/{id}` (headers and decoded bodies) and `POST /requests/{id}/replay`. See `docs/SPEC.md` for the fields.

```sh
curl -H "Authorization: Bearer $TUND_AUTHTOKEN" "https://tund.io/_tund/api/v1/requests?status=5xx&limit=10"
```

## Protecting tunnels

Policies can be set per run with client flags, or stored on a reserved subdomain / custom domain in the dashboard. Stored policies apply to every tunnel that uses the hostname unless the client passes its own policy.

* **Password.** Visitors get a login page, and API clients can use HTTP basic auth with any username. Changing the password signs everyone out.
* **OIDC.** Add a provider under *Access* (issuer URL, client ID/secret). Register **one** redirect URI at the provider: `https://dashboard.<base>/_tund/oidc/callback`. It works for every tunnel and custom domain because the login is handed back to the tunnel host with a short-lived signed token. Allow lists take exact emails (`alice@example.com`) or domains (`@example.com`); an empty list admits anyone who can sign in.

### Identity headers

When a visitor passes a tunnel's password or OIDC check, the edge tells your app who it is:

| header | |
| --- | --- |
| `X-Tund-Auth` | `oidc` or `password` |
| `X-Tund-User-Email`, `X-Tund-User-Email-Verified` | email claim |
| `X-Tund-User-Name`, `X-Tund-User-Username` | `name`, `preferred_username` |
| `X-Tund-User-Id` | the OIDC `sub` |
| `X-Tund-User-Groups` | `groups` claim, comma-separated |
| `X-Tund-Idp` | issuer URL |

The edge **always removes `X-Tund-*` headers sent by visitors** before adding its own, on every tunnel, so an app can rely on them for requests that arrive through tund. Allow lists also accept `group:<name>` (e.g. `--oidc-allow group:engineering`); ask your IdP for the `profile` and `groups` scopes.

## Teams

Teams share OIDC providers and domains between accounts. Create one under *Teams*, then invite people: existing users by email, everyone else with an invite link.

* **Team OIDC providers**: configured once by a team admin and usable by every member, e.g. `tund http 3000 --oidc acme/google` (or just `--oidc google` when that's unambiguous).
* **Team domains**: static hostnames and custom domains (like `*.dev.company.com`) owned by the team. Every member can run tunnels on them, and removing a member immediately closes their tunnels on team domains.
* **Shared traffic**: members see captured requests on the team's hostnames in the inspector and the API, and can replay them.
* **Roles**: owners manage everything, admins manage members, invites, providers and domains, and members use them. Per-account limits (`TUND_MAX_PINNED_PER_USER`, `TUND_MAX_DOMAINS_PER_USER`) also apply per team; `TUND_MAX_TEAMS_PER_USER` caps how many teams a user can own.

## Static hostnames

A static hostname is a name on the hosted domain (`myapp.tund.io`) pinned to your account, so nobody else can take it and your URL never changes. Every account gets one automatically on its first `tund http`. Claim more with `--pin` or in the dashboard under *Domains* (pick a name or a random one), mark one as the default, or pin the hostname of a running tunnel from *Tunnels*. Each static hostname can carry its own access policy (password or OIDC). Set `TUND_AUTO_PIN=false` to turn off the automatic first hostname.

## Custom domains

Custom domains are off by default. Turn them on for everyone under *Admin → Settings* (*Custom domains*, env default `TUND_CUSTOM_DOMAINS`), or per account under *Admin → Users* (*Default* / *On* / *Off*; the per-account choice wins, and admins have them unless set to *Off*). Turning them off disconnects the affected custom-domain tunnels right away.

Add the domain in the dashboard. Create the TXT record it shows (`_tund-challenge.<domain>`) and point the domain at the server with an A record to the server IP or a CNAME to the dashboard host. Then click *Verify*. Wildcards like `*.dev.example.com` let the client use any `<name>.dev.example.com`.

## Edge nodes (global)

Run extra `tund-server` nodes in other regions so clients and visitors connect to a server near them, while one **control** node keeps the dashboard and database.

* **Routing**: point the service domain (`tund.io` and `*.tund.io`) at all nodes with GeoDNS or anycast (e.g. Bunny Magic Containers anycast endpoints). A client connects to its nearest node; a visitor lands on theirs. If the tunnel lives on another node, the visitor's node relays the traffic there over an authenticated TLS link. That link is pinned to a per-node certificate published in the database, and every frame is HMAC-signed with `TUND_SECRET`. This covers HTTP, WebSockets, TLS passthrough and TCP.
* **Shared state**: every node uses the control node's Postgres, including the certificate store (`TUND_CERT_STORAGE=postgres`). The wildcard, custom-domain certificates, ACME challenges and the ACME account (important for CAA) are shared. On first start with Postgres storage, a node imports the existing file-based certificates.
* **Resilience**: a node that stops heartbeating for 45 s is considered dead and its tunnels are ended, so clients can reconnect elsewhere. A reconnecting client takes its hostnames over from its old, dead session on another node. Admins see nodes, regions and tunnel counts in the dashboard.

Setup:

1. On the control node, set `TUND_NODE_NAME`, `TUND_RELAY_URL` (the `host:port` other nodes use to reach its relay listener `TUND_RELAY_ADDR`, default `:4443`) and `TUND_CERT_STORAGE=postgres`, then restart. Make Postgres reachable from the edge nodes over a private network (WireGuard recommended) or TLS with a strict `pg_hba`.
2. On each edge node, use `docker-compose.edge.yml` with the same domain, secrets and DNS-provider settings, its own `TUND_NODE_NAME`/`TUND_NODE_REGION`/`TUND_RELAY_URL`, and `TUND_DATABASE_URL` pointing at the control database.
3. Add the node's IP to GeoDNS or anycast for `tund.io` and `*.tund.io`, and open the relay port between nodes only.

## Abuse protection

A public tunnel service will be used for phishing. tund layers several defences:

* **Accounts**: sign-up modes (open, invite-only, closed), email verification, captcha (Cloudflare Turnstile), per-IP rate limits, disposable-email blocking, and Terms and Acceptable Use acceptance.
* **Browser warning page**: an ngrok-style interstitial for non-trusted accounts, optionally on custom domains too.
* **Trust gating**: custom domains (`allow` / `review` / `deny`), TCP and TLS passthrough can be limited to accounts an admin trusts. Under `review`, each new custom domain waits for admin approval, which shows risk signals: domain age from RDAP, deceptive words, punycode lookalikes and Safe Browsing status.
* **Deceptive hostnames**: non-trusted accounts can't choose names containing brands or words like `login` and `verify` (the list is configurable).
* **Detection**: Google Safe Browsing checks of every online tunnel (with an API key), and phishing heuristics on captured HTML (password fields, brands, "verify your account", foreign form targets). Hits are reported, the account is flagged, and the hostname can be blocked automatically.
* **Takedown**: a public abuse report form, an admin queue with one-click actions (stop, block hostname, disable account), email alerts to admins, and 451 pages for blocked hostnames.
* **Certificates**: CAA records on the service domain allow only the instance's own ACME account. Without them, a TLS-passthrough tunnel could obtain its own certificate for `x.<base>` and bypass the warning page. After recreating the certificate storage, update the `accounturi` values.

## Releases

Tagging `v*` makes GitHub Actions build the client for Linux, macOS and Windows (amd64 and arm64) into a GitHub Release with `checksums.txt` and `version.txt`, and multi-arch images to GHCR (`:<version>`, `:latest`). Pushes to `main` publish `:main` images. The install scripts and `/_tund/downloads/…` fetch binaries from the latest release (`TUND_DOWNLOAD_BASE_URL`) and verify their checksums.

## Development

```sh
docker run -d --name tund-dev-pg -e POSTGRES_USER=tund -e POSTGRES_PASSWORD=tund -e POSTGRES_DB=tund -p 55432:5432 postgres:17-alpine

# edge (plain HTTP, *.localtest.me resolves to 127.0.0.1)
TUND_BASE_DOMAIN=localtest.me TUND_TLS_MODE=off TUND_HTTP_ADDR=:28480 TUND_PUBLIC_PORT=28480 \
TUND_INTERNAL_ADDR=127.0.0.1:28440 TUND_DASHBOARD_UPSTREAM=http://127.0.0.1:3100 \
TUND_DATABASE_URL=postgres://tund:tund@localhost:55432/tund \
TUND_SECRET=dev-secret-dev-secret TUND_INTERNAL_SECRET=dev-internal-secret \
go run ./cmd/tund-server

# dashboard (see dashboard/.env.example)
cd dashboard && pnpm install && PORT=3100 pnpm dev

# client
go run ./cmd/tund config set-server http://dashboard.localtest.me:28480
go run ./cmd/tund http 3000
```

`docs/SPEC.md` describes the wire protocol, database contract and internal API.

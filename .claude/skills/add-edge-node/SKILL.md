---
name: add-edge-node
description: Turn a fresh server into a tund edge node for the tund.io cluster (control-node prerequisites, Docker, .env from the control node, start, verify, DNS). Use when the user points at a new server to add, or asks to remove a node.
argument-hint: <server-ip> [node-name]
disable-model-invocation: true
---

# Add an edge node

Target: `$ARGUMENTS` (the server's public IP or hostname, then an optional node name).

## Cluster facts

- Control node **fsn-1**: `ssh root@tund.io` (public 188.245.5.5). Compose project in `/opt/tund` (`docker-compose.yml`, `.env`); containers `tund-server-1`, `tund-dashboard-1`, `tund-postgres-1`. Postgres is the only database for the whole cluster.
- Edge nodes run this repo's `docker-compose.edge.yml`, installed as `/opt/tund/docker-compose.yml` with its own `/opt/tund/.env`.
- All nodes sit on one Hetzner Cloud Network (private `10.x`). Postgres (`55432`) and the node-to-node relay (`4443`) are reached only over it. Public ports on every node: 22, 80, 443 and the `TUND_TCP_PORTS` range.
- Every node registers in the `nodes` table (`name, role, region, relay_url, relay_cert_sha256, version, started_at, last_seen`); a node is alive while `last_seen` is within 45 s.
- DNS: `@` and `*` of the base domain carry one A record per node (round robin). Every node serves every tunnel and relays to the owner, so any node is a valid answer.
- Each node writes host metrics (CPU, memory, network, tunnels, sessions) and its `public_ip` into `nodes` with every heartbeat (tund 0015+). Admins see them under *Admin → Nodes*.
- With Bunny DNS, **tund-balancer** (a separate tool, runs next to fsn-1) reads that table, health-checks each node over its public IP, and creates, disables and re-weights the A records. Check whether it runs: `ssh root@tund.io 'docker ps --format "{{.Names}}" | grep -i balancer'`.

## Rules

- **Never print secrets.** Read the control `.env` only through commands that print key names, or that pipe values straight into a file on a server. Never `cat` or `grep` it to the terminal. The same applies to the new node's `.env`.
- **Ask before** changing the control node (recreating its containers drops every tunnel for a few seconds), before changing DNS, and before touching anything already running on the new server.
- Verify each step before the next. On a failure, stop and show the exact output; don't improvise around it.
- Everything is idempotent: check first, change only what's missing, so the skill can be rerun after a partial run.
- Use `ssh -o BatchMode=yes`. fsn-1 has `python3`, `curl` and `dig`, but no `jq`.

## 1. Inputs

- `NODE` = first argument. `NAME` = second argument, or propose the next free `fsn-N` (look at the `nodes` table) and confirm it with the user. `REGION` = `eu-central` unless the user says otherwise.
- Check access: `ssh -o BatchMode=yes root@$NODE true`. If it fails, ask the user to add their SSH key to the server.

## 2. Inspect the new server (one SSH call)

```sh
ssh root@$NODE '. /etc/os-release; echo "$PRETTY_NAME $(uname -m)"; nproc; free -m | sed -n 2p; df -h / | tail -1
  ip -4 -br addr; ss -ltnp | grep -E ":(80|443|4443|2[0-9]{4})\b"; docker version --format "{{.Server.Version}}" 2>&1
  ufw status 2>/dev/null | head -1; ls -a /opt/tund 2>&1'
```

- `NODE_PRIV` = its `10.x` address. If there is none, stop: the user must attach the server to the same Hetzner Cloud Network as fsn-1.
- If something already listens on 80/443/4443 or `/opt/tund` exists, stop and ask.

## 3. Control-node prerequisites (one-time)

Check:

```sh
ssh root@tund.io 'ip -4 -br addr | grep " 10\."; sysctl -n net.ipv4.ip_nonlocal_bind
  cd /opt/tund; grep -E "^(TUND_RELAY_ADDR|TUND_RELAY_URL|TUND_CERT_STORAGE|TUND_NODE_NAME|TUND_IMAGE_TAG)=" .env
  cat docker-compose.override.yml 2>/dev/null
  docker exec tund-postgres-1 psql -U tund -d tund -Atc "show max_connections"'
```

`CTRL_PRIV` = fsn-1's `10.x` address. If fsn-1 has none, stop: the user attaches it to the network in the Hetzner console.

Then list what's missing, ask once, and apply:

1. `/etc/sysctl.d/90-tund.conf` containing `net.ipv4.ip_nonlocal_bind = 1`, then `sysctl --system`. This lets Docker and tund-server bind the private IP even when the interface comes up after Docker at boot.
2. In `/opt/tund/.env` (back it up first as `.env.bak-$(date +%F)`; edit in place with `sed`, append missing keys):
   ```
   TUND_RELAY_ADDR=CTRL_PRIV:4443
   TUND_RELAY_URL=CTRL_PRIV:4443
   TUND_CERT_STORAGE=postgres
   TUND_NODE_NAME=fsn-1
   TUND_PUBLIC_IP=188.245.5.5
   ```
   `TUND_CERT_STORAGE=postgres` is required with a relay; the first start imports the file certificates into Postgres.
3. `/opt/tund/docker-compose.override.yml` (Compose merges it automatically; `ports` are appended, so the `127.0.0.1` binding stays):
   ```yaml
   services:
     postgres:
       command: postgres -c max_connections=200
       ports:
         - "CTRL_PRIV:55432:5432"
   ```
   Each node uses up to 20 connections and the dashboard 10; raise `max_connections` further before it gets close.
4. Apply: `cd /opt/tund && docker compose up -d`. This recreates `postgres` and `server`: tunnels drop for a few seconds and clients reconnect.
5. Verify: `docker compose ps` (all healthy); `docker logs --tail 40 tund-server-1` (no errors; certificate import line on first switch); the `nodes` table has `fsn-1` with `relay_url = CTRL_PRIV:4443`.

## 4. Prepare the new server

1. Same sysctl file as on fsn-1, then `sysctl --system`.
2. Docker, if missing: `curl -fsSL https://get.docker.com | sh`, then check `docker compose version`.
3. If `ufw` is active: tund-server uses host networking, so ufw applies. Allow 22, 80, 443 and the TCP range (`ufw allow 20000:20999/tcp`, matching `TUND_TCP_PORTS`), and 4443 from `10.0.0.0/8`. Otherwise recommend the Hetzner Cloud Firewall with the same public ports (it only filters public interfaces).
4. Reachability over the private network, from the new node:
   ```sh
   ssh root@$NODE 'for p in 55432 4443; do timeout 3 bash -c "</dev/tcp/CTRL_PRIV/$p" && echo "$p ok" || echo "$p FAILED"; done'
   ```

## 5. Write the config

1. Copy the compose file from this repo (current working tree):
   ```sh
   ssh root@$NODE 'mkdir -p /opt/tund' && scp docker-compose.edge.yml root@$NODE:/opt/tund/docker-compose.yml
   ```
2. Shared settings and secrets, piped from fsn-1 straight into the new node's `.env` (nothing is shown):
   ```sh
   ssh root@tund.io 'cd /opt/tund && grep -E "^(TUND_BASE_DOMAIN|TUND_DASHBOARD_HOST|TUND_SECRET|TUND_INTERNAL_SECRET|TUND_TLS_MODE|TUND_ACME_EMAIL|TUND_ACME_CA|TUND_DNS_PROVIDER|TUND_DNS_API_TOKEN|TUND_PUBLIC_SCHEME|TUND_PUBLIC_PORT|TUND_TCP_PORTS|TUND_TCP_HOST|TUND_DOWNLOAD_BASE_URL|TUND_REDIRECT_HOSTS|TUND_MAX_[A-Z_]+|TUND_CUSTOM_DOMAINS|TUND_AUTO_PIN|TUND_BROWSER_WARNING|TUND_ABUSE_CONTACT|TUND_CAPTURE_MAX_BODY|TUND_RETENTION_DAYS|TUND_BANDWIDTH_KBPS|TUND_TRANSFER_GB|TUND_UNTRUSTED_[A-Z_]+|TUND_SAFE_BROWSING_API_KEY|TUND_IMAGE_TAG|TZ)=" .env
     . ./.env; echo "TUND_DATABASE_URL=postgres://tund:${POSTGRES_PASSWORD}@CTRL_PRIV:55432/tund?sslmode=disable"' \
     | ssh root@$NODE 'umask 077; cat > /opt/tund/.env'
   ```
   The password must be URL-safe. Check without printing it: `case "$POSTGRES_PASSWORD" in *[!A-Za-z0-9._~-]*) echo needs-encoding;; esac`. If it isn't, percent-encode it in the pipeline.
3. Node-specific keys:
   ```sh
   ssh root@$NODE 'cat >> /opt/tund/.env' <<EOF
   TUND_NODE_NAME=$NAME
   TUND_NODE_REGION=$REGION
   TUND_RELAY_ADDR=$NODE_PRIV:4443
   TUND_RELAY_URL=$NODE_PRIV:4443
   TUND_PUBLIC_IP=$NODE_PUBLIC_IP
   TUND_NODE_CAPACITY_MBPS=$CAPACITY
   EOF
   ```
   `NODE_PUBLIC_IP` is the server's public IPv4 (resolve `NODE` if it is a hostname). This is what DNS publishes. `CAPACITY` is the uplink in Mbit/s: ask the user, or use `0` (unknown; network load is then shown without a limit and the balancer weighs CPU only).
4. Verify with key names only: `sed 's/=.*//' /opt/tund/.env | sort | uniq -d` prints nothing (no duplicates). The required keys are present: `TUND_NODE_NAME TUND_RELAY_URL TUND_DATABASE_URL TUND_BASE_DOMAIN TUND_SECRET TUND_INTERNAL_SECRET`. `TUND_IMAGE_TAG` equals fsn-1's. `cd /opt/tund && docker compose config -q` passes.

## 6. Start and verify

```sh
ssh root@$NODE 'cd /opt/tund && docker compose pull -q && docker compose up -d && sleep 15 && docker compose ps && docker compose logs --tail 60 server'
```

- Logs: no database, relay or certificate errors.
- Registered and alive, same version as fsn-1, reporting metrics with the right public IP:
  ```sh
  ssh root@tund.io 'docker exec tund-postgres-1 psql -U tund -d tund -c "select name, role, region, relay_url, public_ip, version, last_seen > now() - interval '"'"'45 seconds'"'"' as alive, round(cpu_pct) as cpu, metrics_at from nodes order by name"'
  ```
  `cpu` is empty on the first heartbeat and filled from the second, 10 s later.
- Health and certificates on the node's public IP (TLS must verify, so no `-k`):
  ```sh
  curl -sS --resolve tund.io:443:$NODE https://tund.io/_tund/health
  curl -sS -o /dev/null -w "%{http_code}\n" --resolve probe-check.tund.io:443:$NODE https://probe-check.tund.io/
  ```
  The first returns `{"ok":true,...}`. The second may be the "no tunnel" page (404 is fine); what matters is a valid wildcard certificate from shared storage.
- Relay: if an HTTP tunnel is online on another node, request it through the new node and expect the tunnel's own response, not a 404 or 502:
  ```sql
  select hostname from tunnels where ended_at is null and proto = 'http' and node is distinct from '<NAME>' limit 1;
  ```
  ```sh
  curl -sS -o /dev/null -w "%{http_code}\n" --resolve <hostname>:443:$NODE https://<hostname>/
  ```
- If a TCP tunnel is online elsewhere, `timeout 3 bash -c "</dev/tcp/$NODE/<port>"` from your machine should connect.

## 7. DNS (ask first)

**If tund-balancer runs (Bunny DNS):** don't add records by hand. Within two intervals of the node passing its health check, the balancer creates its `@` and `*` records. Check its log (`docker compose logs --tail 20` in its directory on fsn-1) for `create` lines naming the node, then verify with `dig` (below). If it reports the node unhealthy, fix that instead of adding records.

**Otherwise,** add `NODE` to the A records `@` and `*` of the base domain. Run the API calls **on fsn-1** so the DNS token never leaves it. The provider is `TUND_DNS_PROVIDER` in fsn-1's `.env`. Always read the current records first, then add, then read back.

**hetzner** (Hetzner Cloud DNS API; the zone name works in the path):

```sh
ssh root@tund.io 'set -a; . /opt/tund/.env; set +a; api=https://api.hetzner.cloud/v1/zones/$TUND_BASE_DOMAIN/rrsets
  for n in @ "*"; do curl -sS -H "Authorization: Bearer $TUND_DNS_API_TOKEN" "$api/$n/A"; echo; done'
# add (repeat the read afterwards):
ssh root@tund.io 'set -a; . /opt/tund/.env; set +a; api=https://api.hetzner.cloud/v1/zones/$TUND_BASE_DOMAIN/rrsets
  for n in @ "*"; do curl -sS -X POST -H "Authorization: Bearer $TUND_DNS_API_TOKEN" -H "Content-Type: application/json" \
    "$api/$n/A/actions/add_records" -d "{\"records\":[{\"value\":\"NODE\"}]}"; echo; done'
```

Removing uses `.../actions/remove_records` with the same body.

**bunny** (`AccessKey` header; apex is `Name: ""`; Type `0` = A; MonitorType `2` = HTTP):

- Zone id: `GET https://api.bunny.net/dnszone?search=$TUND_BASE_DOMAIN`, then take `Items[].Id` where `Domain` equals the base domain (parse with `python3 -c`).
- Read: `GET https://api.bunny.net/dnszone/$ID`, then the `Records` with `Type == 0` and `Name in ("", "*")`.
- Add each: `PUT https://api.bunny.net/dnszone/$ID/records` with `{"Type":0,"Name":"","Value":"NODE","Ttl":300,"MonitorType":2,"Weight":100}`, and the same with `"Name":"*"`.
- Every record in the set should have `MonitorType` 2, so a dead node drops out of the answers. Fix any that don't with `POST https://api.bunny.net/dnszone/$ID/records/$RECORD_ID`.
- Remove: `DELETE https://api.bunny.net/dnszone/$ID/records/$RECORD_ID`.

Other providers aren't covered: give the user the two records to add by hand.

Verify against the authoritative nameservers: `dig +short NS tund.io`, then `dig +short tund.io A @<ns>` and `dig +short anything.tund.io A @<ns>` both include `NODE`.

## 8. Report

A short table: name, public IP, private IP, version, alive, health and certificate OK, relay test, DNS added. Name anything skipped or left for the user (for example a Hetzner Cloud Firewall).

## Removing a node

1. Remove its IP from `@` and `*` (see step 7) and verify with `dig` against the authoritative nameservers.
2. Wait at least the record TTL.
3. `ssh root@$NODE 'cd /opt/tund && docker compose down'`. Clients on it reconnect elsewhere and take over their tunnels.
4. Its `nodes` row stops being alive after 45 s. Delete the row only if the user asks.

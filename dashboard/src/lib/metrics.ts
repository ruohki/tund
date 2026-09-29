import "server-only";
import { db } from "./db";
import { teamHostnameMatch, teamSlugFor } from "./teams";

export type TrafficBucket = { t: string; s2: number; s3: number; s4: number; s5: number };

export type Overview = {
  onlineTunnels: number;
  requests24h: number;
  requestsPrev24h: number;
  failed24h: number;
  p95Ms: number | null;
  p50Ms: number | null;
  buckets: TrafficBucket[];
  topHosts: { hostname: string; requests: number; failed: number; p50Ms: number | null }[];
  hasEverConnected: boolean;
  tokenCount: number;
};

export async function getOverview(userId: string): Promise<Overview> {
  const sql = db();
  const [counts, stats, buckets, topHosts] = await Promise.all([
    sql`
      select
        (select count(*) from tunnels where user_id = ${userId} and ended_at is null) as online,
        (select count(*) from agent_sessions where user_id = ${userId}) as sessions,
        (select count(*) from authtokens where user_id = ${userId}) as tokens`,
    sql`
      select
        count(*) filter (where started_at > now() - interval '24 hours') as req24,
        count(*) filter (where started_at <= now() - interval '24 hours') as prev24,
        count(*) filter (where started_at > now() - interval '24 hours' and (status >= 500 or status = 0)) as failed24,
        percentile_cont(0.95) within group (order by duration_ms)
          filter (where started_at > now() - interval '24 hours' and status > 0) as p95,
        percentile_cont(0.5) within group (order by duration_ms)
          filter (where started_at > now() - interval '24 hours' and status > 0) as p50
      from requests
      where user_id = ${userId} and started_at > now() - interval '48 hours'`,
    sql`
      with slots as (
        select generate_series(date_trunc('hour', now()) - interval '23 hours', date_trunc('hour', now()), interval '1 hour') as t
      )
      select s.t,
        count(r.id) filter (where r.status between 100 and 299) as s2,
        count(r.id) filter (where r.status between 300 and 399) as s3,
        count(r.id) filter (where r.status between 400 and 499) as s4,
        count(r.id) filter (where r.status >= 500 or r.status = 0) as s5
      from slots s
      left join requests r
        on r.user_id = ${userId} and r.started_at >= s.t and r.started_at < s.t + interval '1 hour'
      group by s.t order by s.t`,
    sql`
      select hostname, count(*) as requests,
        count(*) filter (where status >= 500 or status = 0) as failed,
        percentile_cont(0.5) within group (order by duration_ms) filter (where status > 0) as p50
      from requests
      where user_id = ${userId} and started_at > now() - interval '24 hours'
      group by hostname order by requests desc limit 6`,
  ]);
  const c = counts[0];
  const s = stats[0];
  return {
    onlineTunnels: Number(c.online),
    hasEverConnected: Number(c.sessions) > 0,
    tokenCount: Number(c.tokens),
    requests24h: Number(s.req24),
    requestsPrev24h: Number(s.prev24),
    failed24h: Number(s.failed24),
    p95Ms: s.p95 == null ? null : Number(s.p95),
    p50Ms: s.p50 == null ? null : Number(s.p50),
    buckets: buckets.map((b) => ({
      t: (b.t as Date).toISOString(),
      s2: Number(b.s2),
      s3: Number(b.s3),
      s4: Number(b.s4),
      s5: Number(b.s5),
    })),
    topHosts: topHosts.map((h) => ({
      hostname: h.hostname as string,
      requests: Number(h.requests),
      failed: Number(h.failed),
      p50Ms: h.p50 == null ? null : Number(h.p50),
    })),
  };
}

export type TunnelRow = {
  id: string;
  name: string;
  hostname: string;
  publicUrl: string;
  /** http, tcp or tls (docs/SPEC.md "TCP and TLS tunnels"). */
  proto: "http" | "tcp" | "tls";
  remotePort: number | null;
  localAddr: string;
  authMode: string;
  startedAt: string;
  endedAt: string | null;
  /** Captured requests (HTTP) or finished connections (TCP/TLS). */
  requests: number;
  lastRequestAt: string | null;
  /** The hostname is one of this user's personal static hostnames. */
  pinned: boolean;
  /** Slug of the team owning the hostname, when it's a team domain. */
  teamSlug: string | null;
  /** Started by the viewer (only then can it be stopped or pinned from the dashboard). */
  mine: boolean;
  ownerEmail: string;
  client: { hostname: string; os: string; version: string; remoteAddr: string };
};

/**
 * The user's tunnels. With `includeTeams`, also tunnels of other members on
 * domains owned by the user's teams (docs/SPEC.md "Traffic visibility").
 */
export async function listTunnels(
  userId: string,
  opts: { online?: boolean; limit?: number; includeTeams?: boolean } = {},
): Promise<TunnelRow[]> {
  const sql = db();
  const rows = await sql`
    select t.id, t.name, t.hostname, t.public_url, t.local_addr, t.auth_mode, t.started_at, t.ended_at,
      a.hostname as client_hostname, a.client_os, a.client_version, a.remote_addr,
      case when t.proto = 'http' then (select count(*) from requests r where r.tunnel_id = t.id)
           else (select count(*) from connections c where c.tunnel_id = t.id) end as requests,
      case when t.proto = 'http' then (select max(started_at) from requests r where r.tunnel_id = t.id)
           else (select max(started_at) from connections c where c.tunnel_id = t.id) end as last_request_at,
      (exists(select 1 from domains d where d.user_id = t.user_id and d.team_id is null and d.hostname = t.hostname
              and d.kind = 'subdomain')
        or (t.proto = 'tcp' and exists(select 1 from tcp_reservations r
              where r.port = t.remote_port and r.user_id = t.user_id and r.team_id is null))) as pinned,
      t.proto, t.remote_port,
      ${teamSlugFor(sql, "t.hostname")} as team_slug,
      (t.user_id = ${userId}) as mine, u.email as owner_email
    from tunnels t join agent_sessions a on a.id = t.agent_session_id join users u on u.id = t.user_id
    where ${opts.includeTeams ? sql`(t.user_id = ${userId} or ${teamHostnameMatch(sql, "t.hostname", userId)})` : sql`t.user_id = ${userId}`}
      ${opts.online === true ? sql`and t.ended_at is null` : opts.online === false ? sql`and t.ended_at is not null` : sql``}
    order by (t.ended_at is null) desc, coalesce(t.ended_at, t.started_at) desc
    limit ${opts.limit ?? 100}`;
  return rows.map((r) => ({
    id: r.id,
    name: r.name,
    hostname: r.hostname,
    publicUrl: r.public_url,
    proto: (r.proto as TunnelRow["proto"]) ?? "http",
    remotePort: (r.remote_port as number) ?? null,
    localAddr: r.local_addr,
    authMode: r.auth_mode,
    startedAt: (r.started_at as Date).toISOString(),
    endedAt: r.ended_at ? (r.ended_at as Date).toISOString() : null,
    requests: Number(r.requests),
    lastRequestAt: r.last_request_at ? (r.last_request_at as Date).toISOString() : null,
    pinned: Boolean(r.pinned),
    teamSlug: (r.team_slug as string) ?? null,
    mine: Boolean(r.mine),
    ownerEmail: r.owner_email as string,
    client: { hostname: r.client_hostname, os: r.client_os, version: r.client_version, remoteAddr: r.remote_addr },
  }));
}

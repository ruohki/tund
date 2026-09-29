import "server-only";
import { db } from "./db";
import { internalGet } from "./internal";
import type { TrafficBucket } from "./metrics";

export type EdgeStatus = {
  version: string;
  started_at: string;
  base_domain: string;
  dashboard_host: string;
  tls_mode: string;
  dns_provider: string;
  tunnels: number;
  sessions: number;
  recorder_queue: number;
  certificates: { name: string; not_after: string; issuer: string }[];
  tcp_ports?: { from: number; to: number } | null;
  tcp_host?: string;
};

export async function edgeStatus(): Promise<{ status: EdgeStatus | null; error: string | null }> {
  try {
    return { status: await internalGet<EdgeStatus>("/internal/status"), error: null };
  } catch (err) {
    return { status: null, error: err instanceof Error ? err.message : String(err) };
  }
}

export type AdminOverview = {
  users: { total: number; admins: number; disabled: number; unverified: number; new7d: number };
  teams: number;
  domains: { static: number; custom: number };
  tunnelsOnline: number;
  requests24h: number;
  failed24h: number;
  connections24h: number;
  monthBytes: number;
  topTransfer: { id: string; email: string; bytes: number }[];
  buckets: TrafficBucket[];
  topAccounts: { id: string; email: string; requests: number; failed: number }[];
  recentSignups: { id: string; email: string; name: string; createdAt: string; verified: boolean }[];
};

export async function adminOverview(): Promise<AdminOverview> {
  const sql = db();
  const [[u], [t], [d], [tun], [r], buckets, top, recent, [c], [mb], topT] = await Promise.all([
    sql`
      select count(*)::int as total, count(*) filter (where is_admin)::int as admins,
        count(*) filter (where disabled_at is not null)::int as disabled,
        count(*) filter (where email_verified_at is null)::int as unverified,
        count(*) filter (where created_at > now() - interval '7 days')::int as new7d
      from users`,
    sql`select count(*)::int as n from teams`,
    sql`select count(*) filter (where kind = 'subdomain')::int as static, count(*) filter (where kind = 'custom')::int as custom from domains`,
    sql`select count(*)::int as n from tunnels where ended_at is null`,
    sql`
      select count(*)::int as n, count(*) filter (where status >= 500 or status = 0)::int as failed
      from requests where started_at > now() - interval '24 hours'`,
    sql`
      with slots as (
        select generate_series(date_trunc('hour', now()) - interval '23 hours', date_trunc('hour', now()), interval '1 hour') as t
      )
      select s.t,
        count(r.id) filter (where r.status between 100 and 299)::int as s2,
        count(r.id) filter (where r.status between 300 and 399)::int as s3,
        count(r.id) filter (where r.status between 400 and 499)::int as s4,
        count(r.id) filter (where r.status >= 500 or r.status = 0)::int as s5
      from slots s left join requests r on r.started_at >= s.t and r.started_at < s.t + interval '1 hour'
      group by s.t order by s.t`,
    sql`
      select u.id, u.email, count(*)::int as requests, count(*) filter (where r.status >= 500 or r.status = 0)::int as failed
      from requests r join users u on u.id = r.user_id
      where r.started_at > now() - interval '24 hours'
      group by u.id, u.email order by requests desc limit 8`,
    sql`select id, email, name, created_at, email_verified_at from users order by created_at desc limit 8`,
    sql`select count(*)::int as n from connections where started_at > now() - interval '24 hours'`,
    sql`
      select coalesce(sum(bytes_in + bytes_out), 0)::bigint as n from usage_daily
      where day >= date_trunc('month', now() at time zone 'utc')::date`,
    sql`
      select u.id, u.email, sum(d.bytes_in + d.bytes_out)::bigint as bytes
      from usage_daily d join users u on u.id = d.user_id
      where d.day >= date_trunc('month', now() at time zone 'utc')::date
      group by u.id, u.email order by bytes desc limit 8`,
  ]);
  return {
    users: { total: u.total, admins: u.admins, disabled: u.disabled, unverified: u.unverified, new7d: u.new7d },
    teams: t.n,
    domains: { static: d.static, custom: d.custom },
    tunnelsOnline: tun.n,
    requests24h: r.n,
    failed24h: r.failed,
    connections24h: c.n,
    monthBytes: Number(mb.n),
    topTransfer: topT.map((x) => ({ id: x.id, email: x.email, bytes: Number(x.bytes) })),
    buckets: buckets.map((b) => ({ t: (b.t as Date).toISOString(), s2: b.s2, s3: b.s3, s4: b.s4, s5: b.s5 })),
    topAccounts: top.map((x) => ({ id: x.id, email: x.email, requests: x.requests, failed: x.failed })),
    recentSignups: recent.map((x) => ({
      id: x.id,
      email: x.email,
      name: x.name,
      createdAt: (x.created_at as Date).toISOString(),
      verified: Boolean(x.email_verified_at),
    })),
  };
}

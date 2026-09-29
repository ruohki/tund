import type { Metadata } from "next";
import Link from "next/link";
import { CircleAlert, ShieldCheck, TriangleAlert } from "lucide-react";
import { adminOverview, edgeStatus } from "@/lib/admin-stats";
import { formatCompact, formatDateTime, formatNumber, formatPercent, formatTransfer, timeAgo } from "@/lib/format";
import { smtpConfigured } from "@/lib/mail";
import { Badge, Panel } from "@/components/ui";
import { StatTile } from "@/components/stat-tile";
import { TrafficChart } from "@/components/traffic-chart";

export const metadata: Metadata = { title: "Admin" };

const DAY = 86_400_000;

/** Whole days until `iso` (negative when past). */
function daysUntil(iso: string) {
  return Math.floor((Date.parse(iso) - Date.now()) / DAY);
}

function CertRow({ name, notAfter, issuer }: { name: string; notAfter: string; issuer: string }) {
  const days = daysUntil(notAfter);
  // Status color with icon + words, never color alone.
  const tone = days < 7 ? "critical" : days < 21 ? "warning" : "ok";
  return (
    <li className="flex flex-wrap items-center justify-between gap-3 px-4 py-2.5">
      <span className="min-w-0">
        <span className="block truncate font-mono text-[13px] text-ink">{name}</span>
        <span className="block truncate text-[12px] text-muted">{issuer || "unknown issuer"}</span>
      </span>
      <span
        className="flex items-center gap-1.5 text-[12.5px] font-medium"
        style={{ color: tone === "critical" ? "var(--danger)" : tone === "warning" ? "var(--sodium-ink)" : "var(--ok)" }}
        title={formatDateTime(notAfter)}
      >
        {tone === "ok" ? <ShieldCheck size={14} /> : tone === "warning" ? <TriangleAlert size={14} /> : <CircleAlert size={14} />}
        {days < 0 ? `Expired ${-days} days ago` : days === 0 ? "Expires today" : `Expires in ${days} days`}
      </span>
    </li>
  );
}

export default async function AdminOverviewPage() {
  const [o, edge, mailOn] = await Promise.all([adminOverview(), edgeStatus(), smtpConfigured()]);
  const s = edge.status;
  const maxTop = Math.max(1, ...o.topAccounts.map((a) => a.requests));

  return (
    <>
      <section aria-label="Instance" className="mb-6 grid grid-cols-2 rounded-lg border border-line bg-surface lg:grid-cols-4 lg:divide-x lg:divide-line max-lg:[&>*:nth-child(odd)]:border-r max-lg:[&>*:nth-child(-n+2)]:border-b max-lg:[&>*]:border-line">
        <StatTile
          label="Accounts"
          value={formatNumber(o.users.total)}
          context={`${o.users.new7d} new this week${o.users.disabled ? `, ${o.users.disabled} disabled` : ""}`}
        />
        <StatTile
          label="Online tunnels"
          value={formatNumber(s?.tunnels ?? o.tunnelsOnline)}
          live={(s?.tunnels ?? o.tunnelsOnline) > 0}
          context={s ? `${s.sessions} connected ${s.sessions === 1 ? "client" : "clients"}` : "From the database"}
        />
        <StatTile
          label="Requests, 24h"
          value={formatCompact(o.requests24h)}
          context={[
            o.requests24h ? `${formatPercent(o.failed24h / o.requests24h)} failed` : "No HTTP traffic",
            o.connections24h ? `${formatNumber(o.connections24h)} TCP/TLS connections` : null,
          ]
            .filter(Boolean)
            .join(", ")}
        />
        <StatTile
          label="Transfer this month"
          value={formatTransfer(o.monthBytes)}
          context={`${o.teams} teams, ${o.domains.static} static hostnames, ${o.domains.custom} custom domains`}
        />
      </section>

      <div className="mb-6 grid grid-cols-1 gap-6 xl:grid-cols-[minmax(0,1.6fr)_minmax(0,1fr)]">
        <Panel title="Requests per hour" description="All accounts, last 24 hours.">
          <div className="p-4">
            <TrafficChart buckets={o.buckets} />
          </div>
        </Panel>
        <Panel title="Edge server" description={s ? `tund-server ${s.version}` : "Status unavailable"}>
          {s ? (
            <>
              <dl className="grid grid-cols-2 gap-x-4 gap-y-2 px-4 py-3 text-[13px]">
                <div>
                  <dt className="text-[12px] text-muted">Up since</dt>
                  <dd className="text-ink" title={formatDateTime(s.started_at)}>{timeAgo(s.started_at)}</dd>
                </div>
                <div>
                  <dt className="text-[12px] text-muted">TLS</dt>
                  <dd className="text-ink">
                    {s.tls_mode}
                    {s.dns_provider ? <span className="text-muted"> via {s.dns_provider}</span> : null}
                  </dd>
                </div>
                <div>
                  <dt className="text-[12px] text-muted">Base domain</dt>
                  <dd className="truncate font-mono text-[12.5px] text-ink">{s.base_domain}</dd>
                </div>
                <div>
                  <dt className="text-[12px] text-muted">Recorder queue</dt>
                  <dd className="text-ink tabular">{formatNumber(s.recorder_queue)}</dd>
                </div>
              </dl>
              <h3 className="border-y border-line bg-surface-2 px-4 py-1.5 text-[12px] font-semibold text-ink-2">Certificates</h3>
              {s.certificates?.length ? (
                <ul className="divide-y divide-line">
                  {s.certificates.map((c) => (
                    <CertRow key={c.name} name={c.name} notAfter={c.not_after} issuer={c.issuer} />
                  ))}
                </ul>
              ) : (
                <p className="px-4 py-3 text-[13px] text-muted">
                  No certificate in the cache yet. With ACME they&apos;re issued on the first HTTPS request.
                </p>
              )}
            </>
          ) : (
            <p className="flex items-start gap-2 px-4 py-4 text-[13px] text-danger">
              <CircleAlert size={15} className="mt-0.5 shrink-0" />
              {edge.error}
            </p>
          )}
        </Panel>
      </div>

      <div className="grid grid-cols-1 gap-6 lg:grid-cols-2 xl:grid-cols-3">
        <Panel title="Most traffic" description="Accounts by requests in the last 24 hours.">
          {o.topAccounts.length ? (
            <ul className="divide-y divide-line">
              {o.topAccounts.map((a) => (
                <li key={a.id}>
                  <Link href={`/admin/users/${a.id}`} className="block px-4 py-2.5 hover:bg-surface-2">
                    <span className="flex items-baseline justify-between gap-3">
                      <span className="truncate text-[13px] text-ink">{a.email}</span>
                      <span className="shrink-0 text-[13px] font-medium tabular text-ink">{formatNumber(a.requests)}</span>
                    </span>
                    <span className="mt-1.5 block h-1 rounded-full bg-surface-3">
                      <span className="block h-1 rounded-full bg-s3xx" style={{ width: `${Math.max(2, (a.requests / maxTop) * 100)}%` }} />
                    </span>
                    {a.failed ? <span className="mt-1 block text-[12px] text-muted">{formatNumber(a.failed)} failed</span> : null}
                  </Link>
                </li>
              ))}
            </ul>
          ) : (
            <p className="px-4 py-4 text-[13px] text-muted">No traffic in the last 24 hours.</p>
          )}
        </Panel>
        <Panel title="Most transfer" description="Accounts by bytes this month (UTC), both directions.">
          {o.topTransfer.length ? (
            <ul className="divide-y divide-line">
              {o.topTransfer.map((a) => (
                <li key={a.id}>
                  <Link href={`/admin/users/${a.id}`} className="block px-4 py-2.5 hover:bg-surface-2">
                    <span className="flex items-baseline justify-between gap-3">
                      <span className="truncate text-[13px] text-ink">{a.email}</span>
                      <span className="shrink-0 text-[13px] font-medium tabular text-ink">{formatTransfer(a.bytes)}</span>
                    </span>
                    <span className="mt-1.5 block h-1 rounded-full bg-surface-3">
                      <span
                        className="block h-1 rounded-full"
                        style={{ width: `${Math.max(2, (a.bytes / Math.max(1, o.topTransfer[0].bytes)) * 100)}%`, background: "var(--series-in)" }}
                      />
                    </span>
                  </Link>
                </li>
              ))}
            </ul>
          ) : (
            <p className="px-4 py-4 text-[13px] text-muted">No transfer recorded this month.</p>
          )}
        </Panel>
        <Panel
          title="Recent sign-ups"
          actions={
            <Link href="/admin/users" className="text-[12.5px] font-medium text-ink-2 hover:text-ink">
              All users
            </Link>
          }
        >
          <ul className="divide-y divide-line">
            {o.recentSignups.map((u) => (
              <li key={u.id}>
                <Link href={`/admin/users/${u.id}`} className="flex items-center justify-between gap-3 px-4 py-2.5 hover:bg-surface-2">
                  <span className="min-w-0">
                    <span className="block truncate text-[13px] text-ink">{u.name || u.email}</span>
                    {u.name ? <span className="block truncate text-[12px] text-muted">{u.email}</span> : null}
                  </span>
                  <span className="flex shrink-0 items-center gap-2 text-[12px] text-muted">
                    {!u.verified ? <Badge tone="live">Unverified</Badge> : null}
                    <span title={formatDateTime(u.createdAt)}>{timeAgo(u.createdAt)}</span>
                  </span>
                </Link>
              </li>
            ))}
          </ul>
        </Panel>
      </div>
      {!mailOn ? (
        <p className="mt-6 text-[13px] text-muted">
          Email isn&apos;t configured, so password resets, verification and invite emails are off.{" "}
          <Link href="/admin/email" className="font-medium text-ink underline underline-offset-4">
            Set up email
          </Link>
        </p>
      ) : null}
    </>
  );
}

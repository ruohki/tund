import Link from "next/link";
import { Cable } from "lucide-react";
import type { User } from "@/lib/auth";
import { config, publicConfig } from "@/lib/config";
import { defaultStaticHostname } from "@/lib/static-hostnames";
import { getOverview, listTunnels } from "@/lib/metrics";
import { formatCompact, formatDuration, formatNumber, formatPercent } from "@/lib/format";
import { AuthBadge, ButtonLink, EmptyState, PageHeader, Panel, ProtoBadge } from "@/components/ui";
import { Command } from "@/components/client-ui";
import { StatTile } from "@/components/stat-tile";
import { TrafficChart } from "@/components/traffic-chart";
import { RouteLine } from "@/components/route-line";
import { LiveRefresh } from "@/components/live-refresh";
import { InstallSteps } from "@/components/install-steps";
import { QuotaBanner, TransferUsage } from "@/components/transfer-usage";

/** The signed-in home page. Rendered by app/page.tsx, which also serves the public landing page. */
export async function Overview({ user }: { user: User }) {
  const cfg = publicConfig();
  const [o, online, defaultHost] = await Promise.all([
    getOverview(user.id),
    listTunnels(user.id, { online: true, limit: 20 }),
    defaultStaticHostname(user.id),
  ]);
  const staticUrl = defaultHost ? config().publicUrl(defaultHost) : null;

  const errorRate = o.requests24h ? o.failed24h / o.requests24h : 0;
  const delta = o.requestsPrev24h ? (o.requests24h - o.requestsPrev24h) / o.requestsPrev24h : null;
  const maxHost = Math.max(1, ...o.topHosts.map((h) => h.requests));

  return (
    <>
      <LiveRefresh />
      <QuotaBanner user={user} />
      <PageHeader
        title="Overview"
        description={
          staticUrl ? (
            <>
              <code className="font-mono text-[13px] text-ink">tund http 3000</code> puts your service on{" "}
              <a href={staticUrl} target="_blank" rel="noreferrer" className="font-mono text-[13px] text-ink underline-offset-4 hover:underline">
                {staticUrl.replace(/^https?:\/\//, "")}
              </a>{" "}
              every time. Traffic below covers the last 24 hours.
            </>
          ) : (
            <>
              Tunnels on <span className="font-mono text-[13px] text-ink">*.{cfg.baseDomain}</span> and the traffic they
              carried in the last 24 hours.
            </>
          )
        }
        actions={
          <ButtonLink href="/inspect" variant="secondary">
            Open inspector
          </ButtonLink>
        }
      />

      {!o.hasEverConnected ? (
        <Panel
          title="Start your first tunnel"
          description="Two commands and a local port is reachable on HTTPS."
          className="mb-6"
          bodyClassName="p-5"
        >
          <InstallSteps dashboardUrl={cfg.dashboardUrl} staticUrl={staticUrl} />
        </Panel>
      ) : (
        <Panel
          title="Online now"
          description={
            online.length
              ? `${online.length} ${online.length === 1 ? "tunnel is" : "tunnels are"} routing traffic.`
              : "No tunnel is connected right now."
          }
          actions={
            <ButtonLink href="/tunnels" variant="ghost" size="sm">
              All tunnels
            </ButtonLink>
          }
          className="mb-6"
        >
          {online.length ? (
            <ul className="grid grid-cols-[minmax(0,auto)_minmax(2rem,1fr)_auto] divide-y divide-line md:grid-cols-[minmax(0,auto)_minmax(3rem,1fr)_auto_auto]">
              {online.map((t) => (
                <li
                  key={t.id}
                  className="col-span-full grid grid-cols-subgrid items-center gap-x-3 gap-y-2 px-4 py-3"
                >
                  <RouteLine
                    hostname={t.hostname}
                    publicUrl={t.publicUrl}
                    localAddr={t.localAddr}
                    proto={t.proto}
                    online
                    contents
                  />
                  <div className="col-span-full flex items-center gap-3 text-[12.5px] text-muted md:col-span-1 md:ml-3 md:justify-end">
                    <ProtoBadge proto={t.proto} />
                    {t.proto === "http" ? <AuthBadge mode={t.authMode} /> : null}
                    <span className="tabular">{formatNumber(t.requests)} req</span>
                    <span>{t.client.hostname || t.client.os}</span>
                    <Link
                      href={`/inspect?${t.proto === "http" ? "" : "view=connections&"}host=${encodeURIComponent(t.hostname)}`}
                      className="font-medium text-ink hover:underline"
                    >
                      Inspect
                    </Link>
                  </div>
                </li>
              ))}
            </ul>
          ) : (
            <EmptyState icon={<Cable size={22} />} title="Nothing is exposed at the moment">
              <p className="mb-3">Start a tunnel from any machine with the client installed:</p>
              <Command className="text-left">tund http 3000</Command>
            </EmptyState>
          )}
        </Panel>
      )}

      <section aria-label="Last 24 hours" className="mb-6 grid grid-cols-2 divide-line rounded-lg border border-line bg-surface lg:grid-cols-4 lg:divide-x max-lg:[&>*:nth-child(odd)]:border-r max-lg:[&>*:nth-child(-n+2)]:border-b max-lg:[&>*]:border-line">
        <StatTile
          label="Online tunnels"
          value={o.onlineTunnels}
          live={o.onlineTunnels > 0}
          context={o.onlineTunnels ? "Routing traffic now" : "None connected"}
        />
        <StatTile
          label="Requests, 24h"
          value={formatCompact(o.requests24h)}
          context={
            delta == null
              ? "No traffic the day before"
              : `${delta >= 0 ? "+" : "−"}${formatPercent(Math.abs(delta))} vs previous 24h`
          }
        />
        <StatTile
          label="Failed requests"
          value={formatPercent(errorRate)}
          context={o.failed24h ? `${formatNumber(o.failed24h)} with 5xx or no response` : "No 5xx or dropped requests"}
        />
        <StatTile
          label="p95 latency"
          value={o.p95Ms == null ? "—" : formatDuration(o.p95Ms)}
          context={o.p50Ms == null ? "No completed requests yet" : `Median ${formatDuration(o.p50Ms)}`}
        />
      </section>

      <div className="grid grid-cols-1 gap-6 xl:grid-cols-[minmax(0,1.9fr)_minmax(0,1fr)]">
        <Panel title="Requests per hour" description="Last 24 hours, by response class.">
          <div className="p-4">
            <TrafficChart buckets={o.buckets} />
          </div>
        </Panel>
        <Panel title="Busiest hostnames" description="Last 24 hours.">
          {o.topHosts.length ? (
            <ul className="divide-y divide-line">
              {o.topHosts.map((h) => (
                <li key={h.hostname}>
                  <Link
                    href={`/inspect?host=${encodeURIComponent(h.hostname)}`}
                    className="block px-4 py-2.5 hover:bg-surface-2"
                  >
                    <div className="flex items-baseline justify-between gap-3">
                      <span className="truncate font-mono text-[12.5px] text-ink">{h.hostname}</span>
                      <span className="shrink-0 text-[13px] font-medium tabular text-ink">{formatNumber(h.requests)}</span>
                    </div>
                    <div className="mt-1.5 h-1 rounded-full bg-surface-3">
                      <div
                        className="h-1 rounded-full bg-s3xx"
                        style={{ width: `${Math.max(2, (h.requests / maxHost) * 100)}%` }}
                      />
                    </div>
                    <p className="mt-1 text-[12px] text-muted">
                      {h.failed ? `${formatNumber(h.failed)} failed, ` : ""}
                      median {formatDuration(h.p50Ms)}
                    </p>
                  </Link>
                </li>
              ))}
            </ul>
          ) : (
            <p className="px-4 py-6 text-[13px] text-muted">
              No requests in the last 24 hours.{o.tokenCount === 0 ? " Create an auth token to connect a client." : ""}
            </p>
          )}
        </Panel>
      </div>
      <TransferUsage user={user} className="mt-6" />
    </>
  );
}

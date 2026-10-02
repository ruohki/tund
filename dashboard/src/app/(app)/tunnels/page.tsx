import type { Metadata } from "next";
import Link from "next/link";
import { Cable, History } from "lucide-react";
import { requireUser } from "@/lib/auth";
import { config } from "@/lib/config";
import { getSettings } from "@/lib/settings";
import { db } from "@/lib/db";
import { countTunnels, listTunnels } from "@/lib/metrics";
import { PAGE_SIZE, Pager, pageOffset, pageParam } from "@/components/pager";
import { formatDateTime, formatDuration, formatLifetime, formatNumber, osLabel, sessionLength, timeAgo, timeLeft } from "@/lib/format";
import { effectiveLimits } from "@/lib/usage";
import { AuthBadge, Badge, EmptyState, PageHeader, Panel, ProtoBadge } from "@/components/ui";
import { Command } from "@/components/client-ui";
import { RouteLine } from "@/components/route-line";
import { LiveRefresh } from "@/components/live-refresh";
import { StopButton } from "./stop-button";
import { PinButton } from "./pin-button";

export const metadata: Metadata = { title: "Tunnels" };

async function ownerTrusted(userId: string): Promise<boolean> {
  const [row] = await db()`select trusted from users where id = ${userId}`;
  return Boolean(row?.trusted);
}

function WarningBadge() {
  return (
    <Badge
      tone="outline"
      title="Browsers see a one-time warning page before your site. API clients and fetch() can skip it with the header Tund-Skip-Browser-Warning: 1. Password- or SSO-protected tunnels, custom domains and trusted accounts never show it."
    >
      Browser warning
    </Badge>
  );
}

/** HTTP tunnels open the request inspector; TCP/TLS tunnels its connections view. */
function inspectHref(t: { hostname: string; proto: string }) {
  return `/inspect?${t.proto === "http" ? "" : "view=connections&"}host=${encodeURIComponent(t.hostname)}`;
}

function TeamBadge({ slug }: { slug: string }) {
  return (
    <Link href={`/teams/${slug}`} title={`A domain of team ${slug}; its members see this traffic`}>
      <Badge tone="outline">Team {slug}</Badge>
    </Link>
  );
}

function StaticBadge() {
  return (
    <Badge tone="outline" title="One of your static hostnames">
      Static
    </Badge>
  );
}

export default async function TunnelsPage({ searchParams }: PageProps<"/tunnels">) {
  const user = await requireUser();
  const page = pageParam((await searchParams).page);
  const [online, recent, recentTotal, limits] = await Promise.all([
    listTunnels(user.id, { online: true, limit: 500, includeTeams: true }),
    listTunnels(user.id, { online: false, limit: PAGE_SIZE, offset: pageOffset(page), includeTeams: true }),
    countTunnels(user.id, { online: false, includeTeams: true }),
    effectiveLimits(user),
  ]);
  // Your own tunnels close after your lifetime; team members' follow theirs.
  const closesIn = (t: { mine: boolean; startedAt: string }) =>
    t.mine && limits.lifetimeMinutes ? timeLeft(t.startedAt, limits.lifetimeMinutes) : null;
  const myOnline = online.filter((t) => t.mine).length;
  const cfg = config();
  const settings = await getSettings();
  const maxTunnels = settings.limit_tunnels > 0 && !user.isAdmin ? settings.limit_tunnels : null;
  // Only single-label names under the server's own domain can become static hostnames.
  // Mirrors the edge's rule (docs/SPEC.md "Browser warning page") so users know which tunnels show it.
  const ownerExempt = user.isAdmin || (settings.browser_warning && (await ownerTrusted(user.id)));
  const showsWarning = (t: { hostname: string; authMode: string; mine: boolean }) =>
    settings.browser_warning && t.mine && !ownerExempt && t.authMode === "none" && t.hostname.endsWith(`.${cfg.baseDomain}`);
  const canPin = (t: { hostname: string; pinned: boolean; mine: boolean; teamSlug: string | null; proto: string; remotePort: number | null }) =>
    t.mine && !t.pinned && !t.teamSlug && (t.proto === "tcp" ? t.remotePort !== null : pinnable(t.hostname));
  const pinnable = (hostname: string) => {
    const suffix = `.${cfg.baseDomain}`;
    return hostname.endsWith(suffix) && !hostname.slice(0, -suffix.length).includes(".");
  };

  return (
    <>
      <LiveRefresh throttleMs={10_000} />
      <PageHeader
        title="Tunnels"
        description="Every hostname a client has bound. Online tunnels route traffic to the machine that opened them."
      />

      <Panel
        title="Online"
        description={
          maxTunnels
            ? `${myOnline} of ${maxTunnels} online. Your account can run up to ${maxTunnels} tunnels at once.`
            : online.length
              ? `${online.length} connected`
              : undefined
        }
        className="mb-6"
      >
        {online.length ? (
          <ul className="divide-y divide-line">
            {online.map((t) => (
              <li key={t.id} className="px-4 py-4">
                <div className="flex flex-wrap items-center justify-between gap-3">
                  <div className="flex min-w-0 items-center gap-2.5">
                    <span className="live-dot shrink-0" aria-hidden />
                    <span className="truncate text-[15px] font-semibold text-ink">{t.name}</span>
                    <ProtoBadge proto={t.proto} />
                    {t.proto === "http" ? <AuthBadge mode={t.authMode} /> : null}
                    {t.pinned ? <StaticBadge /> : null}
                    {t.teamSlug ? <TeamBadge slug={t.teamSlug} /> : null}
                    {showsWarning(t) ? <WarningBadge /> : null}
                    {!t.mine ? <span className="truncate text-[12px] text-muted">by {t.ownerEmail}</span> : null}
                  </div>
                  <div className="flex items-center gap-2">
                    {canPin(t) ? <PinButton id={t.id} /> : null}
                    <Link
                      href={inspectHref(t)}
                      className="inline-flex h-7 items-center rounded-[5px] px-2.5 text-[13px] font-medium text-ink-2 hover:bg-surface-3 hover:text-ink"
                    >
                      {t.proto === "http" ? "Inspect traffic" : "Connections"}
                    </Link>
                    {t.mine ? <StopButton id={t.id} /> : null}
                  </div>
                </div>
                <RouteLine
                  className="mt-3"
                  hostname={t.hostname}
                  publicUrl={t.publicUrl}
                  localAddr={t.localAddr}
                  proto={t.proto}
                  online
                />
                <dl className="mt-3 grid grid-cols-2 gap-x-6 gap-y-1 text-[12.5px] sm:grid-cols-4">
                  <div>
                    <dt className="text-muted">Client</dt>
                    <dd className="truncate text-ink" title={t.client.remoteAddr}>
                      {t.client.hostname || "unknown host"}
                    </dd>
                  </div>
                  <div>
                    <dt className="text-muted">Platform</dt>
                    <dd className="text-ink">
                      {osLabel(t.client.os)}
                      {t.client.version ? <span className="text-muted"> v{t.client.version}</span> : null}
                    </dd>
                  </div>
                  <div>
                    <dt className="text-muted">Connected</dt>
                    <dd className="text-ink" title={formatDateTime(t.startedAt)}>
                      {sessionLength(t.startedAt, null)}
                      {closesIn(t) !== null ? (
                        <span className="text-muted" title={`This server closes tunnels after ${formatLifetime(limits.lifetimeMinutes)}`}>
                          , closes in {formatDuration(closesIn(t))}
                        </span>
                      ) : null}
                    </dd>
                  </div>
                  <div>
                    <dt className="text-muted">{t.proto === "http" ? "Requests" : "Connections"}</dt>
                    <dd className="text-ink tabular">
                      {formatNumber(t.requests)}
                      {t.lastRequestAt ? <span className="text-muted">, last {timeAgo(t.lastRequestAt)}</span> : null}
                    </dd>
                  </div>
                </dl>
              </li>
            ))}
          </ul>
        ) : (
          <EmptyState icon={<Cable size={22} />} title="No tunnel is online">
            <p className="mb-3">Start one on the machine running your service:</p>
            <Command className="text-left">tund http 3000</Command>
          </EmptyState>
        )}
      </Panel>

      <Panel title="Recent" description="Tunnels that have disconnected. Their captured requests stay available until retention removes them.">
        {recent.length ? (
          <div className="overflow-x-auto scroll-thin">
            <table className="w-full min-w-[840px] text-[13px]">
              <thead>
                <tr className="border-b border-line text-left text-[12px] text-muted">
                  <th className="px-4 py-2 font-medium">Hostname</th>
                  <th className="px-4 py-2 font-medium">Local address</th>
                  <th className="px-4 py-2 font-medium">Access</th>
                  <th className="px-4 py-2 font-medium">Client</th>
                  <th className="px-4 py-2 font-medium">Ended</th>
                  <th className="px-4 py-2 font-medium">Duration</th>
                  <th className="px-4 py-2 text-right font-medium">Requests</th>
                  <th className="px-4 py-2" />
                </tr>
              </thead>
              <tbody className="divide-y divide-line">
                {recent.map((t) => (
                  <tr key={t.id} className="hover:bg-surface-2">
                    <td className="px-4 py-2">
                      <Link
                        href={inspectHref(t)}
                        className="font-mono text-[12.5px] text-ink hover:underline"
                      >
                        {t.hostname}
                      </Link>
                      {t.pinned ? (
                        <span className="ml-2">
                          <StaticBadge />
                        </span>
                      ) : null}
                      {t.teamSlug ? (
                        <span className="ml-2">
                          <TeamBadge slug={t.teamSlug} />
                        </span>
                      ) : null}
                    </td>
                    <td className="px-4 py-2 font-mono text-[12.5px] text-ink-2">{t.localAddr}</td>
                    <td className="px-4 py-2">
                      <span className="flex gap-1.5">
                        <ProtoBadge proto={t.proto} />
                        {t.proto === "http" ? <AuthBadge mode={t.authMode} /> : null}
                      </span>
                    </td>
                    <td className="px-4 py-2 text-ink-2">
                      {t.client.hostname || osLabel(t.client.os)}
                      {!t.mine ? <span className="block text-[12px] text-muted">{t.ownerEmail}</span> : null}
                    </td>
                    <td className="px-4 py-2 text-ink-2" title={formatDateTime(t.endedAt!)}>
                      {timeAgo(t.endedAt)}
                    </td>
                    <td className="px-4 py-2 text-ink-2 tabular">{sessionLength(t.startedAt, t.endedAt)}</td>
                    <td className="px-4 py-2 text-right tabular text-ink">{formatNumber(t.requests)}</td>
                    <td className="px-4 py-1.5 text-right">
                      {canPin(t) ? <PinButton id={t.id} /> : null}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : (
          <EmptyState icon={<History size={22} />} title="No history yet">
            Tunnels appear here once they disconnect.
          </EmptyState>
        )}
        <Pager path="/tunnels" page={page} total={recentTotal} />
      </Panel>
    </>
  );
}

import type { Metadata } from "next";
import Link from "next/link";
import { Globe, Waypoints } from "lucide-react";
import { requireUser } from "@/lib/auth";
import { customDomainsEnabled, customDomainsOffMessage, passthroughEnabled } from "@/lib/abuse";
import { publicConfig } from "@/lib/config";
import { serverAddresses } from "@/lib/servers";
import { loadDomainItems, myTeamDomains } from "@/lib/domain-items";
import { domainUsage } from "@/lib/static-hostnames";
import { providerOptions } from "@/lib/teams";
import { listTcpReservations, tcpConfig } from "@/lib/tcp";
import { Badge, EmptyState, PageHeader, Panel } from "@/components/ui";
import { AddCustomDomainForm, DomainRow, StaticHostnameForms } from "./domain-forms";
import { ReserveTcpForm, TcpPortRow } from "./tcp-ports";

export const metadata: Metadata = { title: "Domains" };

function Usage({ used, limit, noun, label }: { used: number; limit: number | null; noun: string; label?: string }) {
  if (limit === null) return <span className="text-[12.5px] text-muted tabular">{used ? `${used} ${noun}` : null}</span>;
  return (
    <span className={`text-[12.5px] tabular ${used >= limit ? "font-medium text-ink" : "text-muted"}`}>
      {label ? `${label} ` : ""}
      {used} of {limit}
      {label ? "" : " used"}
    </span>
  );
}

export default async function DomainsPage() {
  const user = await requireUser();
  const cfg = { ...publicConfig(), serverIps: await serverAddresses() };
  const [items, providers, pinned, custom, teamDomains, tcpRange, tcpPorts, customOn, passthroughOn] = await Promise.all([
    loadDomainItems(user.id, null),
    providerOptions(user.id, null),
    domainUsage(user, "subdomain"),
    domainUsage(user, "custom"),
    myTeamDomains(user.id),
    tcpConfig(),
    listTcpReservations(user.id, { teamId: null }),
    customDomainsEnabled(user),
    passthroughEnabled(user),
  ]);
  // Static TCP ports only for accounts that may open TCP tunnels.
  const tcp = passthroughOn ? tcpRange : null;
  cfg.passthrough = passthroughOn;
  const statics = items.filter((i) => i.kind === "subdomain");
  const customs = items.filter((i) => i.kind === "custom");
  const provs = providers.map((p) => ({ id: p.id, name: p.name, slug: p.slug, ref: p.ref }));
  const staticFull =
    pinned.limit !== null && pinned.used >= pinned.limit
      ? `You're using all ${pinned.limit} static ${pinned.limit === 1 ? "address" : "addresses"} (static hostnames${tcp ? " and TCP ports" : ""}) your account can have. Release one to add another.`
      : null;
  const customFull = !customOn
    ? await customDomainsOffMessage()
    : custom.limit !== null && custom.used >= custom.limit
      ? `You're using all ${custom.limit} custom ${custom.limit === 1 ? "domain" : "domains"} your account can have. Remove one to add another.`
      : null;

  return (
    <>
      <PageHeader
        title="Domains"
        description="Keep the same URLs between runs, bring your own domains, and decide who may open them."
      />

      <Panel
        title={
          <>
            Static hostnames <span className="font-mono text-[13px] font-normal text-muted">*.{cfg.baseDomain}</span>
          </>
        }
        description={
          <>
            Names that belong to your account. <code className="font-mono text-[12.5px] text-ink-2">tund http 3000</code>{" "}
            uses your default every time; pick another with <code className="font-mono text-[12.5px] text-ink-2">--subdomain</code>.
          </>
        }
        actions={
          <Usage
            used={pinned.used}
            limit={pinned.limit}
            noun={pinned.used === 1 ? "static address" : "static addresses"}
            label={tcp ? "Static addresses" : undefined}
          />
        }
        className="mb-6"
      >
        <div className="border-b border-line p-4">
          <StaticHostnameForms baseDomain={cfg.baseDomain} full={staticFull} />
        </div>
        {statics.length ? (
          <ul className="divide-y divide-line">
            {statics.map((d) => (
              <DomainRow key={d.id} domain={d} providers={provs} cfg={cfg} />
            ))}
          </ul>
        ) : (
          <EmptyState icon={<Waypoints size={22} />} title="No static hostnames yet">
            Your first <code className="font-mono text-[12.5px]">tund http</code> creates one automatically, or pin one
            above. Only your account can use it.
          </EmptyState>
        )}
      </Panel>

      {tcp ? (
        <Panel
          title="Static TCP ports"
          description={
            <>
              Keep the same <span className="font-mono text-[12.5px]">tcp://{tcp.host}:port</span> for SSH, databases and
              other TCP services. They share the static-address budget with static hostnames.
            </>
          }
          actions={
            <Usage
              used={pinned.used}
              limit={pinned.limit}
              noun={pinned.used === 1 ? "static address" : "static addresses"}
              label="Static addresses"
            />
          }
          className="mb-6"
        >
          <div className="border-b border-line p-4">
            <ReserveTcpForm range={tcp} full={staticFull} />
          </div>
          {tcpPorts.length ? (
            <ul className="divide-y divide-line">
              {tcpPorts.map((p) => (
                <TcpPortRow key={p.port} item={p} host={tcp.host} />
              ))}
            </ul>
          ) : (
            <p className="px-4 py-4 text-[13px] text-muted">
              No reserved ports. <code className="font-mono text-[12.5px]">tund tcp 22 --pin</code> also keeps the port it
              gets.
            </p>
          )}
        </Panel>
      ) : null}

      <Panel
        title="Custom domains"
        description="Serve tunnels from a domain you own. Add a single host like api.example.com or a wildcard like *.dev.example.com."
        actions={<Usage used={custom.used} limit={custom.limit} noun={custom.used === 1 ? "domain" : "domains"} />}
      >
        <div className="border-b border-line p-4">
          <AddCustomDomainForm full={customFull} />
        </div>
        {customs.length ? (
          <ul className="divide-y divide-line">
            {customs.map((d) => (
              <DomainRow key={d.id} domain={d} providers={provs} cfg={cfg} />
            ))}
          </ul>
        ) : (
          <EmptyState icon={<Globe size={22} />} title="No custom domains yet">
            Certificates are issued automatically once DNS points at this server.
          </EmptyState>
        )}
      </Panel>

      <Panel
        title="Team domains"
        description="Static hostnames and custom domains owned by your teams. Every member can use them; team owners and admins manage them on the team page."
        className="mt-6"
      >
        {teamDomains.length ? (
          <ul className="divide-y divide-line">
            {teamDomains.map((d) => (
              <li key={d.hostname} className="flex flex-wrap items-center justify-between gap-3 px-4 py-2.5">
                <span className="flex min-w-0 items-center gap-2.5">
                  {d.online ? <span className="live-dot shrink-0" title="A tunnel is online on this name" /> : null}
                  <span className="truncate font-mono text-[13px] text-ink">{d.hostname}</span>
                  <Badge tone="outline">{d.kind === "custom" ? "Custom" : "Static"}</Badge>
                </span>
                <Link href={`/teams/${d.teamSlug}#domains`} className="text-[12.5px] font-medium text-ink-2 hover:text-ink hover:underline">
                  {d.teamName}
                </Link>
              </li>
            ))}
          </ul>
        ) : (
          <p className="px-4 py-4 text-[13px] text-muted">
            None yet. Domains added on a{" "}
            <Link href="/teams" className="font-medium text-ink underline underline-offset-4">
              team
            </Link>{" "}
            page are shared with its members.
          </p>
        )}
      </Panel>
    </>
  );
}

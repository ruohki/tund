import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { Globe, ShieldCheck, Waypoints } from "lucide-react";
import { requireUser } from "@/lib/auth";
import { CUSTOM_DOMAINS_OFF, customDomainsEnabled, passthroughEnabled } from "@/lib/abuse";
import { publicConfig } from "@/lib/config";
import { serverAddresses } from "@/lib/servers";
import { db } from "@/lib/db";
import { loadDomainItems } from "@/lib/domain-items";
import { listTcpReservations, tcpConfig } from "@/lib/tcp";
import { domainUsage } from "@/lib/static-hostnames";
import { atLeast, membershipBySlug, providerOptions, type TeamRole } from "@/lib/teams";
import { formatDateTime, timeAgo } from "@/lib/format";
import { EmptyState, PageHeader, Panel } from "@/components/ui";
import { ProviderForm, ProviderRow, type ProviderItem } from "../../access/provider-forms";
import { AddCustomDomainForm, DomainRow, StaticHostnameForms } from "../../domains/domain-forms";
import { ReserveTcpForm, TcpPortRow } from "../../domains/tcp-ports";
import { AddMemberForm, CreateInviteForm, DeleteTeam, LeaveTeam, MemberRow, RevokeInvite, type MemberItem } from "./team-forms";

export async function generateMetadata({ params }: PageProps<"/teams/[slug]">): Promise<Metadata> {
  return { title: `Team ${(await params).slug}` };
}

function Usage({ used, limit }: { used: number; limit: number | null }) {
  if (limit === null) return null;
  return <span className={`text-[12.5px] tabular ${used >= limit ? "font-medium text-ink" : "text-muted"}`}>{used} of {limit} used</span>;
}

export default async function TeamPage({ params }: PageProps<"/teams/[slug]">) {
  const user = await requireUser();
  const { slug } = await params;
  const m = await membershipBySlug(user.id, slug);
  // Non-members get the same answer as for a team that doesn't exist.
  if (!m) notFound();
  const { team, role } = m;
  const manage = atLeast(role, "admin");
  const cfg = { ...publicConfig(), serverIps: await serverAddresses() };

  const [memberRows, inviteRows, providerRows, domains, options, pinned, custom] = await Promise.all([
    db()`
      select u.id, u.email, u.name, m.role from team_members m join users u on u.id = m.user_id
      where m.team_id = ${team.id}
      order by case m.role when 'owner' then 0 when 'admin' then 1 else 2 end, u.email`,
    manage
      ? db()`
          select i.id, i.email, i.role, i.created_at, i.expires_at, u.email as inviter
          from team_invites i left join users u on u.id = i.invited_by
          where i.team_id = ${team.id} and i.accepted_at is null and i.expires_at > now()
          order by i.created_at desc`
      : Promise.resolve([]),
    db()`
      select p.*, (select count(*) from domains d where d.auth_oidc_provider_id = p.id) as domains
      from oidc_providers p where p.team_id = ${team.id} order by p.created_at`,
    loadDomainItems(user.id, team.id),
    providerOptions(user.id, team.id),
    domainUsage(user, "subdomain", { teamId: team.id }),
    domainUsage(user, "custom", { teamId: team.id }),
  ]);
  const [tcpRange, tcpPorts, customOn, passthroughOn] = await Promise.all([
    tcpConfig(),
    listTcpReservations(user.id, { teamId: team.id }),
    customDomainsEnabled(user),
    passthroughEnabled(user),
  ]);
  // Static TCP ports only for accounts that may open TCP tunnels.
  const tcp = passthroughOn ? tcpRange : null;

  const members: MemberItem[] = memberRows.map((r) => ({
    id: r.id,
    email: r.email,
    name: r.name,
    role: r.role as TeamRole,
    self: r.id === user.id,
  }));
  const providers: ProviderItem[] = providerRows.map((r) => ({
    id: r.id,
    name: r.name,
    slug: r.slug,
    issuer: r.issuer,
    clientId: r.client_id,
    hasSecret: Boolean(r.client_secret),
    scopes: r.scopes,
    domains: Number(r.domains),
  }));
  const provs = options.map((p) => ({ id: p.id, name: p.name, slug: p.slug, ref: p.ref }));
  const statics = domains.filter((d) => d.kind === "subdomain");
  const customs = domains.filter((d) => d.kind === "custom");
  const staticFull =
    pinned.limit !== null && pinned.used >= pinned.limit
      ? `This team uses all ${pinned.limit} static ${pinned.limit === 1 ? "address" : "addresses"} (static hostnames and TCP ports) it can have.`
      : null;
  const customFull = !customOn
    ? CUSTOM_DOMAINS_OFF
    : custom.limit !== null && custom.used >= custom.limit
      ? `This team uses all ${custom.limit} custom ${custom.limit === 1 ? "domain" : "domains"} it can have.`
      : null;
  const callback = `${cfg.dashboardUrl}/_tund/oidc/callback`;

  return (
    <>
      <PageHeader
        title={team.name}
        description={
          <>
            <span className="font-mono text-[13px] text-ink">{team.slug}</span>. You&apos;re{" "}
            {role === "owner" ? "an owner" : role === "admin" ? "an admin" : "a member"}
            {role === "member"
              ? ": you can run tunnels on the team's domains, protect them with its providers and see their traffic."
              : ": you can manage members, invites, providers and domains."}
          </>
        }
      />

      <Panel
        title="Members"
        description={`${members.length} ${members.length === 1 ? "person" : "people"}`}
        className="mb-6"
      >
        <ul className="divide-y divide-line">
          {members.map((mem) => (
            <MemberRow key={mem.id} teamId={team.id} member={mem} myRole={role} />
          ))}
        </ul>
        {manage ? (
          <div className="border-t border-line p-4">
            <AddMemberForm teamId={team.id} myRole={role} />
          </div>
        ) : null}
      </Panel>

      {manage ? (
        <Panel
          title="Invite links"
          description="For people without an account yet, or when you'd rather send a link. Each link works once and expires after 7 days."
          className="mb-6"
        >
          <div className="border-b border-line p-4">
            <CreateInviteForm teamId={team.id} />
          </div>
          {inviteRows.length ? (
            <ul className="divide-y divide-line">
              {inviteRows.map((i) => (
                <li key={i.id} className="flex flex-wrap items-center justify-between gap-3 px-4 py-2.5 text-[13px]">
                  <span className="min-w-0">
                    <span className="text-ink">{i.email || "Anyone with the link"}</span>
                    <span className="ml-2 text-[12px] text-muted">
                      {i.role}, created by {i.inviter ?? "a former member"} {timeAgo(i.created_at)}, expires{" "}
                      {formatDateTime(i.expires_at)}
                    </span>
                  </span>
                  <RevokeInvite teamId={team.id} id={i.id} />
                </li>
              ))}
            </ul>
          ) : (
            <p className="px-4 py-3 text-[13px] text-muted">No pending invites.</p>
          )}
        </Panel>
      ) : null}

      <Panel
        id="providers"
        title="Identity providers"
        description={
          <>
            Shared with every member. Reference them from the CLI as{" "}
            <code className="font-mono text-[12.5px] text-ink-2">--oidc {team.slug}/&lt;provider&gt;</code>; the redirect
            URI to register is <span className="font-mono text-[12.5px] text-ink-2">{callback}</span>.
          </>
        }
        className="mb-6"
      >
        {providers.length ? (
          <ul className="divide-y divide-line">
            {providers.map((p) => (
              <ProviderRow key={p.id} provider={p} teamId={team.id} teamSlug={team.slug} canManage={manage} />
            ))}
          </ul>
        ) : (
          <EmptyState icon={<ShieldCheck size={22} />} title="No team providers yet">
            {manage ? "Add one below to share single sign-on with the team." : "Team owners and admins can add them."}
          </EmptyState>
        )}
        {manage ? (
          <div className="border-t border-line p-4">
            <h3 className="mb-3 text-[14px] font-semibold text-ink">Add a provider</h3>
            <ProviderForm teamId={team.id} teamSlug={team.slug} />
          </div>
        ) : null}
      </Panel>

      <Panel
        id="domains"
        title={
          <>
            Static hostnames <span className="font-mono text-[13px] font-normal text-muted">*.{cfg.baseDomain}</span>
          </>
        }
        description={
          <>
            Any member can run a tunnel on these with <code className="font-mono text-[12.5px]">--subdomain</code>. Team
            hostnames are never anyone&apos;s default.
          </>
        }
        actions={<Usage used={pinned.used} limit={pinned.limit} />}
        className="mb-6"
      >
        {manage ? (
          <div className="border-b border-line p-4">
            <StaticHostnameForms baseDomain={cfg.baseDomain} full={staticFull} teamId={team.id} />
          </div>
        ) : null}
        {statics.length ? (
          <ul className="divide-y divide-line">
            {statics.map((d) => (
              <DomainRow key={d.id} domain={d} providers={provs} cfg={cfg} canManage={manage} />
            ))}
          </ul>
        ) : (
          <EmptyState icon={<Waypoints size={22} />} title="No team hostnames yet" />
        )}
      </Panel>

      {tcp ? (
        <Panel
          title="Static TCP ports"
          description="Team ports any member can use with --remote-port. They count toward the team's static addresses."
          actions={<Usage used={pinned.used} limit={pinned.limit} />}
          className="mb-6"
        >
          {manage ? (
            <div className="border-b border-line p-4">
              <ReserveTcpForm range={tcp} full={staticFull} teamId={team.id} />
            </div>
          ) : null}
          {tcpPorts.length ? (
            <ul className="divide-y divide-line">
              {tcpPorts.map((p) => (
                <TcpPortRow key={p.port} item={p} host={tcp.host} teamId={team.id} canManage={manage} />
              ))}
            </ul>
          ) : (
            <p className="px-4 py-4 text-[13px] text-muted">No team ports yet.</p>
          )}
        </Panel>
      ) : null}

      <Panel
        title="Custom domains"
        description={
          <>
            Domains the team serves tunnels from. Any member can use them with{" "}
            <code className="font-mono text-[12.5px]">--domain</code> once verified.
          </>
        }
        actions={<Usage used={custom.used} limit={custom.limit} />}
        className="mb-6"
      >
        {manage ? (
          <div className="border-b border-line p-4">
            <AddCustomDomainForm full={customFull} teamId={team.id} />
          </div>
        ) : null}
        {customs.length ? (
          <ul className="divide-y divide-line">
            {customs.map((d) => (
              <DomainRow key={d.id} domain={d} providers={provs} cfg={cfg} canManage={manage} />
            ))}
          </ul>
        ) : (
          <EmptyState icon={<Globe size={22} />} title="No team domains yet" />
        )}
      </Panel>

      <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
        <Panel title="Leave team" bodyClassName="p-4">
          <p className="mb-3 text-[13px] text-ink-2">
            You lose access to the team&apos;s domains and providers, and your tunnels on team domains are closed.
            {role === "owner" ? " A team needs at least one owner, so hand over ownership first." : ""}
          </p>
          <LeaveTeam teamId={team.id} />
        </Panel>
        {role === "owner" ? (
          <Panel title="Delete team" bodyClassName="p-4">
            <DeleteTeam teamId={team.id} slug={team.slug} />
          </Panel>
        ) : null}
      </div>
    </>
  );
}

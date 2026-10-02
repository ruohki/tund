import type { Metadata } from "next";
import Link from "next/link";
import { notFound } from "next/navigation";
import { ArrowLeft } from "lucide-react";
import { requireAdmin } from "@/lib/auth";
import { db } from "@/lib/db";
import { formatBytes, formatDateTime, formatKbps, formatNumber, formatTransfer, osLabel, sessionLength, timeAgo } from "@/lib/format";
import { effectiveLimits, monthUsage } from "@/lib/usage";
import { getSettings } from "@/lib/settings";
import { LimitsForm } from "./limits-form";
import { FeatureForm } from "./feature-form";
import { identitiesOf } from "@/lib/oauth";
import { smtpConfigured } from "@/lib/mail";
import { listTunnels } from "@/lib/metrics";
import { isUuid } from "@/lib/requests";
import { AuthBadge, Badge, PageHeader, Panel } from "@/components/ui";
import { ConfirmSubmit } from "@/components/client-ui";
import { deleteUserAction } from "@/app/actions/admin";
import { FlagSwitch, ResetPasswordButton, StopTunnelForm } from "../../admin-forms";
import { FlagUserForm } from "../../abuse/abuse-forms";
import { ApprovalBadge } from "../../domains/approval-badge";

export const metadata: Metadata = { title: "User" };

export default async function AdminUserPage({ params }: PageProps<"/admin/users/[id]">) {
  const admin = await requireAdmin();
  const { id } = await params;
  if (!isUuid(id)) notFound();
  const sql = db();
  const [u] = await sql`select * from users where id = ${id}`;
  if (!u) notFound();

  const [tunnels, domains, teams, [counts], mailOn, usage, settings] = await Promise.all([
    listTunnels(id, { limit: 20 }),
    sql`select id, hostname, kind, team_id, verified_at, auth_mode, is_default, approval from domains where user_id = ${id} and team_id is null order by kind desc, hostname`,
    sql`
      select t.slug, t.name, m.role, (select count(*)::int from team_members x where x.team_id = t.id) as members
      from team_members m join teams t on t.id = m.team_id where m.user_id = ${id} order by t.name`,
    sql`
      select
        (select count(*)::int from authtokens where user_id = ${id}) as tokens,
        (select count(*)::int from sessions where user_id = ${id} and expires_at > now()) as sessions,
        (select count(*)::int from requests where user_id = ${id} and started_at > now() - interval '24 hours') as req24,
        (select count(*)::int from requests where user_id = ${id} and started_at > now() - interval '7 days') as req7d,
        (select coalesce(sum(req_body_size + resp_body_size), 0)::bigint from requests
          where user_id = ${id} and started_at > now() - interval '7 days') as bytes7d,
        (select count(*)::int from agent_sessions where user_id = ${id}) as client_sessions`,
    smtpConfigured(),
    monthUsage(id),
    getSettings(),
  ]);
  const identities = await identitiesOf(id);
  const limits = await effectiveLimits({ id, isAdmin: Boolean(u.is_admin) });
  const monthBytes = usage.bytesIn + usage.bytesOut;
  const self = u.id === admin.id;
  const online = tunnels.filter((t) => !t.endedAt);
  const recent = tunnels.filter((t) => t.endedAt).slice(0, 10);

  return (
    <>
      <Link href="/admin/users" className="mb-3 inline-flex items-center gap-1 text-[13px] text-muted hover:text-ink">
        <ArrowLeft size={14} /> Users
      </Link>
      <PageHeader
        title={u.name || u.email}
        description={
          <span className="flex flex-wrap items-center gap-2">
            <span>{u.email}</span>
            {u.is_admin ? <Badge tone="outline">Admin</Badge> : null}
            {u.disabled_at ? <Badge tone="danger">Disabled</Badge> : null}
            {u.flagged_at ? <Badge tone="danger">Flagged</Badge> : null}
            {u.email_verified_at ? <Badge tone="ok">Verified</Badge> : <Badge tone="live">Unverified</Badge>}
            {identities.map((i) => (
              <Badge key={i.provider} tone="outline">
                <span title={i.email}>{i.label}</span>
              </Badge>
            ))}
            {u.password_hash === null ? <Badge tone="outline">No password</Badge> : null}
            <span className="text-muted">joined {formatDateTime(u.created_at)}</span>
          </span>
        }
      />

      <div className="mb-6 grid grid-cols-1 gap-6 lg:grid-cols-[minmax(0,1fr)_minmax(0,1.4fr)]">
        <Panel title="Account" bodyClassName="divide-y divide-line px-4">
          <FlagSwitch
            id={u.id}
            flag="disabled"
            on={Boolean(u.disabled_at)}
            label="Disabled"
            help="Blocks sign-in and the CLI; signs the user out and disconnects their clients. Nothing is deleted."
            disabled={self}
          />
          <FlagSwitch
            id={u.id}
            flag="admin"
            on={Boolean(u.is_admin)}
            label="Administrator"
            help="Full access to this admin area."
            disabled={self}
          />
          <FlagSwitch
            id={u.id}
            flag="trusted"
            on={Boolean(u.trusted) || Boolean(u.is_admin)}
            label="Trusted"
            help={u.is_admin ? "Admins are always trusted." : "Their tunnels skip the browser warning page."}
            disabled={Boolean(u.is_admin)}
          />
          {u.flagged_at ? (
            <FlagSwitch
              id={u.id}
              flag="flagged"
              on
              label="Flagged"
              help={`${u.flag_reason || "No reason given"} (${formatDateTime(u.flagged_at)}). A note for admins; it doesn't restrict the account.`}
            />
          ) : (
            <div className="py-3">
              <p className="mb-2 text-[13.5px] font-medium text-ink">Flag for review</p>
              <FlagUserForm id={u.id} />
            </div>
          )}
          <FlagSwitch
            id={u.id}
            flag="verified"
            on={Boolean(u.email_verified_at)}
            label="Email verified"
            help={u.email_verified_at ? `Since ${formatDateTime(u.email_verified_at)}.` : "Mark the address as confirmed without the email link."}
            disabled={Boolean(u.email_verified_at)}
          />
          <div className="flex flex-col gap-3 py-3">
            <ResetPasswordButton id={u.id} disabled={!mailOn} />
            {!mailOn ? <p className="text-[12.5px] text-muted">Needs email (Admin → Email).</p> : null}
            {!self ? (
              <form action={deleteUserAction}>
                <input type="hidden" name="id" value={u.id} />
                <input type="hidden" name="redirect" value="list" />
                <ConfirmSubmit confirmText="Delete account and all its data?">Delete account</ConfirmSubmit>
              </form>
            ) : null}
          </div>
        </Panel>

        <Panel title="Usage">
          <dl className="grid grid-cols-2 gap-x-6 gap-y-3 p-4 text-[13px] sm:grid-cols-3">
            {(
              [
                ["Transfer this month", `${formatTransfer(monthBytes)}${limits.transferGb ? ` of ${limits.transferGb} GB` : ""}`],
                ["Speed limit", formatKbps(limits.bandwidthKbps)],
                ["Requests, 24h", formatNumber(counts.req24)],
                ["Requests, 7 days", formatNumber(counts.req7d)],
                ["Captured bodies, 7 days", formatBytes(Number(counts.bytes7d))],
                ["Auth tokens", formatNumber(counts.tokens)],
                ["Dashboard sessions", formatNumber(counts.sessions)],
                ["Client connections", formatNumber(counts.client_sessions)],
              ] as const
            ).map(([k, v]) => (
              <div key={k}>
                <dt className="text-[12px] text-muted">{k}</dt>
                <dd className="text-[15px] font-medium text-ink">{v}</dd>
              </div>
            ))}
          </dl>
          <h3 className="border-y border-line bg-surface-2 px-4 py-1.5 text-[12px] font-semibold text-ink-2">Teams</h3>
          {teams.length ? (
            <ul className="divide-y divide-line">
              {teams.map((t) => (
                <li key={t.slug} className="flex items-center justify-between gap-3 px-4 py-2 text-[13px]">
                  <span className="text-ink">
                    {t.name} <span className="font-mono text-[12px] text-muted">{t.slug}</span>
                  </span>
                  <span className="text-[12px] text-muted">
                    {t.role}, {t.members} {t.members === 1 ? "member" : "members"}
                  </span>
                </li>
              ))}
            </ul>
          ) : (
            <p className="px-4 py-3 text-[13px] text-muted">Not in any team.</p>
          )}
        </Panel>
      </div>

      <Panel
        title="Bandwidth"
        description={u.is_admin ? "Admins are unlimited unless you set a value here." : "Overrides the instance defaults for this account."}
        className="mb-6"
        bodyClassName="p-4"
      >
        <LimitsForm
          id={u.id}
          bandwidth={u.bandwidth_kbps ?? null}
          transfer={u.transfer_quota_gb ?? null}
          defaultBandwidth={u.is_admin ? "unlimited for admins" : formatKbps(settings.limit_bandwidth_kbps)}
          defaultTransfer={u.is_admin ? "unlimited for admins" : settings.limit_transfer_gb ? `${settings.limit_transfer_gb} GB` : "unlimited"}
        />
      </Panel>

      <Panel title="Tunnels" description={`${online.length} online`} className="mb-6">
        <div className="border-b border-line p-4">
          <p className="mb-2 text-[13.5px] font-medium text-ink">TCP and TLS tunnels</p>
          <FeatureForm
            id={u.id}
            feature="passthrough"
            label="TCP and TLS tunnels"
            value={u.passthrough ?? null}
            inheritLabel={u.is_admin ? "on for admins" : settings.passthrough ? "on" : "off"}
          />
        </div>
        {online.length || recent.length ? (
          <ul className="divide-y divide-line">
            {[...online, ...recent].map((t) => (
              <li key={t.id} className="flex flex-wrap items-center justify-between gap-3 px-4 py-2.5">
                <span className="min-w-0">
                  <span className="flex items-center gap-2">
                    {!t.endedAt ? <span className="live-dot shrink-0" aria-hidden /> : null}
                    <span className="truncate font-mono text-[13px] text-ink">{t.hostname}</span>
                    <AuthBadge mode={t.authMode} />
                  </span>
                  <span className="block text-[12px] text-muted">
                    {t.localAddr} on {t.client.hostname || osLabel(t.client.os)},{" "}
                    {t.endedAt ? `ended ${timeAgo(t.endedAt)}` : `for ${sessionLength(t.startedAt, null)}`}
                  </span>
                </span>
                {!t.endedAt ? <StopTunnelForm id={t.id} /> : null}
              </li>
            ))}
          </ul>
        ) : (
          <p className="px-4 py-3 text-[13px] text-muted">No tunnels yet.</p>
        )}
      </Panel>

      <Panel title="Static hostnames and custom domains">
        <div className="border-b border-line p-4">
          <p className="mb-2 text-[13.5px] font-medium text-ink">Custom domains</p>
          <FeatureForm
            id={u.id}
            feature="custom_domains"
            label="Custom domains"
            value={u.custom_domains ?? null}
            inheritLabel={u.is_admin ? "on for admins" : settings.custom_domains ? "on" : "off"}
          />
        </div>
        {domains.length ? (
          <ul className="divide-y divide-line">
            {domains.map((d) => (
              <li key={d.id} className="flex flex-wrap items-center justify-between gap-3 px-4 py-2 text-[13px]">
                <span className="flex items-center gap-2">
                  <span className="font-mono text-ink">{d.hostname}</span>
                  <Badge tone="outline">{d.kind === "custom" ? "Custom" : d.is_default ? "Static, default" : "Static"}</Badge>
                  {d.kind === "custom" && !d.verified_at ? <Badge tone="live">Unverified</Badge> : null}
                  {d.kind === "custom" && d.approval !== "approved" ? <ApprovalBadge approval={d.approval} /> : null}
                </span>
                <AuthBadge mode={d.auth_mode} />
              </li>
            ))}
          </ul>
        ) : (
          <p className="px-4 py-3 text-[13px] text-muted">None.</p>
        )}
      </Panel>
    </>
  );
}

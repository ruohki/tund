import type { Metadata } from "next";
import Link from "next/link";
import { notFound } from "next/navigation";
import { ArrowLeft, ExternalLink } from "lucide-react";
import { requireAdmin } from "@/lib/auth";
import { db } from "@/lib/db";
import { formatDateTime, timeAgo } from "@/lib/format";
import { isUuid } from "@/lib/requests";
import { categoryLabel, sourceLabel } from "@/lib/abuse-shared";
import type { DomainRisk } from "@/lib/abuse";
import { Badge, PageHeader, Panel } from "@/components/ui";
import { ConfirmSubmit } from "@/components/client-ui";
import { unblockHostAction } from "@/app/actions/abuse";
import { FlagSwitch, StopTunnelForm } from "../../admin-forms";
import { BlockHostForm, FlagUserForm, ReportStatusForm } from "../abuse-forms";
import { DomainReview } from "../../domains/domain-review";
import { StatusBadge } from "../status-badge";

export const metadata: Metadata = { title: "Abuse report" };

export default async function AbuseReportPage({ params }: PageProps<"/admin/abuse/[id]">) {
  await requireAdmin();
  const { id } = await params;
  if (!isUuid(id)) notFound();
  const sql = db();
  const [r] = await sql`
    select r.*, u.email, u.is_admin, u.trusted, u.disabled_at, u.flagged_at, u.flag_reason, rb.email as resolved_by_email
    from abuse_reports r left join users u on u.id = r.user_id left join users rb on rb.id = r.resolved_by
    where r.id = ${id}`;
  if (!r) notFound();
  const details = (r.details ?? {}) as Record<string, unknown>;
  const [online, [blocked], others, [domain]] = await Promise.all([
    sql`
      select t.id, t.local_addr, t.started_at, u.email from tunnels t join users u on u.id = t.user_id
      where t.hostname = ${r.hostname} and t.ended_at is null`,
    sql`select reason, created_at from blocked_hosts where hostname = ${r.hostname}`,
    sql`
      select id, category, source, status, created_at from abuse_reports
      where hostname = ${r.hostname} and id <> ${id} order by created_at desc limit 20`,
    details.kind === "domain_review" && typeof details.domain_id === "string" && isUuid(details.domain_id)
      ? sql`select id, hostname, approval, verified_at, risk, registered_at from domains where id = ${details.domain_id}`
      : Promise.resolve([]),
  ]);

  const rows: [string, React.ReactNode][] = [
    ["Category", details.kind === "domain_review" ? "Custom domain review" : categoryLabel(r.category)],
    ["Source", sourceLabel(r.source)],
    ["Reported", `${formatDateTime(r.created_at)} (${timeAgo(r.created_at)})`],
    [
      "URL",
      r.url ? (
        <span className="break-all font-mono text-[12.5px]">{r.url}</span>
      ) : (
        <span className="text-muted">not given</span>
      ),
    ],
  ];
  if (r.source === "form") {
    rows.push(["Reporter", r.reporter_email || <span className="text-muted">anonymous</span>]);
    if (r.reporter_ip) rows.push(["Reporter IP", <span key="ip" className="font-mono text-[12.5px]">{r.reporter_ip}</span>]);
  }
  if (typeof details.threat_type === "string") rows.push(["Threat type", details.threat_type]);
  if (typeof details.score === "number") rows.push(["Heuristic score", String(details.score)]);
  if (Array.isArray(details.signals)) rows.push(["Signals", (details.signals as unknown[]).map(String).join(", ")]);
  if (typeof details.request_id === "string" && isUuid(details.request_id)) {
    rows.push(["Captured request", <span key="req" className="font-mono text-[12.5px]">{details.request_id}</span>]);
  }

  return (
    <>
      <Link href="/admin/abuse" className="mb-3 inline-flex items-center gap-1 text-[13px] text-muted hover:text-ink">
        <ArrowLeft size={14} /> Abuse
      </Link>
      <PageHeader
        title={<span className="break-all font-mono text-[22px]">{r.hostname}</span>}
        description={
          <span className="flex flex-wrap items-center gap-2">
            <StatusBadge status={r.status} />
            {blocked ? <Badge tone="danger">Blocked</Badge> : null}
            {online.length ? <Badge tone="live">Online</Badge> : <Badge>Offline</Badge>}
          </span>
        }
      />

      <div className="grid grid-cols-1 gap-6 lg:grid-cols-[minmax(0,1.3fr)_minmax(0,1fr)]">
        <div className="flex flex-col gap-6">
          <Panel title="Report">
            <dl className="divide-y divide-line text-[13px]">
              {rows.map(([k, v]) => (
                <div key={k} className="grid grid-cols-[9rem_minmax(0,1fr)] gap-3 px-4 py-2">
                  <dt className="text-muted">{k}</dt>
                  <dd className="text-ink">{v}</dd>
                </div>
              ))}
            </dl>
            <div className="border-t border-line px-4 py-3">
              <p className="mb-1 text-[12px] text-muted">Description</p>
              <p className="whitespace-pre-wrap break-words text-[13.5px] text-ink">{r.description || "—"}</p>
            </div>
          </Panel>

          {domain ? (
            <Panel title="Custom domain review" description="The edge only serves the domain once it's approved.">
              <DomainReview
                domain={{
                  id: domain.id,
                  hostname: domain.hostname,
                  approval: domain.approval,
                  verified: Boolean(domain.verified_at),
                  risk: domain.risk as DomainRisk,
                }}
              />
            </Panel>
          ) : null}

          <Panel title={r.status === "open" ? "Close the report" : "Outcome"} bodyClassName="p-4">
            {r.status !== "open" ? (
              <p className="mb-3 text-[13px] text-ink-2">
                {r.status === "resolved" ? "Resolved" : "Dismissed"} {r.resolved_by_email ? `by ${r.resolved_by_email}` : ""}{" "}
                {r.resolved_at ? timeAgo(r.resolved_at) : ""}
                {r.resolution ? <span className="mt-1 block whitespace-pre-wrap text-ink">{r.resolution}</span> : null}
              </p>
            ) : null}
            <ReportStatusForm id={r.id} status={r.status} />
          </Panel>

          {others.length ? (
            <Panel title="Other reports for this hostname">
              <ul className="divide-y divide-line">
                {others.map((o) => (
                  <li key={o.id}>
                    <Link href={`/admin/abuse/${o.id}`} className="flex items-center justify-between gap-3 px-4 py-2 text-[13px] hover:bg-surface-2">
                      <span className="text-ink-2">
                        {categoryLabel(o.category)} · {sourceLabel(o.source)}
                      </span>
                      <span className="flex items-center gap-2 text-muted">
                        <StatusBadge status={o.status} /> {timeAgo(o.created_at)}
                      </span>
                    </Link>
                  </li>
                ))}
              </ul>
            </Panel>
          ) : null}
        </div>

        <div className="flex flex-col gap-6">
          <Panel title="Tunnel" bodyClassName="p-4">
            {online.length ? (
              <ul className="flex flex-col gap-3">
                {online.map((t) => (
                  <li key={t.id} className="flex flex-col gap-2 text-[13px]">
                    <span className="text-ink-2">
                      Online for {timeAgo(t.started_at).replace(/ ago$/, "")}, forwarding to{" "}
                      <span className="font-mono text-[12.5px] text-ink">{t.local_addr}</span> ({t.email})
                    </span>
                    <StopTunnelForm id={t.id} />
                  </li>
                ))}
              </ul>
            ) : (
              <p className="text-[13px] text-muted">No tunnel is online on this hostname.</p>
            )}
          </Panel>

          <Panel title="Hostname" bodyClassName="p-4">
            {blocked ? (
              <div className="flex flex-wrap items-center justify-between gap-3 text-[13px]">
                <span className="text-ink-2">
                  Blocked {timeAgo(blocked.created_at)}
                  {blocked.reason ? `: ${blocked.reason}` : ""}
                </span>
                <form action={unblockHostAction}>
                  <input type="hidden" name="hostname" value={r.hostname} />
                  <ConfirmSubmit variant="secondary" confirmText="Unblock?">
                    Unblock
                  </ConfirmSubmit>
                </form>
              </div>
            ) : (
              <>
                <p className="mb-2 text-[12.5px] text-muted">Stops any tunnel on it and refuses future binds.</p>
                <BlockHostForm hostname={r.hostname} reportId={r.id} />
              </>
            )}
          </Panel>

          <Panel title="Account" bodyClassName="px-4">
            {r.user_id ? (
              <div className="divide-y divide-line">
                <p className="flex flex-wrap items-center gap-2 py-3 text-[13px]">
                  <Link href={`/admin/users/${r.user_id}`} className="inline-flex items-center gap-1 font-medium text-ink hover:underline">
                    {r.email} <ExternalLink size={12} />
                  </Link>
                  {r.is_admin ? <Badge tone="outline">Admin</Badge> : null}
                </p>
                <FlagSwitch
                  id={r.user_id}
                  flag="disabled"
                  on={Boolean(r.disabled_at)}
                  label="Disabled"
                  help="Signs them out and disconnects their clients."
                  disabled={Boolean(r.is_admin)}
                />
                <FlagSwitch
                  id={r.user_id}
                  flag="trusted"
                  on={Boolean(r.trusted) || Boolean(r.is_admin)}
                  label="Trusted"
                  help="Skips the browser warning; custom domains without review."
                  disabled={Boolean(r.is_admin)}
                />
                {r.flagged_at ? (
                  <FlagSwitch
                    id={r.user_id}
                    flag="flagged"
                    on
                    label="Flagged"
                    help={`${r.flag_reason || "No reason"} (${timeAgo(r.flagged_at)})`}
                  />
                ) : (
                  <div className="py-3">
                    <FlagUserForm id={r.user_id} />
                  </div>
                )}
              </div>
            ) : (
              <p className="py-3 text-[13px] text-muted">No account is linked to this hostname.</p>
            )}
          </Panel>
        </div>
      </div>
    </>
  );
}

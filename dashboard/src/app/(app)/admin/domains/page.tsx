import type { Metadata } from "next";
import Link from "next/link";
import { Search } from "lucide-react";
import { db } from "@/lib/db";
import { timeAgo } from "@/lib/format";
import { AuthBadge, Badge, buttonClass, cn, inputClass, Panel } from "@/components/ui";
import { ConfirmSubmit } from "@/components/client-ui";
import { adminDeleteDomainAction } from "@/app/actions/admin";
import type { DomainRisk } from "@/lib/abuse";
import { ApprovalBadge } from "./approval-badge";
import { DomainReview } from "./domain-review";

export const metadata: Metadata = { title: "All domains" };

export default async function AdminDomainsPage({ searchParams }: PageProps<"/admin/domains">) {
  const sp = await searchParams;
  const q = typeof sp.q === "string" ? sp.q.trim().toLowerCase().slice(0, 100) : "";
  const approval = sp.approval === "pending" || sp.approval === "rejected" ? sp.approval : "";
  const sql = db();
  const [rows, pending] = await Promise.all([
    sql`
      select d.id, d.hostname, d.kind, d.verified_at, d.auth_mode, d.is_default, d.created_at, d.approval,
        u.id as user_id, u.email, t.slug as team_slug,
        exists(select 1 from tunnels x where x.ended_at is null and x.hostname = d.hostname) as online
      from domains d join users u on u.id = d.user_id left join teams t on t.id = d.team_id
      where true
      ${q ? sql`and (d.hostname like ${"%" + q.replace(/[\\%_]/g, (c) => "\\" + c) + "%"} or u.email ilike ${"%" + q + "%"})` : sql``}
      ${approval ? sql`and d.approval = ${approval}` : sql``}
      order by d.created_at desc limit 1000`,
    sql`
      select d.id, d.hostname, d.approval, d.verified_at, d.risk, d.created_at, u.id as user_id, u.email, t.slug as team_slug
      from domains d join users u on u.id = d.user_id left join teams t on t.id = d.team_id
      where d.approval = 'pending' order by d.created_at limit 50`,
  ]);
  return (
    <>
    {pending.length ? (
      <Panel
        title="Awaiting review"
        description="Custom domains of non-trusted accounts. The edge serves them only after approval."
        className="mb-6"
      >
        <ul className="divide-y divide-line">
          {pending.map((d) => (
            <li key={d.id} className="grid gap-2 md:grid-cols-[minmax(0,14rem)_minmax(0,1fr)]">
              <div className="px-4 pt-4 text-[13px] md:pb-4">
                <Link href={`/admin/users/${d.user_id}`} className="block font-medium text-ink hover:underline">
                  {d.email}
                </Link>
                <span className="block text-[12px] text-muted">
                  {d.team_slug ? `for team ${d.team_slug}, ` : ""}added {timeAgo(d.created_at)}
                </span>
              </div>
              <DomainReview
                domain={{ id: d.id, hostname: d.hostname, approval: d.approval, verified: Boolean(d.verified_at), risk: d.risk as DomainRisk }}
              />
            </li>
          ))}
        </ul>
      </Panel>
    ) : null}
    <Panel title="Domains" description="Static hostnames and custom domains across all accounts and teams.">
      <form method="get" className="flex flex-wrap items-center gap-2 border-b border-line px-4 py-3">
        <label className="relative min-w-52 flex-1 sm:max-w-sm">
          <span className="sr-only">Search domains</span>
          <Search size={14} className="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 text-muted" />
          <input name="q" defaultValue={q} placeholder="Hostname or owner email" className={cn(inputClass, "h-8 pl-8")} />
        </label>
        <select name="approval" defaultValue={approval} aria-label="Approval" className={cn(inputClass, "h-8 w-auto")}>
          <option value="">Any approval</option>
          <option value="pending">Pending review</option>
          <option value="rejected">Rejected</option>
        </select>
        <button type="submit" className={buttonClass("secondary", "sm")}>
          Filter
        </button>
        <span className="ml-auto text-[12.5px] text-muted tabular">{rows.length} shown</span>
      </form>
      <div className="overflow-x-auto scroll-thin">
        <table className="w-full min-w-[820px] text-[13px]">
          <thead>
            <tr className="border-b border-line text-left text-[12px] text-muted">
              <th className="px-4 py-2 font-medium">Hostname</th>
              <th className="px-4 py-2 font-medium">Kind</th>
              <th className="px-4 py-2 font-medium">Owner</th>
              <th className="px-4 py-2 font-medium">Access</th>
              <th className="px-4 py-2 font-medium">Added</th>
              <th className="px-4 py-2" />
            </tr>
          </thead>
          <tbody className="divide-y divide-line">
            {rows.map((d) => (
              <tr key={d.id}>
                <td className="px-4 py-2.5">
                  <span className="flex items-center gap-2">
                    {d.online ? <span className="live-dot shrink-0" title="A tunnel is online on it" /> : null}
                    <span className="font-mono text-[12.5px] text-ink">{d.hostname}</span>
                  </span>
                </td>
                <td className="px-4 py-2.5">
                  <span className="flex flex-wrap gap-1.5">
                    <Badge tone="outline">{d.kind === "custom" ? "Custom" : "Static"}</Badge>
                    {d.kind === "custom" && !d.verified_at ? <Badge tone="live">Unverified</Badge> : null}
                    {d.kind === "custom" && d.approval !== "approved" ? <ApprovalBadge approval={d.approval} /> : null}
                    {d.is_default ? <Badge>Default</Badge> : null}
                  </span>
                </td>
                <td className="px-4 py-2.5">
                  {d.team_slug ? <span className="text-ink">Team {d.team_slug}</span> : null}
                  <Link href={`/admin/users/${d.user_id}`} className="block text-[12.5px] text-ink-2 hover:underline">
                    {d.team_slug ? `added by ${d.email}` : d.email}
                  </Link>
                </td>
                <td className="px-4 py-2.5">
                  <AuthBadge mode={d.auth_mode} />
                </td>
                <td className="px-4 py-2.5 text-ink-2">{timeAgo(d.created_at)}</td>
                <td className="px-4 py-2 text-right">
                  <form action={adminDeleteDomainAction}>
                    <input type="hidden" name="id" value={d.id} />
                    <ConfirmSubmit confirmText="Delete it?">Delete</ConfirmSubmit>
                  </form>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        {!rows.length ? <p className="px-4 py-6 text-[13px] text-muted">No domains match.</p> : null}
      </div>
    </Panel>
    </>
  );
}

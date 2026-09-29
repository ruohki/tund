import type { Metadata } from "next";
import Link from "next/link";
import { db } from "@/lib/db";
import { formatDateTime, timeAgo } from "@/lib/format";
import { categoryLabel, REPORT_CATEGORIES, REPORT_SOURCES, sourceLabel } from "@/lib/abuse-shared";
import { Badge, cn, Panel } from "@/components/ui";
import { ConfirmSubmit } from "@/components/client-ui";
import { unblockHostAction } from "@/app/actions/abuse";
import { BlockHostForm } from "./abuse-forms";
import { StatusBadge } from "./status-badge";

export const metadata: Metadata = { title: "Abuse" };

const STATUSES = [
  { id: "open", label: "Open" },
  { id: "resolved", label: "Resolved" },
  { id: "dismissed", label: "Dismissed" },
  { id: "all", label: "All" },
] as const;

export default async function AbusePage({ searchParams }: PageProps<"/admin/abuse">) {
  const sp = await searchParams;
  const status = STATUSES.some((s) => s.id === sp.status) ? (sp.status as string) : "open";
  const source = REPORT_SOURCES.some((s) => s.value === sp.source) ? (sp.source as string) : "";
  const category = REPORT_CATEGORIES.some((c) => c.value === sp.category) ? (sp.category as string) : "";
  const sql = db();
  const [reports, blocked, [counts]] = await Promise.all([
    sql`
      select r.id, r.hostname, r.category, r.source, r.status, r.created_at, r.description, r.details,
        u.id as user_id, u.email, u.flagged_at, u.disabled_at,
        exists(select 1 from tunnels t where t.hostname = r.hostname and t.ended_at is null) as online,
        exists(select 1 from blocked_hosts b where b.hostname = r.hostname) as blocked
      from abuse_reports r left join users u on u.id = r.user_id
      where true
        ${status !== "all" ? sql`and r.status = ${status}` : sql``}
        ${source ? sql`and r.source = ${source}` : sql``}
        ${category ? sql`and r.category = ${category}` : sql``}
      order by r.created_at desc limit 500`,
    sql`
      select b.hostname, b.reason, b.created_at, u.email as created_by
      from blocked_hosts b left join users u on u.id = b.created_by order by b.created_at desc limit 500`,
    sql`
      select count(*) filter (where status = 'open')::int as open,
        (select count(*)::int from domains where approval = 'pending') as pending_domains
      from abuse_reports`,
  ]);
  const href = (p: Record<string, string>) => {
    const q = new URLSearchParams({ status, source, category, ...p });
    for (const [k, v] of [...q]) if (!v || (k === "status" && v === "open")) q.delete(k);
    return `/admin/abuse${q.size ? `?${q}` : ""}`;
  };
  const chip = (active: boolean) =>
    cn("flex h-full items-center rounded-[3px] px-2.5 text-[12.5px]", active ? "bg-surface-3 font-medium text-ink" : "text-muted hover:text-ink");

  return (
    <>
      <Panel
        title="Reports"
        description={
          <>
            {counts.open} open. Reports come from the public{" "}
            <Link href="/report" className="underline underline-offset-4">
              report form
            </Link>
            , Safe Browsing, the phishing heuristics and custom domain reviews
            {counts.pending_domains ? (
              <>
                {" "}
                (
                <Link href="/admin/domains?approval=pending" className="underline underline-offset-4">
                  {counts.pending_domains} {counts.pending_domains === 1 ? "domain" : "domains"} awaiting review
                </Link>
                )
              </>
            ) : null}
            .
          </>
        }
        className="mb-6"
      >
        <div className="flex flex-wrap items-center gap-2 border-b border-line px-4 py-3">
          <div role="group" aria-label="Status" className="inline-flex h-8 items-center rounded-[5px] border border-line-strong p-0.5">
            {STATUSES.map((s) => (
              <Link key={s.id} href={href({ status: s.id })} aria-current={status === s.id ? "true" : undefined} className={chip(status === s.id)}>
                {s.label}
              </Link>
            ))}
          </div>
          <div role="group" aria-label="Source" className="inline-flex h-8 items-center rounded-[5px] border border-line-strong p-0.5">
            <Link href={href({ source: "" })} className={chip(!source)}>
              Any source
            </Link>
            {REPORT_SOURCES.map((s) => (
              <Link key={s.value} href={href({ source: s.value })} className={chip(source === s.value)}>
                {s.label}
              </Link>
            ))}
          </div>
          <div role="group" aria-label="Category" className="flex flex-wrap gap-1">
            {[{ value: "", label: "Any category" }, ...REPORT_CATEGORIES].map((c) => (
              <Link
                key={c.value}
                href={href({ category: c.value })}
                className={cn(
                  "rounded-[4px] border px-2 py-1 text-[12px]",
                  category === c.value ? "border-ink text-ink" : "border-line text-muted hover:text-ink",
                )}
              >
                {c.label}
              </Link>
            ))}
          </div>
        </div>
        <div className="overflow-x-auto scroll-thin">
          <table className="w-full min-w-[860px] text-[13px]">
            <thead>
              <tr className="border-b border-line text-left text-[12px] text-muted">
                <th className="px-4 py-2 font-medium">Hostname</th>
                <th className="px-4 py-2 font-medium">Category</th>
                <th className="px-4 py-2 font-medium">Source</th>
                <th className="px-4 py-2 font-medium">Account</th>
                <th className="px-4 py-2 font-medium">Status</th>
                <th className="px-4 py-2 font-medium">Reported</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-line">
              {reports.map((r) => (
                <tr key={r.id} className="hover:bg-surface-2">
                  <td className="max-w-[320px] px-4 py-2.5">
                    <Link href={`/admin/abuse/${r.id}`} className="block">
                      <span className="flex items-center gap-2">
                        {r.online ? <span className="live-dot shrink-0" title="A tunnel is online on it" /> : null}
                        <span className="truncate font-mono text-[12.5px] text-ink hover:underline">{r.hostname}</span>
                        {r.blocked ? <Badge tone="danger">Blocked</Badge> : null}
                      </span>
                      <span className="block truncate text-[12px] text-muted">{r.description}</span>
                    </Link>
                  </td>
                  <td className="px-4 py-2.5 text-ink-2">
                    {r.details?.kind === "domain_review" ? "Domain review" : categoryLabel(r.category)}
                  </td>
                  <td className="px-4 py-2.5 text-ink-2">{sourceLabel(r.source)}</td>
                  <td className="px-4 py-2.5">
                    {r.user_id ? (
                      <Link href={`/admin/users/${r.user_id}`} className="flex flex-wrap items-center gap-1.5 text-ink-2 hover:underline">
                        {r.email}
                        {r.flagged_at ? <Badge tone="danger">Flagged</Badge> : null}
                        {r.disabled_at ? <Badge tone="danger">Disabled</Badge> : null}
                      </Link>
                    ) : (
                      <span className="text-muted">unknown</span>
                    )}
                  </td>
                  <td className="px-4 py-2.5">
                    <StatusBadge status={r.status} />
                  </td>
                  <td className="px-4 py-2.5 text-ink-2" title={formatDateTime(r.created_at)}>
                    {timeAgo(r.created_at)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          {!reports.length ? <p className="px-4 py-6 text-[13px] text-muted">No reports match.</p> : null}
        </div>
      </Panel>

      <Panel
        title="Blocked hostnames"
        description="Nobody can bind these; visitors get “451 This site has been blocked”. Blocking an online hostname stops its tunnel."
      >
        <div className="border-b border-line px-4 py-3">
          <BlockHostForm />
        </div>
        {blocked.length ? (
          <ul className="divide-y divide-line">
            {blocked.map((b) => (
              <li key={b.hostname} className="flex flex-wrap items-center justify-between gap-3 px-4 py-2.5 text-[13px]">
                <span className="min-w-0">
                  <span className="block font-mono text-[12.5px] text-ink">{b.hostname}</span>
                  <span className="block text-[12px] text-muted">
                    {b.reason || "no reason"} · {b.created_by ? `by ${b.created_by}` : "automatic"}, {timeAgo(b.created_at)}
                  </span>
                </span>
                <form action={unblockHostAction}>
                  <input type="hidden" name="hostname" value={b.hostname} />
                  <ConfirmSubmit variant="secondary" confirmText="Unblock?">
                    Unblock
                  </ConfirmSubmit>
                </form>
              </li>
            ))}
          </ul>
        ) : (
          <p className="px-4 py-3 text-[13px] text-muted">No blocked hostnames.</p>
        )}
      </Panel>
    </>
  );
}

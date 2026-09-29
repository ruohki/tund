import type { Metadata } from "next";
import Link from "next/link";
import { db } from "@/lib/db";
import { formatDateTime } from "@/lib/format";
import { buttonClass, cn, inputClass, Panel, Select } from "@/components/ui";

export const metadata: Metadata = { title: "Audit log" };

const PAGE = 50;

function detailText(d: Record<string, unknown>): string {
  return Object.entries(d)
    .map(([k, v]) => `${k}: ${Array.isArray(v) ? v.join(", ") : typeof v === "object" ? JSON.stringify(v) : String(v)}`)
    .join("; ");
}

export default async function AuditPage({ searchParams }: PageProps<"/admin/audit">) {
  const sp = await searchParams;
  const action = typeof sp.action === "string" ? sp.action.slice(0, 60) : "";
  const actor = typeof sp.actor === "string" ? sp.actor.trim().slice(0, 100) : "";
  const before = typeof sp.before === "string" && /^\d+$/.test(sp.before) ? sp.before : "";
  const sql = db();
  const [rows, actions] = await Promise.all([
    sql`
      select id, actor_id, actor_email, action, target, details, ip, created_at from audit_log
      where true
        ${action ? sql`and action = ${action}` : sql``}
        ${actor ? sql`and actor_email ilike ${"%" + actor.replace(/[\\%_]/g, (c) => "\\" + c) + "%"}` : sql``}
        ${before ? sql`and id < ${before}` : sql``}
      order by id desc limit ${PAGE + 1}`,
    sql`select distinct action from audit_log order by action`,
  ]);
  const more = rows.length > PAGE;
  const page = rows.slice(0, PAGE);
  const qs = (extra: Record<string, string>) =>
    new URLSearchParams({ ...(action ? { action } : {}), ...(actor ? { actor } : {}), ...extra }).toString();

  return (
    <Panel title="Audit log" description="Admin actions and security-relevant account events, newest first.">
      <form method="get" className="flex flex-wrap items-center gap-2 border-b border-line px-4 py-3">
        <Select
          name="action"
          defaultValue={action}
          aria-label="Action"
          className="h-8 w-auto min-w-44"
          mono
          options={[{ value: "", label: "All actions" }, ...actions.map((a) => ({ value: a.action, label: a.action }))]}
        />
        <input name="actor" defaultValue={actor} placeholder="Actor email" aria-label="Actor" className={cn(inputClass, "h-8 sm:w-64")} />
        <button type="submit" className={buttonClass("secondary", "sm")}>
          Filter
        </button>
        {action || actor ? (
          <Link href="/admin/audit" className={buttonClass("ghost", "sm")}>
            Clear
          </Link>
        ) : null}
      </form>
      <div className="overflow-x-auto scroll-thin">
        <table className="w-full min-w-[860px] text-[13px]">
          <thead>
            <tr className="border-b border-line text-left text-[12px] text-muted">
              <th className="px-4 py-2 font-medium">Time</th>
              <th className="px-4 py-2 font-medium">Actor</th>
              <th className="px-4 py-2 font-medium">Action</th>
              <th className="px-4 py-2 font-medium">Target</th>
              <th className="px-4 py-2 font-medium">Details</th>
              <th className="px-4 py-2 font-medium">IP</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-line">
            {page.map((r) => (
              <tr key={r.id} className="align-top">
                <td className="whitespace-nowrap px-4 py-2 text-ink-2">{formatDateTime(r.created_at)}</td>
                <td className="px-4 py-2">
                  {r.actor_id ? (
                    <Link href={`/admin/users/${r.actor_id}`} className="text-ink hover:underline">
                      {r.actor_email}
                    </Link>
                  ) : (
                    <span className="text-muted">{r.actor_email || "system"}</span>
                  )}
                </td>
                <td className="px-4 py-2 font-mono text-[12.5px] text-ink">{r.action}</td>
                <td className="max-w-64 truncate px-4 py-2 text-ink-2" title={r.target}>
                  {r.target}
                </td>
                <td className="max-w-80 px-4 py-2 text-[12.5px] text-muted">{detailText(r.details ?? {})}</td>
                <td className="px-4 py-2 font-mono text-[12px] text-muted">{r.ip}</td>
              </tr>
            ))}
          </tbody>
        </table>
        {!page.length ? <p className="px-4 py-6 text-[13px] text-muted">Nothing recorded yet.</p> : null}
      </div>
      <div className="flex items-center justify-between gap-3 border-t border-line px-4 py-2.5 text-[12.5px] text-muted">
        <span>{before ? "Older entries" : "Latest entries"}</span>
        <span className="flex gap-2">
          {before ? (
            <Link href={`/admin/audit?${qs({})}`} className={buttonClass("ghost", "sm")}>
              Newest
            </Link>
          ) : null}
          {more ? (
            <Link href={`/admin/audit?${qs({ before: String(page[page.length - 1].id) })}`} className={buttonClass("secondary", "sm")}>
              Older
            </Link>
          ) : null}
        </span>
      </div>
    </Panel>
  );
}

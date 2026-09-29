import type { Metadata } from "next";
import Link from "next/link";
import { Search } from "lucide-react";
import { db } from "@/lib/db";
import { formatDateTime, formatNumber, formatTransfer, timeAgo } from "@/lib/format";
import { GB } from "@/lib/usage";
import { getSettings } from "@/lib/settings";
import { Badge, cn, inputClass, Panel } from "@/components/ui";
import { CreateUserForm } from "./create-user";

export const metadata: Metadata = { title: "Users" };

const FILTERS = [
  { id: "", label: "All" },
  { id: "admin", label: "Admins" },
  { id: "disabled", label: "Disabled" },
  { id: "unverified", label: "Unverified" },
] as const;

export default async function UsersPage({ searchParams }: PageProps<"/admin/users">) {
  const sp = await searchParams;
  const q = typeof sp.q === "string" ? sp.q.trim().slice(0, 100) : "";
  const filter = typeof sp.filter === "string" ? sp.filter : "";
  const sql = db();
  const like = `%${q.replace(/[\\%_]/g, (c) => "\\" + c)}%`;
  const users = await sql`
    select u.id, u.email, u.name, u.is_admin, u.trusted, u.disabled_at, u.email_verified_at, u.created_at,
      u.transfer_quota_gb,
      (select coalesce(sum(d.bytes_in + d.bytes_out), 0)::bigint from usage_daily d
        where d.user_id = u.id and d.day >= date_trunc('month', now() at time zone 'utc')::date) as month_bytes,
      (select count(*) from tunnels t where t.user_id = u.id and t.ended_at is null)::int as online,
      (select count(*) from requests r where r.user_id = u.id and r.started_at > now() - interval '24 hours')::int as req24,
      (select max(connected_at) from agent_sessions a where a.user_id = u.id) as last_connected
    from users u
    where true
      ${q ? sql`and (u.email ilike ${like} or u.name ilike ${like})` : sql``}
      ${filter === "admin" ? sql`and u.is_admin` : filter === "disabled" ? sql`and u.disabled_at is not null` : filter === "unverified" ? sql`and u.email_verified_at is null` : sql``}
    order by u.created_at desc
    limit 500`;
  const settings = await getSettings();
  const mode = settings.signup_mode;
  const quotaOf = (u: Record<string, unknown>) =>
    (u.transfer_quota_gb as number | null) ?? (u.is_admin ? 0 : settings.limit_transfer_gb);

  return (
    <>
      <Panel
        title="Users"
        description={
          mode === "open"
            ? "Anyone can create an account (Settings → Sign-up). You can also add people here."
            : mode === "invite"
              ? "People join through team invite links (Settings → Sign-up), or you add them here."
              : "Sign-up is closed (Settings → Sign-up), so accounts are created here."
        }
        className="mb-6"
      >
        <form method="get" className="flex flex-wrap items-center gap-2 border-b border-line px-4 py-3">
          <label className="relative min-w-52 flex-1 sm:max-w-sm">
            <span className="sr-only">Search users</span>
            <Search size={14} className="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 text-muted" />
            <input name="q" defaultValue={q} placeholder="Search by email or name" className={cn(inputClass, "h-8 pl-8")} />
          </label>
          {filter ? <input type="hidden" name="filter" value={filter} /> : null}
          <div role="group" aria-label="Filter" className="inline-flex h-8 items-center rounded-[5px] border border-line-strong p-0.5">
            {FILTERS.map((f) => (
              <Link
                key={f.id}
                href={`/admin/users?${new URLSearchParams({ ...(q ? { q } : {}), ...(f.id ? { filter: f.id } : {}) })}`}
                aria-current={filter === f.id ? "true" : undefined}
                className={cn(
                  "flex h-full items-center rounded-[3px] px-2.5 text-[12.5px]",
                  filter === f.id ? "bg-surface-3 font-medium text-ink" : "text-muted hover:text-ink",
                )}
              >
                {f.label}
              </Link>
            ))}
          </div>
          <span className="ml-auto text-[12.5px] text-muted tabular">
            {users.length} {users.length === 1 ? "account" : "accounts"}
          </span>
        </form>
        <div className="overflow-x-auto scroll-thin">
          <table className="w-full min-w-[880px] text-[13px]">
            <thead>
              <tr className="border-b border-line text-left text-[12px] text-muted">
                <th className="px-4 py-2 font-medium">User</th>
                <th className="px-4 py-2 font-medium">Status</th>
                <th className="px-4 py-2 font-medium">Online tunnels</th>
                <th className="px-4 py-2 font-medium">Requests, 24h</th>
                <th className="px-4 py-2 font-medium">Transfer this month</th>
                <th className="px-4 py-2 font-medium">Last client connection</th>
                <th className="px-4 py-2 font-medium">Joined</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-line">
              {users.map((u) => (
                <tr key={u.id} className={cn("hover:bg-surface-2", u.disabled_at && "text-muted")}>
                  <td className="px-4 py-2.5">
                    <Link href={`/admin/users/${u.id}`} className="block">
                      <span className="block font-medium text-ink hover:underline">{u.name || u.email.split("@")[0]}</span>
                      <span className="block text-[12px] text-muted">{u.email}</span>
                    </Link>
                  </td>
                  <td className="px-4 py-2.5">
                    <span className="flex flex-wrap gap-1.5">
                      {u.is_admin ? <Badge tone="outline">Admin</Badge> : null}
                      {u.trusted && !u.is_admin ? <Badge tone="outline">Trusted</Badge> : null}
                      {u.disabled_at ? <Badge tone="danger">Disabled</Badge> : null}
                      {!u.email_verified_at ? <Badge tone="live">Unverified</Badge> : null}
                    </span>
                  </td>
                  <td className="px-4 py-2.5 tabular text-ink">{formatNumber(u.online)}</td>
                  <td className="px-4 py-2.5 tabular text-ink">{formatNumber(u.req24)}</td>
                  <td className="px-4 py-2.5 tabular">
                    {(() => {
                      const quota = quotaOf(u);
                      const used = Number(u.month_bytes);
                      const over = quota > 0 && used >= quota * GB;
                      return (
                        <span className={over ? "font-medium text-danger" : "text-ink"}>
                          {formatTransfer(used)}
                          <span className="text-muted">{quota ? ` / ${quota} GB` : ""}</span>
                        </span>
                      );
                    })()}
                  </td>
                  <td className="px-4 py-2.5 text-ink-2">{timeAgo(u.last_connected)}</td>
                  <td className="px-4 py-2.5 text-ink-2" title={formatDateTime(u.created_at)}>
                    {timeAgo(u.created_at)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          {!users.length ? <p className="px-4 py-6 text-[13px] text-muted">No accounts match.</p> : null}
        </div>
      </Panel>
      <Panel title="Add a user" bodyClassName="p-4">
        <CreateUserForm />
      </Panel>
    </>
  );
}

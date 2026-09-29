import type { Metadata } from "next";
import Link from "next/link";
import { Search } from "lucide-react";
import { db } from "@/lib/db";
import { timeAgo } from "@/lib/format";
import { AuthBadge, Badge, cn, inputClass, Panel } from "@/components/ui";
import { ConfirmSubmit } from "@/components/client-ui";
import { adminDeleteDomainAction } from "@/app/actions/admin";

export const metadata: Metadata = { title: "All domains" };

export default async function AdminDomainsPage({ searchParams }: PageProps<"/admin/domains">) {
  const sp = await searchParams;
  const q = typeof sp.q === "string" ? sp.q.trim().toLowerCase().slice(0, 100) : "";
  const sql = db();
  const rows = await sql`
    select d.id, d.hostname, d.kind, d.verified_at, d.auth_mode, d.is_default, d.created_at,
      u.id as user_id, u.email, t.slug as team_slug,
      exists(select 1 from tunnels x where x.ended_at is null and x.hostname = d.hostname) as online
    from domains d join users u on u.id = d.user_id left join teams t on t.id = d.team_id
    ${q ? sql`where d.hostname like ${"%" + q.replace(/[\\%_]/g, (c) => "\\" + c) + "%"} or u.email ilike ${"%" + q + "%"}` : sql``}
    order by d.created_at desc limit 1000`;
  return (
    <Panel title="Domains" description="Static hostnames and custom domains across all accounts and teams.">
      <form method="get" className="flex flex-wrap items-center gap-2 border-b border-line px-4 py-3">
        <label className="relative min-w-52 flex-1 sm:max-w-sm">
          <span className="sr-only">Search domains</span>
          <Search size={14} className="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 text-muted" />
          <input name="q" defaultValue={q} placeholder="Hostname or owner email" className={cn(inputClass, "h-8 pl-8")} />
        </label>
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
  );
}

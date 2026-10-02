import type { Metadata } from "next";
import { UsersRound } from "lucide-react";
import { db } from "@/lib/db";
import { formatDateTime, formatNumber, timeAgo } from "@/lib/format";
import Link from "next/link";
import { Badge, EmptyState, Panel } from "@/components/ui";
import { PLAN_LABEL, type TeamPlan } from "@/lib/plans";
import { ConfirmSubmit } from "@/components/client-ui";
import { adminDeleteTeamAction } from "@/app/actions/admin";
import { PAGE_SIZE, Pager, pageOffset, pageParam } from "@/components/pager";

export const metadata: Metadata = { title: "All teams" };

export default async function AdminTeamsPage({ searchParams }: PageProps<"/admin/teams">) {
  const page = pageParam((await searchParams).page);
  const [[{ total }], teams] = await Promise.all([
    db()`select count(*)::int as total from teams`,
    db()`
    select t.id, t.name, t.slug, t.created_at,
      (select count(*)::int from team_members m where m.team_id = t.id) as members,
      (select string_agg(u.email, ', ' order by u.email) from team_members m join users u on u.id = m.user_id
        where m.team_id = t.id and m.role = 'owner') as owners,
      (select count(*)::int from domains d where d.team_id = t.id) as domains,
      (select count(*)::int from oidc_providers p where p.team_id = t.id) as providers,
      team_plan(t.id) as plan, t.plan_granted is not null as granted
    from teams t order by t.created_at desc
    limit ${PAGE_SIZE} offset ${pageOffset(page)}`,
  ]);
  return (
    <Panel title="Teams" description={`${formatNumber(total)} on this server. Deleting a team removes its providers and domains.`}>
      {teams.length ? (
        <div className="overflow-x-auto scroll-thin">
          <table className="w-full min-w-[760px] text-[13px]">
            <thead>
              <tr className="border-b border-line text-left text-[12px] text-muted">
                <th className="px-4 py-2 font-medium">Team</th>
                <th className="px-4 py-2 font-medium">Owners</th>
                <th className="px-4 py-2 font-medium">Plan</th>
                <th className="px-4 py-2 font-medium">Members</th>
                <th className="px-4 py-2 font-medium">Domains</th>
                <th className="px-4 py-2 font-medium">Providers</th>
                <th className="px-4 py-2 font-medium">Created</th>
                <th className="px-4 py-2" />
              </tr>
            </thead>
            <tbody className="divide-y divide-line">
              {teams.map((t) => (
                <tr key={t.id}>
                  <td className="px-4 py-2.5">
                    <Link href={`/admin/teams/${t.id}`} className="font-medium text-ink hover:underline">
                      {t.name}
                    </Link>
                    <span className="block font-mono text-[12px] text-muted">{t.slug}</span>
                  </td>
                  <td className="px-4 py-2.5 text-ink-2">{t.owners ?? "none"}</td>
                  <td className="px-4 py-2.5">
                    {t.plan ? <Badge tone="ok">{PLAN_LABEL[t.plan as TeamPlan]}</Badge> : <span className="text-muted">none</span>}
                    {t.granted ? <span className="ml-1.5 text-[12px] text-muted">exempt</span> : null}
                  </td>
                  <td className="px-4 py-2.5 tabular">{t.members}</td>
                  <td className="px-4 py-2.5 tabular">{t.domains}</td>
                  <td className="px-4 py-2.5 tabular">{t.providers}</td>
                  <td className="px-4 py-2.5 text-ink-2" title={formatDateTime(t.created_at)}>
                    {timeAgo(t.created_at)}
                  </td>
                  <td className="px-4 py-2 text-right">
                    <form action={adminDeleteTeamAction}>
                      <input type="hidden" name="id" value={t.id} />
                      <ConfirmSubmit confirmText={`Delete ${t.slug}?`}>Delete</ConfirmSubmit>
                    </form>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <EmptyState icon={<UsersRound size={22} />} title="No teams yet" />
      )}
      <Pager path="/admin/teams" page={page} total={total as number} />
    </Panel>
  );
}

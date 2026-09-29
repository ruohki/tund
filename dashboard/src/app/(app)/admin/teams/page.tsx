import type { Metadata } from "next";
import { UsersRound } from "lucide-react";
import { db } from "@/lib/db";
import { formatDateTime, timeAgo } from "@/lib/format";
import { EmptyState, Panel } from "@/components/ui";
import { ConfirmSubmit } from "@/components/client-ui";
import { adminDeleteTeamAction } from "@/app/actions/admin";

export const metadata: Metadata = { title: "All teams" };

export default async function AdminTeamsPage() {
  const teams = await db()`
    select t.id, t.name, t.slug, t.created_at,
      (select count(*)::int from team_members m where m.team_id = t.id) as members,
      (select string_agg(u.email, ', ' order by u.email) from team_members m join users u on u.id = m.user_id
        where m.team_id = t.id and m.role = 'owner') as owners,
      (select count(*)::int from domains d where d.team_id = t.id) as domains,
      (select count(*)::int from oidc_providers p where p.team_id = t.id) as providers
    from teams t order by t.created_at desc`;
  return (
    <Panel title="Teams" description={`${teams.length} on this server. Deleting a team removes its providers and domains.`}>
      {teams.length ? (
        <div className="overflow-x-auto scroll-thin">
          <table className="w-full min-w-[760px] text-[13px]">
            <thead>
              <tr className="border-b border-line text-left text-[12px] text-muted">
                <th className="px-4 py-2 font-medium">Team</th>
                <th className="px-4 py-2 font-medium">Owners</th>
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
                    <span className="font-medium text-ink">{t.name}</span>
                    <span className="block font-mono text-[12px] text-muted">{t.slug}</span>
                  </td>
                  <td className="px-4 py-2.5 text-ink-2">{t.owners ?? "none"}</td>
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
    </Panel>
  );
}

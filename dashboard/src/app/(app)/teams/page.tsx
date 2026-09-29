import type { Metadata } from "next";
import Link from "next/link";
import { UsersRound } from "lucide-react";
import { requireUser } from "@/lib/auth";
import { getSettings } from "@/lib/settings";
import { listMyTeams } from "@/lib/teams";
import { Badge, EmptyState, PageHeader, Panel } from "@/components/ui";
import { CreateTeamForm } from "./create-team";

export const metadata: Metadata = { title: "Teams" };

export default async function TeamsPage() {
  const user = await requireUser();
  const teams = await listMyTeams(user.id);
  const max = (await getSettings()).limit_teams;
  const owned = teams.filter((t) => t.role === "owner").length;
  const full = max > 0 && !user.isAdmin && owned >= max;

  return (
    <>
      <PageHeader
        title="Teams"
        description="Share identity providers and domains with other accounts. Members can run tunnels on team domains, protect them with team providers, and see the traffic on them."
      />
      <Panel title="Your teams" className="mb-6">
        {teams.length ? (
          <ul className="divide-y divide-line">
            {teams.map((t) => (
              <li key={t.id}>
                <Link href={`/teams/${t.slug}`} className="flex flex-wrap items-center justify-between gap-3 px-4 py-3 hover:bg-surface-2">
                  <span className="min-w-0">
                    <span className="flex items-center gap-2">
                      <span className="truncate text-[14.5px] font-medium text-ink">{t.name}</span>
                      <span className="font-mono text-[12px] text-muted">{t.slug}</span>
                    </span>
                    <span className="mt-0.5 block text-[12.5px] text-muted">
                      {t.members} {t.members === 1 ? "member" : "members"}, {t.domains}{" "}
                      {t.domains === 1 ? "domain" : "domains"}, {t.providers} {t.providers === 1 ? "provider" : "providers"}
                    </span>
                  </span>
                  <Badge tone={t.role === "member" ? "neutral" : "outline"}>{t.role[0].toUpperCase() + t.role.slice(1)}</Badge>
                </Link>
              </li>
            ))}
          </ul>
        ) : (
          <EmptyState icon={<UsersRound size={22} />} title="You're not in a team yet">
            Create one below, or ask a team owner for an invite link.
          </EmptyState>
        )}
      </Panel>
      <Panel
        title="Create a team"
        description={max > 0 && !user.isAdmin ? `You own ${owned} of ${max} teams you can own.` : undefined}
        bodyClassName="p-4"
      >
        {full ? (
          <p className="rounded-md border border-line bg-surface-2 px-3 py-2 text-[13px] text-ink-2">
            You already own {owned} {owned === 1 ? "team" : "teams"}, the most an account can own on this server. Delete
            one or hand over ownership to create another.
          </p>
        ) : (
          <CreateTeamForm />
        )}
      </Panel>
    </>
  );
}

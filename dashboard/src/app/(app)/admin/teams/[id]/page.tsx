import type { Metadata } from "next";
import Link from "next/link";
import { notFound } from "next/navigation";
import { ArrowLeft } from "lucide-react";
import { requireAdmin } from "@/lib/auth";
import { db } from "@/lib/db";
import { isUuid } from "@/lib/requests";
import { PLAN_LABEL, teamBilling } from "@/lib/plans";
import { teamSubscription } from "@/lib/billing";
import { formatDateTime } from "@/lib/format";
import { Badge, PageHeader, Panel } from "@/components/ui";
import { TeamPlanForm } from "./team-plan-form";

export const metadata: Metadata = { title: "Team" };

export default async function AdminTeamPage({ params }: PageProps<"/admin/teams/[id]">) {
  await requireAdmin();
  const { id } = await params;
  if (!isUuid(id)) notFound();
  const [t] = await db()`select * from teams where id = ${id}`;
  if (!t) notFound();
  const [billing, sub, members] = await Promise.all([
    teamBilling(id),
    teamSubscription(id),
    db()`
      select u.id, u.email, m.role from team_members m join users u on u.id = m.user_id where m.team_id = ${id}
      order by case m.role when 'owner' then 0 when 'admin' then 1 else 2 end, u.email`,
  ]);
  const payer = sub?.payerId ? members.find((m) => m.id === sub.payerId)?.email ?? sub.payerId : null;
  return (
    <>
      <Link href="/admin/teams" className="mb-3 inline-flex items-center gap-1 text-[13px] text-muted hover:text-ink">
        <ArrowLeft size={14} /> Teams
      </Link>
      <PageHeader
        title={t.name}
        description={
          <span className="flex flex-wrap items-center gap-2">
            <span className="font-mono">{t.slug}</span>
            {billing.plan ? <Badge tone="ok">{PLAN_LABEL[billing.plan]}</Badge> : <Badge tone="outline">No plan</Badge>}
            {billing.granted ? <Badge tone="outline">Billing exempt</Badge> : null}
            <span className="text-muted">
              {billing.members} {billing.members === 1 ? "member" : "members"}
              {billing.seats !== null ? ` of ${billing.seats} seats` : ""}
            </span>
          </span>
        }
      />
      <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
        <Panel title="Billing" bodyClassName="p-4">
          <p className="mb-4 text-[13px] text-ink-2">
            {sub
              ? `Subscription ${sub.id}: ${PLAN_LABEL[sub.plan]}, ${sub.status}, billed ${sub.interval === "year" ? "yearly" : "monthly"}${sub.seatPacks ? `, ${sub.seatPacks} extra seat packs` : ""}${sub.active && sub.periodEnd ? `, ${sub.cancelAtPeriodEnd ? "ends" : "renews"} ${formatDateTime(sub.periodEnd)}` : ""}. ${sub.active ? "Paid" : "Was paid"} by ${payer ?? "an account that no longer exists"}.`
              : "No subscription."}
          </p>
          {/* Keyed on the stored values: React resets the form after saving, so it remounts with them. */}
          <TeamPlanForm
            key={`${t.plan_granted ?? ""}:${t.seats_override ?? ""}`}
            teamId={id}
            planGranted={(t.plan_granted as string | null) ?? null}
            seatsOverride={(t.seats_override as number | null) ?? null}
          />
        </Panel>
        <Panel title="Members">
          <ul className="divide-y divide-line">
            {members.map((m) => (
              <li key={m.id} className="flex items-center justify-between gap-3 px-4 py-2 text-[13px]">
                <Link href={`/admin/users/${m.id}`} className="truncate text-ink hover:underline">
                  {m.email}
                </Link>
                <span className="text-muted">{m.role}</span>
              </li>
            ))}
          </ul>
        </Panel>
      </div>
    </>
  );
}

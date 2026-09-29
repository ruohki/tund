"use server";

import { randomBytes } from "node:crypto";
import { refresh } from "next/cache";
import { redirect } from "next/navigation";
import { requireUser, sha256, type User } from "@/lib/auth";
import { config } from "@/lib/config";
import { getSettings } from "@/lib/settings";
import { inviteEmail, smtpConfigured, trySendMail } from "@/lib/mail";
import { db, notify } from "@/lib/db";
import { isUuid } from "@/lib/requests";
import { atLeast, membershipById, TEAM_SLUG_RE, teamChanged, type TeamRole } from "@/lib/teams";
import { checkEmail } from "@/lib/validate";
import type { FormState } from "./auth";

const str = (fd: FormData, k: string) => String(fd.get(k) ?? "").trim();
const INVITE_DAYS = 7;

function isUniqueViolation(err: unknown) {
  return (err as { code?: string }).code === "23505";
}

async function actor(user: User, teamId: string, min: TeamRole) {
  if (!isUuid(teamId)) return { error: "Unknown team." } as const;
  const m = await membershipById(user.id, teamId);
  if (!m) return { error: "You're not a member of this team." } as const;
  if (!atLeast(m.role, min)) {
    return { error: min === "owner" ? "Only team owners can do that." : "Only team owners and admins can do that." } as const;
  }
  return { m } as const;
}

// --- teams -----------------------------------------------------------------

export async function createTeamAction(_: FormState, fd: FormData): Promise<FormState> {
  const user = await requireUser();
  const name = str(fd, "name").slice(0, 80);
  const slug = str(fd, "slug").toLowerCase();
  if (!name) return { error: "Give the team a name." };
  if (!TEAM_SLUG_RE.test(slug)) return { error: "Slug: use lowercase letters, digits and hyphens (up to 40 characters)." };
  const max = (await getSettings()).limit_teams;
  let teamId: string;
  try {
    const res = await db().begin(async (tx) => {
      await tx`select 1 from users where id = ${user.id} for update`;
      if (max > 0 && !user.isAdmin) {
        const [{ n }] = await tx`select count(*)::int as n from team_members where user_id = ${user.id} and role = 'owner'`;
        if (n >= max) return { error: `You already own ${n} ${n === 1 ? "team" : "teams"}, the most an account can own here.` };
      }
      const [t] = await tx`insert into teams (name, slug, created_by) values (${name}, ${slug}, ${user.id}) returning id`;
      await tx`insert into team_members (team_id, user_id, role) values (${t.id}, ${user.id}, 'owner')`;
      return { id: t.id as string };
    });
    if ("error" in res) return { error: res.error };
    teamId = res.id;
  } catch (err) {
    if (isUniqueViolation(err)) return { error: `The slug “${slug}” is taken. Pick another one.` };
    throw err;
  }
  await teamChanged(teamId);
  redirect(`/teams/${slug}`);
}

export async function deleteTeamAction(_: FormState, fd: FormData): Promise<FormState> {
  const user = await requireUser();
  const teamId = str(fd, "team_id");
  const a = await actor(user, teamId, "owner");
  if ("error" in a) return { error: a.error };
  if (str(fd, "confirm") !== a.m.team.slug) return { error: `Type “${a.m.team.slug}” to confirm.` };
  const domains = await db().begin(async (tx) => {
    const rows = await tx`select id from domains where team_id = ${teamId}`;
    await tx`delete from teams where id = ${teamId}`;
    return rows.map((r) => r.id as string);
  });
  await teamChanged(teamId);
  for (const d of domains) await notify("tund_config", { kind: "domain", id: d });
  redirect("/teams");
}

// --- members ---------------------------------------------------------------

/** Role change and removal share the "keep at least one owner" rule, checked under a team lock. */
async function changeMember(user: User, teamId: string, targetId: string, newRole: TeamRole | null): Promise<FormState> {
  const self = targetId === user.id;
  const a = await actor(user, teamId, self && newRole === null ? "member" : "admin");
  if ("error" in a) return { error: a.error };
  if (!isUuid(targetId)) return { error: "Unknown member." };
  const res = await db().begin(async (tx): Promise<FormState> => {
    await tx`select 1 from teams where id = ${teamId} for update`;
    const [target] = await tx`select role from team_members where team_id = ${teamId} and user_id = ${targetId}`;
    if (!target) return { error: "That person is no longer a member." };
    const current = target.role as TeamRole;
    if (a.m.role !== "owner" && !self) {
      // Admins manage members and admins, never owners.
      if (current === "owner" || newRole === "owner") return { error: "Only team owners can change owners." };
    }
    if (a.m.role !== "owner" && self && newRole && newRole !== current && atLeast(newRole, a.m.role)) {
      return { error: "You can't promote yourself." };
    }
    if (current === "owner" && newRole !== "owner") {
      const [{ n }] = await tx`select count(*)::int as n from team_members where team_id = ${teamId} and role = 'owner'`;
      if (n <= 1) {
        return {
          error:
            newRole === null
              ? "A team needs at least one owner. Make someone else an owner first, or delete the team."
              : "A team needs at least one owner. Make someone else an owner first.",
        };
      }
    }
    if (newRole === null) await tx`delete from team_members where team_id = ${teamId} and user_id = ${targetId}`;
    else await tx`update team_members set role = ${newRole} where team_id = ${teamId} and user_id = ${targetId}`;
    return { ok: newRole === null ? "Removed." : "Role updated." };
  });
  if (res?.ok) await teamChanged(teamId);
  return res;
}

export async function changeRoleAction(_: FormState, fd: FormData): Promise<FormState> {
  const user = await requireUser();
  const role = str(fd, "role") as TeamRole;
  if (!["owner", "admin", "member"].includes(role)) return { error: "Unknown role." };
  const res = await changeMember(user, str(fd, "team_id"), str(fd, "user_id"), role);
  refresh();
  return res;
}

export async function removeMemberAction(_: FormState, fd: FormData): Promise<FormState> {
  const user = await requireUser();
  const res = await changeMember(user, str(fd, "team_id"), str(fd, "user_id"), null);
  refresh();
  return res;
}

export async function leaveTeamAction(_: FormState, fd: FormData): Promise<FormState> {
  const user = await requireUser();
  const res = await changeMember(user, str(fd, "team_id"), user.id, null);
  if (res?.error) return res;
  redirect("/teams");
}

export type AddMemberState = (FormState & { inviteEmail?: string }) | null;

/** Existing accounts are added directly; for unknown emails the UI offers an invite link instead. */
export async function addMemberAction(_: AddMemberState, fd: FormData): Promise<AddMemberState> {
  const user = await requireUser();
  const teamId = str(fd, "team_id");
  const a = await actor(user, teamId, "admin");
  if ("error" in a) return { error: a.error };
  const email = str(fd, "email").toLowerCase();
  const role = str(fd, "role") as TeamRole;
  if (checkEmail(email)) return { error: "Enter the email address of the person to add." };
  if (!["admin", "member", ...(a.m.role === "owner" ? ["owner"] : [])].includes(role)) return { error: "Unknown role." };
  const [target] = await db()`select id, disabled_at from users where email = ${email}`;
  if (!target) return { inviteEmail: email };
  if (target.disabled_at) return { error: `${email} is disabled on this server.` };
  const res = await db()`
    insert into team_members (team_id, user_id, role) values (${teamId}, ${target.id}, ${role})
    on conflict (team_id, user_id) do nothing`;
  if (!res.count) return { error: `${email} is already a member.` };
  await teamChanged(teamId);
  refresh();
  return { ok: `Added ${email} as ${role}.` };
}

// --- invites ---------------------------------------------------------------

export type InviteState = { error?: string; link?: string; email?: string; emailed?: boolean } | null;

export async function createInviteAction(_: InviteState, fd: FormData): Promise<InviteState> {
  const user = await requireUser();
  const teamId = str(fd, "team_id");
  const a = await actor(user, teamId, "admin");
  if ("error" in a) return { error: a.error };
  const email = str(fd, "email").toLowerCase();
  if (email && checkEmail(email)) return { error: "Enter a valid email address, or leave it empty for a link anyone can use." };
  const role = str(fd, "role") === "admin" ? "admin" : "member";
  const token = randomBytes(32).toString("base64url");
  await db()`
    insert into team_invites (team_id, email, role, token_hash, invited_by, expires_at)
    values (${teamId}, ${email}, ${role}, ${sha256(token)}, ${user.id}, ${new Date(Date.now() + INVITE_DAYS * 86400_000)})`;
  const link = `${config().dashboardUrl}/invite/${token}`;
  // The link stays copyable either way; email is a convenience when SMTP is set up.
  let emailed = false;
  if (email && (await smtpConfigured())) {
    const { instance_name } = await getSettings();
    emailed = await trySendMail(email, inviteEmail(instance_name, link, a.m.team.name, user.name || user.email, role));
  }
  refresh();
  return { link, email, emailed };
}

export async function revokeInviteAction(fd: FormData) {
  const user = await requireUser();
  const teamId = str(fd, "team_id");
  const a = await actor(user, teamId, "admin");
  if ("error" in a) return;
  const id = str(fd, "id");
  if (isUuid(id)) await db()`delete from team_invites where id = ${id} and team_id = ${teamId} and accepted_at is null`;
  refresh();
}

export type AcceptResult = { error: string } | null;

export async function acceptInviteAction(token: string): Promise<AcceptResult> {
  const user = await requireUser();
  if (typeof token !== "string" || token.length > 100) return { error: "This invite link isn't valid." };
  const res = await db().begin(async (tx) => {
    const [inv] = await tx`
      select i.*, t.slug from team_invites i join teams t on t.id = i.team_id
      where i.token_hash = ${sha256(token)} for update of i`;
    if (!inv) return { error: "This invite link isn't valid. Ask for a new one." };
    if (inv.accepted_at) return { error: "This invite has already been used." };
    if ((inv.expires_at as Date).getTime() < Date.now()) return { error: "This invite has expired. Ask for a new one." };
    if (inv.email && inv.email !== user.email.toLowerCase()) {
      return { error: `This invite is for ${inv.email}. Sign in with that account to accept it.` };
    }
    const added = await tx`
      insert into team_members (team_id, user_id, role) values (${inv.team_id}, ${user.id}, ${inv.role})
      on conflict (team_id, user_id) do nothing`;
    if (added.count) await tx`update team_invites set accepted_by = ${user.id}, accepted_at = now() where id = ${inv.id}`;
    return { teamId: inv.team_id as string, slug: inv.slug as string, added: added.count > 0 };
  });
  if ("error" in res) return { error: res.error as string };
  if (res.added) await teamChanged(res.teamId);
  redirect(`/teams/${res.slug}`);
}

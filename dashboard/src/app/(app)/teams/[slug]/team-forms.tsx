"use client";

import { startTransition, useActionState, useState } from "react";
import { Link2, TriangleAlert } from "lucide-react";
import {
  addMemberAction,
  changeRoleAction,
  createInviteAction,
  deleteTeamAction,
  leaveTeamAction,
  removeMemberAction,
  revokeInviteAction,
  updateTeamSsoAction,
} from "@/app/actions/teams";
import type { TeamRole } from "@/lib/teams";
import { ALLOW_LIST_HINT } from "@/lib/validate";
import { buttonClass, cn, Field, FormMessage, inputClass, Select } from "@/components/ui";
import { Command, ConfirmSubmit, SubmitButton } from "@/components/client-ui";

const ROLE_LABEL: Record<TeamRole, string> = { owner: "Owner", admin: "Admin", member: "Member" };

export type MemberItem = { id: string; email: string; name: string; role: TeamRole; self: boolean };

/** Role select + remove for one member; what's offered follows the spec's role rules. */
export function MemberRow({ teamId, member, myRole }: { teamId: string; member: MemberItem; myRole: TeamRole }) {
  const [roleState, roleAction] = useActionState(changeRoleAction, null);
  const [removeState, removeAction] = useActionState(removeMemberAction, null);
  // Owners manage everyone; admins manage admins and members (not owners); members only see.
  const manageable = myRole === "owner" || (myRole === "admin" && member.role !== "owner");
  const roles: TeamRole[] = myRole === "owner" ? ["owner", "admin", "member"] : ["admin", "member"];
  const error = roleState?.error ?? removeState?.error;
  return (
    <li className="flex flex-wrap items-center justify-between gap-3 px-4 py-2.5">
      <div className="min-w-0">
        <p className="truncate text-[13.5px] font-medium text-ink">
          {member.name || member.email.split("@")[0]}
          {member.self ? <span className="ml-1.5 text-[12px] font-normal text-muted">(you)</span> : null}
        </p>
        <p className="truncate text-[12px] text-muted">{member.email}</p>
        {error ? <p className="mt-1 text-[12px] text-danger">{error}</p> : null}
      </div>
      <div className="flex items-center gap-2">
        {manageable && !member.self ? (
          <>
            <Select
              defaultValue={member.role}
              aria-label={`Role of ${member.email}`}
              onValueChange={(role) => {
                // Save right away, like the old auto-submitting form.
                const fd = new FormData();
                fd.set("team_id", teamId);
                fd.set("user_id", member.id);
                fd.set("role", role);
                startTransition(() => roleAction(fd));
              }}
              className="h-7 w-auto min-w-24 py-0 text-[12.5px]"
              options={roles.map((r) => ({ value: r, label: ROLE_LABEL[r] }))}
            />
            <form action={removeAction}>
              <input type="hidden" name="team_id" value={teamId} />
              <input type="hidden" name="user_id" value={member.id} />
              <ConfirmSubmit variant="ghost" confirmText="Remove from team?">
                Remove
              </ConfirmSubmit>
            </form>
          </>
        ) : (
          <span className="rounded-[4px] border border-line-strong px-1.5 py-0.5 text-[12px] text-ink-2">
            {ROLE_LABEL[member.role]}
          </span>
        )}
      </div>
    </li>
  );
}

function RoleSelect({ name, owner, id }: { name: string; owner: boolean; id: string }) {
  return (
    <Select
      id={id}
      name={name}
      defaultValue="member"
      className="sm:w-36"
      options={[
        { value: "member", label: "Member" },
        { value: "admin", label: "Admin" },
        ...(owner ? [{ value: "owner", label: "Owner" }] : []),
      ]}
    />
  );
}

function InviteLink({ link, email, emailed }: { link: string; email?: string; emailed?: boolean }) {
  return (
    <div className="rounded-md border border-sodium/50 bg-sodium-wash p-3">
      <p className="flex items-center gap-2 text-[13px] font-medium text-ink">
        <Link2 size={14} />
        {email ? `Invite link for ${email}` : "Invite link for anyone who has it"}
      </p>
      <Command prompt="" className="mt-2 bg-surface">
        {link}
      </Command>
      <p className="mt-2 text-[12px] text-muted">
        {emailed ? `Emailed to ${email}. ` : ""}Works once, for 7 days. The link is shown only now; send it through a
        channel you trust.
      </p>
    </div>
  );
}

/** Add an existing account directly; for unknown emails, offer an invite link bound to that address. */
export function AddMemberForm({ teamId, myRole }: { teamId: string; myRole: TeamRole }) {
  const [state, action] = useActionState(addMemberAction, null);
  const [invite, inviteAction] = useActionState(createInviteAction, null);
  return (
    <div className="flex flex-col gap-3">
      <form action={action} className="flex flex-wrap items-end gap-2">
        <input type="hidden" name="team_id" value={teamId} />
        <Field label="Add by email" htmlFor="add-email" className="min-w-0 flex-1 sm:max-w-sm">
          <input id="add-email" name="email" type="email" required placeholder="alex@example.com" className={inputClass} />
        </Field>
        <Field label="Role" htmlFor="add-role">
          <RoleSelect id="add-role" name="role" owner={myRole === "owner"} />
        </Field>
        <SubmitButton pendingText="Adding…">Add member</SubmitButton>
      </form>
      <FormMessage state={state} />
      {state?.inviteEmail && !invite?.link ? (
        <form action={inviteAction} className="flex flex-wrap items-center gap-3 rounded-md border border-line bg-surface-2 px-3 py-2.5">
          <input type="hidden" name="team_id" value={teamId} />
          <input type="hidden" name="email" value={state.inviteEmail} />
          <input type="hidden" name="role" value="member" />
          <p className="text-[13px] text-ink-2">
            No account uses {state.inviteEmail} yet. Create an invite link that only this address can accept?
          </p>
          <SubmitButton size="sm" pendingText="Creating…">
            Create invite link
          </SubmitButton>
        </form>
      ) : null}
      {invite?.error ? <FormMessage state={{ error: invite.error }} /> : null}
      {invite?.link ? <InviteLink link={invite.link} email={invite.email} emailed={invite.emailed} /> : null}
    </div>
  );
}

export function CreateInviteForm({ teamId }: { teamId: string }) {
  const [state, action] = useActionState(createInviteAction, null);
  return (
    <div className="flex flex-col gap-3">
      <form action={action} className="flex flex-col gap-1.5">
        <input type="hidden" name="team_id" value={teamId} />
        <div className="flex flex-wrap items-end gap-2">
          <Field label="Email (optional)" htmlFor="invite-email" className="min-w-0 flex-1 sm:max-w-sm">
            <input id="invite-email" name="email" type="email" placeholder="alex@example.com" className={inputClass} />
          </Field>
          <Field label="Role" htmlFor="invite-role">
            <RoleSelect id="invite-role" name="role" owner={false} />
          </Field>
          <SubmitButton variant="secondary" pendingText="Creating…">
            <Link2 size={14} /> Create invite link
          </SubmitButton>
        </div>
        <p className="text-xs text-muted">Leave the email empty for a link anyone can use once.</p>
      </form>
      {state?.error ? <FormMessage state={{ error: state.error }} /> : null}
      {state?.link ? <InviteLink link={state.link} email={state.email} emailed={state.emailed} /> : null}
    </div>
  );
}

export type TeamSso = { required: boolean; providerId: string; allow: string[] };

/** Require sign-in with a team provider on every team hostname. */
export function TeamSsoForm({
  teamId,
  sso,
  providers,
}: {
  teamId: string;
  sso: TeamSso;
  providers: { id: string; name: string; ref: string }[];
}) {
  const [state, action] = useActionState(updateTeamSsoAction, null);
  const [required, setRequired] = useState(sso.required);
  if (!providers.length && !sso.required) {
    return <p className="text-[13px] text-ink-2">Add an identity provider above first.</p>;
  }
  return (
    <form action={action} className="flex flex-col gap-4">
      <input type="hidden" name="team_id" value={teamId} />
      <label className="flex items-center gap-2 text-[13.5px] text-ink">
        <input
          type="checkbox"
          name="required"
          checked={required}
          onChange={(e) => setRequired(e.target.checked)}
          className="h-4 w-4 accent-[var(--ink)]"
        />
        Require sign-in on all team hostnames
      </label>
      {required ? (
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="Identity provider" htmlFor="sso-provider">
            <Select
              id="sso-provider"
              name="provider"
              defaultValue={sso.providerId}
              required
              placeholder="Choose…"
              options={providers.map((p) => ({ value: p.id, label: `${p.name} (${p.ref})` }))}
            />
          </Field>
          <Field label="Allowed people" htmlFor="sso-allow" hint={ALLOW_LIST_HINT}>
            <input
              id="sso-allow"
              name="allow"
              defaultValue={sso.allow.join(", ")}
              placeholder="@company.com, alex@example.org, group:engineering"
              className={cn(inputClass, "font-mono text-[12.5px]")}
            />
          </Field>
        </div>
      ) : (
        <>
          {/* Keep the stored choice for when it's turned on again. */}
          <input type="hidden" name="provider" value={sso.providerId} />
          <input type="hidden" name="allow" value={sso.allow.join(", ")} />
        </>
      )}
      <div className="flex flex-wrap items-center gap-3">
        <SubmitButton size="sm" pendingText="Saving…">
          Save
        </SubmitButton>
        <p className="text-[12px] text-muted">
          Members&apos; <code className="font-mono">--password</code> and <code className="font-mono">--oidc</code> flags
          are ignored on team hostnames while this is on. A team domain set to single sign-on in its own access settings
          keeps its provider and allow list.
        </p>
      </div>
      <FormMessage state={state} />
    </form>
  );
}

export function RevokeInvite({ teamId, id }: { teamId: string; id: string }) {
  return (
    <form action={revokeInviteAction}>
      <input type="hidden" name="team_id" value={teamId} />
      <input type="hidden" name="id" value={id} />
      <ConfirmSubmit variant="ghost" confirmText="Revoke?">
        Revoke
      </ConfirmSubmit>
    </form>
  );
}

export function LeaveTeam({ teamId }: { teamId: string }) {
  const [state, action] = useActionState(leaveTeamAction, null);
  return (
    <form action={action} className="flex flex-col items-start gap-2">
      <input type="hidden" name="team_id" value={teamId} />
      <ConfirmSubmit variant="secondary" size="md" confirmText="Leave this team?">
        Leave team
      </ConfirmSubmit>
      <FormMessage state={state} />
    </form>
  );
}

/** Deleting needs the slug typed out: it removes the team's providers and domains too. */
export function DeleteTeam({ teamId, slug }: { teamId: string; slug: string }) {
  const [state, action] = useActionState(deleteTeamAction, null);
  const [typed, setTyped] = useState("");
  return (
    <form action={action} className="flex flex-col gap-3">
      <input type="hidden" name="team_id" value={teamId} />
      <p className="flex items-start gap-2 text-[13px] text-ink-2">
        <TriangleAlert size={15} className="mt-0.5 shrink-0 text-danger" />
        Deletes the team, its identity providers and its domains. Tunnels on team domains are closed. Members keep their
        accounts.
      </p>
      <div className="flex flex-wrap items-center gap-2">
        <input
          name="confirm"
          value={typed}
          onChange={(e) => setTyped(e.target.value)}
          aria-label={`Type ${slug} to confirm`}
          placeholder={slug}
          autoComplete="off"
          spellCheck={false}
          className={cn(inputClass, "w-56 font-mono text-[12.5px]")}
        />
        <button type="submit" disabled={typed !== slug} className={buttonClass("danger")}>
          Delete team
        </button>
      </div>
      <FormMessage state={state} />
    </form>
  );
}

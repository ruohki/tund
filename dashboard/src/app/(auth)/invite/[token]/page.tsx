import type { Metadata } from "next";
import Link from "next/link";
import { getCurrentUser, sha256, withNext } from "@/lib/auth";
import { signupCheck } from "@/lib/signup";
import { db } from "@/lib/db";
import { buttonClass } from "@/components/ui";
import { logoutAction } from "@/app/actions/auth";
import { AcceptInvite } from "./accept";

export const metadata: Metadata = { title: "Team invite" };

function Message({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <>
      <h1 className="text-[24px] font-semibold tracking-[-0.015em] text-ink">{title}</h1>
      <div className="mt-2 text-[14px] text-ink-2">{children}</div>
    </>
  );
}

export default async function InvitePage({ params }: PageProps<"/invite/[token]">) {
  const { token } = await params;
  const user = await getCurrentUser();
  const [inv] =
    token.length <= 100
      ? await db()`
          select i.email, i.role, i.expires_at <= now() as expired, i.accepted_at, t.id as team_id, t.name as team_name, t.slug,
                 u.email as inviter_email, u.name as inviter_name
          from team_invites i join teams t on t.id = i.team_id left join users u on u.id = i.invited_by
          where i.token_hash = ${sha256(token)}`
      : [];

  if (!inv) {
    return (
      <Message title="This invite link isn't valid">
        It may have been revoked, or the link was copied incompletely. Ask a team owner or admin for a new one.
      </Message>
    );
  }
  if (inv.accepted_at) {
    return (
      <Message title="This invite was already used">
        Each invite link works once. Ask a team owner or admin for a new one.
        {user ? (
          <p className="mt-4">
            <Link href="/teams" className="font-medium text-ink underline underline-offset-4">
              See your teams
            </Link>
          </p>
        ) : null}
      </Message>
    );
  }
  if (inv.expired) {
    return (
      <Message title="This invite has expired">
        Invite links are valid for 7 days. Ask a team owner or admin of {inv.team_name} for a new one.
      </Message>
    );
  }

  const inviter = inv.inviter_name || inv.inviter_email || "A team member";
  const next = `/invite/${token}`;
  const intro = (
    <Message title={`Join ${inv.team_name}`}>
      <span className="font-medium text-ink">{inviter}</span> invited you to the team{" "}
      <span className="font-mono text-[13px] text-ink">{inv.slug}</span> as {inv.role === "admin" ? "an admin" : "a member"}.
      Members share its identity providers and domains and see the traffic on them.
      {inv.email ? (
        <p className="mt-2">
          This invite is for <span className="font-medium text-ink">{inv.email}</span>.
        </p>
      ) : null}
    </Message>
  );

  if (!user) {
    const canSignUp = (await signupCheck(next)).allowed;
    return (
      <>
        {intro}
        <div className="mt-6 flex flex-wrap gap-2">
          <Link href={withNext("/login", next)} className={buttonClass("primary")}>
            Sign in to accept
          </Link>
          {canSignUp ? (
            <Link href={withNext("/signup", next)} className={buttonClass("secondary")}>
              Create an account
            </Link>
          ) : null}
        </div>
        {!canSignUp ? (
          <p className="mt-4 text-[13px] text-muted">Accounts on this server are created by an administrator.</p>
        ) : null}
      </>
    );
  }

  const [member] = await db()`select 1 from team_members where team_id = ${inv.team_id} and user_id = ${user.id}`;
  if (member) {
    return (
      <Message title={`You're already in ${inv.team_name}`}>
        <Link href={`/teams/${inv.slug}`} className={buttonClass("primary", "md", "mt-4")}>
          Open the team
        </Link>
      </Message>
    );
  }

  if (inv.email && inv.email !== user.email.toLowerCase()) {
    return (
      <>
        {intro}
        <p className="mt-5 rounded-md border border-line-strong bg-surface px-3 py-2 text-[13.5px] text-ink-2">
          You&apos;re signed in as {user.email}. Only {inv.email} can accept this invite.
        </p>
        <form action={logoutAction} className="mt-4">
          <input type="hidden" name="next" value={next} />
          <button type="submit" className={buttonClass("secondary")}>
            Sign in with another account
          </button>
        </form>
      </>
    );
  }

  return (
    <>
      {intro}
      <p className="mt-4 text-[13px] text-muted">You&apos;ll join as {user.email}.</p>
      <AcceptInvite token={token} />
    </>
  );
}

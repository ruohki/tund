import "server-only";
import { sha256, usersExist } from "./auth";
import { db } from "./db";
import { getSettings } from "./settings";

const INVITE_PATH = /^\/invite\/([A-Za-z0-9_-]{20,100})$/;

export type SignupCheck = { allowed: true; inviteEmail: string | null } | { allowed: false; reason: string };

/**
 * Whether someone may create an account right now (docs/SPEC.md signup_mode):
 * open, invite (only arriving through a valid team invite link in `next`), or
 * closed. The very first account is always allowed (/setup).
 */
export async function signupCheck(next: string): Promise<SignupCheck> {
  if (!(await usersExist())) return { allowed: true, inviteEmail: null };
  const mode = (await getSettings()).signup_mode;
  if (mode === "open") return { allowed: true, inviteEmail: null };
  if (mode === "invite") {
    const m = INVITE_PATH.exec(next);
    if (m) {
      const [inv] = await db()`
        select email from team_invites
        where token_hash = ${sha256(m[1])} and accepted_at is null and expires_at > now()`;
      if (inv) return { allowed: true, inviteEmail: (inv.email as string) || null };
    }
    return { allowed: false, reason: "Accounts on this server are created through team invites. Ask a team owner for an invite link." };
  }
  return { allowed: false, reason: "Sign-up is closed on this server. Ask an administrator for an account." };
}

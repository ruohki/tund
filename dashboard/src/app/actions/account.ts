"use server";

import { refresh } from "next/cache";
import { redirect } from "next/navigation";
import { deleteAccount } from "@/lib/account-delete";
import { endSession, getSession, requireUser, type Session, type User } from "@/lib/auth";
import { audit } from "@/lib/audit";
import { BillingError, cancelSubscriptionsOf } from "@/lib/billing";
import { config } from "@/lib/config";
import { isOAuthProvider } from "@/lib/oauth-shared";
import { db } from "@/lib/db";
import { isDisposableEmail } from "@/lib/disposable-domains";
import { checkEmailToken, consumeEmailToken, issueEmailToken, rateLimited } from "@/lib/email-tokens";
import { accountDeletedEmail, emailChangeEmail, securityNoticeEmail, smtpConfigured, trySendMail } from "@/lib/mail";
import { hashPassword, MIN_PASSWORD_LENGTH, verifyPassword } from "@/lib/password";
import { getSettings } from "@/lib/settings";
import { checkEmail } from "@/lib/validate";
import type { FormState } from "./auth";

export async function updateProfileAction(_: FormState, fd: FormData): Promise<FormState> {
  const user = await requireUser();
  const name = String(fd.get("name") ?? "").trim().slice(0, 100);
  await db()`update users set name = ${name} where id = ${user.id}`;
  refresh();
  return { ok: "Profile saved." };
}

/**
 * For changes that could hand the account to someone else or end it: the
 * password, or, for accounts that sign in only with Google/GitHub or a
 * passkey, a sign-in within the last 15 minutes.
 */
async function reauthenticate(session: Session, fd: FormData): Promise<string | null> {
  const [row] = await db()`
    select u.password_hash, s.created_at > now() - interval '15 minutes' as fresh
    from sessions s join users u on u.id = s.user_id where s.id = ${session.id}`;
  if (!row) return "Your session expired. Sign in again.";
  if (row.password_hash === null) {
    return row.fresh ? null : "For your safety, sign out and sign in again, then try again within 15 minutes.";
  }
  if (rateLimited(`reauth:${session.user.id}`, 10, 15 * 60_000)) return "Too many attempts. Try again in a few minutes.";
  return (await verifyPassword(String(fd.get("password") ?? ""), row.password_hash)) ? null : "The password is incorrect.";
}

/** Switches the address; `confirmed` when the new address opened its link. */
async function applyEmailChange(user: User, email: string, confirmed: boolean): Promise<FormState> {
  try {
    // Links sent to the old address stop working: they are tied to it.
    await db()`update users set email = ${email}, email_verified_at = ${confirmed ? new Date() : null} where id = ${user.id}`;
  } catch (e) {
    if ((e as { code?: string }).code === "23505") return { error: "Another account already uses this email." };
    throw e;
  }
  await audit({ id: user.id, email }, "user.email_change", user.email, { to: email, confirmed });
  if (await smtpConfigured()) {
    const { instance_name } = await getSettings();
    await trySendMail(
      user.email,
      securityNoticeEmail(
        instance_name,
        "Your email address was changed",
        `The email address of your account is now ${email}. Sign-in and emails use it from now on.`,
        `${config().dashboardUrl}/settings`,
      ),
    );
  }
  return null;
}

/**
 * Changing the address: with email, a link to the new address that switches
 * when opened in this account; without, right away and unconfirmed.
 */
export async function changeEmailAction(_: FormState, fd: FormData): Promise<FormState> {
  const session = await getSession();
  if (!session) return { error: "Your session expired. Sign in again." };
  const user = session.user;
  const email = String(fd.get("email") ?? "").trim().toLowerCase();
  const err = checkEmail(email);
  if (err) return { error: err };
  if (email === user.email) return { error: "That's your address already." };
  const refused = await reauthenticate(session, fd);
  if (refused) return { error: refused };
  const s = await getSettings();
  if (s.block_disposable_emails && isDisposableEmail(email)) {
    return { error: "Disposable email addresses can't be used here. Use an address you'll keep." };
  }
  const [taken] = await db()`select 1 from users where email = ${email}`;
  if (taken) return { error: "Another account already uses this email." };
  if (!(await smtpConfigured())) {
    const res = await applyEmailChange(user, email, false);
    if (res?.error) return res;
    refresh();
    return { ok: `Your address is now ${email}.` };
  }
  if (rateLimited(`email-change:${user.id}`, 5, 60 * 60_000)) return { error: "Too many changes in the last hour. Try again later." };
  const token = await issueEmailToken(user.id, "email_change", email);
  if (!(await trySendMail(email, emailChangeEmail(s.instance_name, `${config().dashboardUrl}/change-email/${token}`)))) {
    return { error: "Sending the confirmation email failed. Try again later or ask an administrator." };
  }
  return { ok: `We sent a link to ${email}. Open it while signed in here; until then your address stays ${user.email}.` };
}

/** The button on /change-email/<token>: only the account that asked can switch. */
export async function confirmEmailChangeAction(_: FormState, fd: FormData): Promise<FormState> {
  const user = await requireUser();
  const token = String(fd.get("token") ?? "");
  const check = await checkEmailToken(token, "email_change");
  if (check.ok && check.userId !== user.id) {
    return { error: "This link belongs to another account. Sign in to the account that asked for the change, then open it again." };
  }
  const t = check.ok ? await consumeEmailToken(token, "email_change") : check;
  if (!t.ok) {
    return {
      error:
        t.reason === "expired"
          ? "This link has expired. Ask for a new one in your settings."
          : t.reason === "used"
            ? "This link was already used."
            : "This link isn't valid.",
    };
  }
  const res = await applyEmailChange(user, t.email, true);
  if (res?.error) return res;
  redirect("/settings?email_changed=1");
}

/** Settings → Delete account: everything goes, subscriptions end at once. */
export async function deleteOwnAccountAction(_: FormState, fd: FormData): Promise<FormState> {
  const session = await getSession();
  if (!session) return { error: "Your session expired. Sign in again." };
  const user = session.user;
  if (String(fd.get("confirm") ?? "").trim().toLowerCase() !== user.email) return { error: `Type ${user.email} to confirm.` };
  const refused = await reauthenticate(session, fd);
  if (refused) return { error: refused };
  if (user.isAdmin) {
    const [{ n }] = await db()`select count(*)::int as n from users where is_admin and disabled_at is null and id <> ${user.id}`;
    if (!n) return { error: "You're the only administrator. Make someone else an administrator first." };
  }
  try {
    await cancelSubscriptionsOf(user.id);
  } catch (err) {
    if (err instanceof BillingError) return { error: `${err.message} Your account wasn't deleted; try again or contact the operators.` };
    throw err;
  }
  const gone = await deleteAccount(user.id);
  if (gone) {
    await audit({ id: null, email: user.email }, "user.delete_self", user.email, {
      teams_deleted: gone.teamsDeleted,
      teams_handed_over: gone.teamsHandedOver,
    });
    if (await smtpConfigured()) await trySendMail(user.email, accountDeletedEmail((await getSettings()).instance_name));
  }
  await endSession();
  redirect("/login?deleted=1");
}

export async function changePasswordAction(_: FormState, fd: FormData): Promise<FormState> {
  const session = await getSession();
  if (!session) return { error: "Your session expired. Sign in again." };
  const current = String(fd.get("current") ?? "");
  const next = String(fd.get("next") ?? "");
  if (next.length < MIN_PASSWORD_LENGTH) return { error: `Use at least ${MIN_PASSWORD_LENGTH} characters.` };
  const [row] = await db()`select password_hash from users where id = ${session.user.id}`;
  if (!row) return { error: "Your session expired. Sign in again." };
  // Accounts created with Google or GitHub have no password yet: they set one without it.
  if (row.password_hash !== null && !(await verifyPassword(current, row.password_hash))) {
    return { error: "The current password is incorrect." };
  }
  await db()`update users set password_hash = ${await hashPassword(next)} where id = ${session.user.id}`;
  // Sign out everywhere else.
  await db()`delete from sessions where user_id = ${session.user.id} and id <> ${session.id}`;
  refresh();
  if (row.password_hash === null) return { ok: "Password set. You can now also sign in with your email and password." };
  return { ok: "Password changed. Other sessions were signed out." };
}

export async function revokeSessionAction(fd: FormData) {
  const session = await getSession();
  if (!session) return;
  const id = String(fd.get("id") ?? "");
  if (id === "others") {
    await db()`delete from sessions where user_id = ${session.user.id} and id <> ${session.id}`;
  } else if (/^[0-9a-f]{64}$/.test(id) && id !== session.id) {
    await db()`delete from sessions where user_id = ${session.user.id} and id = ${id}`;
  }
  refresh();
}

/** Disconnects a Google/GitHub account, as long as another way to sign in remains. */
export async function disconnectIdentityAction(fd: FormData) {
  const user = await requireUser();
  const provider = String(fd.get("provider") ?? "");
  if (!isOAuthProvider(provider)) return;
  const [row] = await db()`
    select u.password_hash is not null as has_password,
           (select count(*)::int from user_identities where user_id = u.id and provider <> ${provider})
             + (select count(*)::int from user_passkeys where user_id = u.id) as others
    from users u where u.id = ${user.id}`;
  if (!row?.has_password && !row?.others) redirect("/settings?oauth_error=last_method");
  await db()`delete from user_identities where user_id = ${user.id} and provider = ${provider}`;
  await audit({ id: user.id, email: user.email }, "user.identity_unlink", user.email, { provider });
  refresh();
}

"use server";

import { refresh } from "next/cache";
import { redirect } from "next/navigation";
import { requireUser, startSession, type User } from "@/lib/auth";
import { audit } from "@/lib/audit";
import { config } from "@/lib/config";
import { db } from "@/lib/db";
import { rateLimited } from "@/lib/email-tokens";
import { securityNoticeEmail, smtpConfigured, trySendMail } from "@/lib/mail";
import { decryptSecret, getSettings } from "@/lib/settings";
import {
  ALERT_AFTER,
  clearSecondFactorFailures,
  endFlow,
  failFlow,
  matchTotp,
  newRecoveryCodes,
  newTotpSecret,
  readFlow,
  recordSecondFactorFailure,
  sealTotpSecret,
  secondFactorLockedFor,
  startFlow,
  totpEnrollment,
  verifySecondFactor,
  verifyUserTotp,
} from "@/lib/two-factor";
import type { FormState } from "./auth";

/** Emails the account owner about a change to how they sign in (when email is set up). */
async function notifySecurity(user: { email: string }, heading: string, message: string) {
  if (!(await smtpConfigured())) return;
  const { instance_name } = await getSettings();
  await trySendMail(user.email, securityNoticeEmail(instance_name, heading, message, `${config().dashboardUrl}/settings#security`));
}

// --- Signing in -------------------------------------------------------------------

/** Second step of signing in: an authenticator code or a recovery code. */
export async function verifySecondFactorAction(_: FormState, fd: FormData): Promise<FormState> {
  const flow = await readFlow("second_factor");
  if (!flow?.userId) redirect("/login?two_factor=expired");
  const code = String(fd.get("code") ?? "").trim();
  if (!code) return { error: "Enter the code from your authenticator app, or a recovery code." };
  const locked = await secondFactorLockedFor(flow.userId);
  if (locked > 0) {
    const minutes = Math.ceil(locked / 60_000);
    return { error: `Too many wrong codes for this account. Try again in ${minutes} ${minutes === 1 ? "minute" : "minutes"}, or use a passkey.` };
  }
  const how = await verifySecondFactor(flow.userId, code);
  if (!how) {
    const failures = await recordSecondFactorFailure(flow.userId);
    if (failures === ALERT_AFTER) {
      const [owner] = await db()`select email from users where id = ${flow.userId}`;
      if (owner) {
        await notifySecurity(
          owner as { email: string },
          "Someone has your password",
          `Someone entered your password correctly but then failed the two-factor step ${ALERT_AFTER} times in a row. Change your password now.`,
        );
      }
    }
    const left = await failFlow(flow);
    if (left <= 0) {
      await endFlow("second_factor", flow);
      redirect("/login?two_factor=attempts");
    }
    return { error: `That code didn't work. ${left} ${left === 1 ? "attempt" : "attempts"} left.` };
  }
  const [user] = await db()`select id, email, disabled_at from users where id = ${flow.userId}`;
  await endFlow("second_factor", flow);
  await clearSecondFactorFailures(flow.userId);
  if (!user || user.disabled_at) redirect("/login");
  if (how === "recovery") {
    await audit({ id: user.id, email: user.email }, "user.recovery_code_used", user.email);
    await notifySecurity(user as { email: string }, "A recovery code was used", "Someone signed in to your account with one of your recovery codes. Each code works once.");
  }
  await startSession(user.id as string);
  redirect(flow.next);
}

export async function cancelSecondFactorAction() {
  await endFlow("second_factor", await readFlow("second_factor"));
  redirect("/login");
}

// --- Settings: authenticator app ------------------------------------------------------

export type TotpSetup = { ok: true; qr: string; secret: string; uri: string } | { ok: false; error: string };

/** Starts setting up an authenticator: a new secret, shown as QR code, confirmed by its first code. */
export async function startTotpSetupAction(): Promise<TotpSetup> {
  const user = await requireUser();
  const [row] = await db()`select totp_secret <> '' as on from users where id = ${user.id}`;
  if (row?.on) return { ok: false, error: "Two-factor authentication is already on." };
  const secret = newTotpSecret();
  await startFlow("totp_setup", { userId: user.id, data: sealTotpSecret(secret) });
  return { ok: true, ...(await totpEnrollment(secret, user.email)) };
}

export type CodesState = { error?: string | null; codes?: string[] } | null;

export async function confirmTotpSetupAction(_: CodesState, fd: FormData): Promise<CodesState> {
  const user = await requireUser();
  const flow = await readFlow("totp_setup");
  if (!flow || flow.userId !== user.id) return { error: "The setup timed out. Start again." };
  const secret = decryptSecret(flow.data);
  if (!secret) return { error: "The setup timed out. Start again." };
  const step = matchTotp(secret, String(fd.get("code") ?? ""));
  if (step === null) {
    const left = await failFlow(flow);
    if (left <= 0) {
      await endFlow("totp_setup", flow);
      return { error: "Too many wrong codes. Start the setup again." };
    }
    return { error: "That code didn't match. Check the time on your phone and enter the current code." };
  }
  const codes = await db().begin(async (tx) => {
    await tx`
      update users set totp_secret = ${sealTotpSecret(secret)}, totp_enabled_at = now(), totp_last_step = ${step}
      where id = ${user.id}`;
    return newRecoveryCodes(user.id, tx);
  });
  await endFlow("totp_setup", flow);
  await audit({ id: user.id, email: user.email }, "user.2fa_on", user.email);
  await notifySecurity(user, "Two-factor authentication is on", "Signing in to your account now also asks for a code from your authenticator app (or a passkey, or a recovery code).");
  return { codes };
}

async function checkCode(user: User, fd: FormData, recoveryAllowed: boolean): Promise<string | null> {
  const code = String(fd.get("code") ?? "").trim();
  if (!code) return "Enter a code from your authenticator app.";
  if (rateLimited(`2fa:${user.id}`, 10, 15 * 60_000)) return "Too many attempts. Wait a few minutes and try again.";
  const ok = recoveryAllowed ? Boolean(await verifySecondFactor(user.id, code)) : await verifyUserTotp(user.id, code);
  return ok ? null : "That code didn't work.";
}

/** Turns two-factor off; needs a current code (or a recovery code, for a lost phone). */
export async function disableTotpAction(_: FormState, fd: FormData): Promise<FormState> {
  const user = await requireUser();
  const problem = await checkCode(user, fd, true);
  if (problem) return { error: problem };
  await db().begin(async (tx) => {
    await tx`update users set totp_secret = '', totp_enabled_at = null, totp_last_step = 0 where id = ${user.id}`;
    await tx`delete from user_recovery_codes where user_id = ${user.id}`;
  });
  await audit({ id: user.id, email: user.email }, "user.2fa_off", user.email);
  await notifySecurity(user, "Two-factor authentication is off", "Signing in to your account no longer asks for a second step.");
  refresh();
  return { ok: "Two-factor authentication is off." };
}

/** New recovery codes; the old ones stop working. Needs an authenticator code. */
export async function regenerateRecoveryCodesAction(_: CodesState, fd: FormData): Promise<CodesState> {
  const user = await requireUser();
  const problem = await checkCode(user, fd, false);
  if (problem) return { error: problem };
  const codes = await newRecoveryCodes(user.id);
  await audit({ id: user.id, email: user.email }, "user.recovery_codes_new", user.email);
  refresh();
  return { codes };
}

// --- Settings: passkeys ----------------------------------------------------------------

/** Removes a passkey, unless it's the account's last way to sign in. */
export async function removePasskeyAction(fd: FormData) {
  const user = await requireUser();
  const id = String(fd.get("id") ?? "");
  if (!/^[0-9a-f-]{36}$/i.test(id)) return;
  const [row] = await db()`
    select u.password_hash is not null as has_password,
      (select count(*)::int from user_identities i where i.user_id = u.id) as identities,
      (select count(*)::int from user_passkeys p where p.user_id = u.id and p.id <> ${id}) as other_passkeys
    from users u where u.id = ${user.id}`;
  if (!row?.has_password && !row?.identities && !row?.other_passkeys) redirect("/settings?passkey_error=last_method#security");
  const [gone] = await db()`delete from user_passkeys where id = ${id} and user_id = ${user.id} returning name`;
  if (gone) {
    await audit({ id: user.id, email: user.email }, "user.passkey_remove", user.email, { name: gone.name });
    await notifySecurity(user, "A passkey was removed", `The passkey “${gone.name || "Passkey"}” can no longer sign in to your account.`);
  }
  refresh();
}

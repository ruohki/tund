"use server";

import { redirect } from "next/navigation";
import { db } from "@/lib/db";
import { endSession, safeNext, signIn, startSession, usersExist, withNext } from "@/lib/auth";
import { hashPassword, MIN_PASSWORD_LENGTH, verifyPassword } from "@/lib/password";
import { checkEmail } from "@/lib/validate";
import { signupCheck } from "@/lib/signup";
import { verificationRequired } from "@/lib/mail";
import { notifyAdminsOfSignup, sendVerification } from "@/lib/account-mail";
import { clientIp } from "@/lib/client-ip";
import { isDisposableEmail } from "@/lib/disposable-domains";
import { rateExceeded, rateRecord } from "@/lib/email-tokens";
import { getSettings } from "@/lib/settings";
import { verifyTurnstile } from "@/lib/turnstile";

export type FormState = { error?: string | null; ok?: string | null } | null;

const str = (fd: FormData, k: string) => String(fd.get(k) ?? "").trim();

// A fixed hash to compare against when the email is unknown, so timing doesn't reveal accounts.
const DUMMY_HASH = "scrypt$16384$8$1$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA";

export async function loginAction(_: FormState, fd: FormData): Promise<FormState> {
  const email = str(fd, "email").toLowerCase();
  const password = String(fd.get("password") ?? "");
  if (!email || !password) return { error: "Enter your email and password." };
  const [user] = await db()`select id, password_hash, disabled_at from users where email = ${email}`;
  const ok = await verifyPassword(password, user?.password_hash ?? DUMMY_HASH);
  if (!user || !ok) return { error: "Email or password is incorrect." };
  // Only revealed after the password matched, so it can't be used to probe for accounts.
  if (user.disabled_at) return { error: "This account has been disabled. Contact an administrator of this server." };
  redirect(await signIn(user.id, safeNext(fd.get("next"))));
}

type AccountOptions = {
  first: boolean;
  inviteEmail?: string | null;
  /** Runs after the form validated, before the account is created; returns an error to stop. */
  gate?: (email: string) => Promise<string | null>;
  onCreated?: () => void;
};

async function createAccount(fd: FormData, opts: AccountOptions): Promise<FormState> {
  const email = str(fd, "email").toLowerCase();
  const name = str(fd, "name").slice(0, 100);
  const password = String(fd.get("password") ?? "");
  const emailError = checkEmail(email);
  if (emailError) return { error: emailError };
  if (password.length < MIN_PASSWORD_LENGTH) return { error: `Use at least ${MIN_PASSWORD_LENGTH} characters for the password.` };
  if (opts.inviteEmail && opts.inviteEmail !== email) {
    return { error: `This invite is for ${opts.inviteEmail}. Sign up with that address to accept it.` };
  }
  const refused = opts.gate ? await opts.gate(email) : null;
  if (refused) return { error: refused };
  const hash = await hashPassword(password);
  // New accounts start unverified only while verification is enforced; the first admin never is.
  const mustVerify = !opts.first && (await verificationRequired());

  let userId: string | null = null;
  try {
    userId = await db().begin(async (tx) => {
      // Serialize the "first account becomes admin" decision.
      await tx`select pg_advisory_xact_lock(7461001)`;
      const [{ count }] = await tx`select count(*)::int as count from users`;
      if (opts.first && count > 0) return null;
      const [row] = await tx`
        insert into users (email, name, password_hash, is_admin, email_verified_at)
        values (${email}, ${name}, ${hash}, ${count === 0}, ${mustVerify && count > 0 ? null : new Date()})
        returning id`;
      return row.id as string;
    });
  } catch (err) {
    if ((err as { code?: string }).code === "23505") return { error: "An account with this email already exists." };
    throw err;
  }
  if (!userId) redirect(withNext("/login", fd.get("next")));
  opts.onCreated?.();
  if (mustVerify) await sendVerification(userId, email);
  if (!opts.first) await notifyAdminsOfSignup(userId, email);
  await startSession(userId);
  const next = safeNext(fd.get("next"));
  redirect(next !== "/" ? next : opts.first ? "/get-started" : "/");
}

export async function setupAction(_: FormState, fd: FormData): Promise<FormState> {
  if (await usersExist()) redirect(withNext("/login", fd.get("next")));
  return createAccount(fd, { first: true });
}

export async function signupAction(_: FormState, fd: FormData): Promise<FormState> {
  const check = await signupCheck(safeNext(fd.get("next")));
  if (!check.allowed) return { error: check.reason };
  const s = await getSettings();
  const rateKey = `signup-ip:${(await clientIp()) || "unknown"}`;
  return createAccount(fd, {
    first: false,
    inviteEmail: check.inviteEmail,
    // docs/SPEC.md "Abuse protection": terms, disposable addresses, Turnstile, sign-ups per IP and hour.
    gate: async (email) => {
      if (fd.get("terms") !== "on") return "Accept the Terms of Service and the Acceptable Use Policy to create an account.";
      // An invite names the address, so whoever invited them already chose it.
      if (s.block_disposable_emails && !check.inviteEmail && isDisposableEmail(email)) {
        return "Disposable email addresses can't be used here. Sign up with an address you'll keep.";
      }
      if (s.signup_rate_limit > 0 && rateExceeded(rateKey, s.signup_rate_limit, 60 * 60_000)) {
        return "Too many accounts were created from your network in the last hour. Try again later.";
      }
      return verifyTurnstile(fd);
    },
    onCreated: () => rateRecord(rateKey),
  });
}

export async function logoutAction(fd?: FormData) {
  await endSession();
  redirect(withNext("/login", fd?.get("next")));
}

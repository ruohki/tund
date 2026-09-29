"use server";

import { headers } from "next/headers";
import { redirect } from "next/navigation";
import { getCurrentUser, requireUser } from "@/lib/auth";
import { audit } from "@/lib/audit";
import { config } from "@/lib/config";
import { db } from "@/lib/db";
import { consumeEmailToken, issueEmailToken, rateLimited } from "@/lib/email-tokens";
import { resetPasswordEmail, smtpConfigured, trySendMail } from "@/lib/mail";
import { hashPassword, MIN_PASSWORD_LENGTH } from "@/lib/password";
import { getSettings } from "@/lib/settings";
import { sendVerification } from "@/lib/account-mail";
import { checkEmail } from "@/lib/validate";
import type { FormState } from "./auth";

async function clientIp() {
  const h = await headers();
  return (h.get("x-forwarded-for") ?? "").split(",")[0].trim() || h.get("x-real-ip") || "unknown";
}

const SENT = "If an account uses that address, we sent it a link to reset the password. It works for 1 hour.";

/** Always gives the same answer, so the form can't be used to find out which addresses have accounts. */
export async function forgotPasswordAction(_: FormState, fd: FormData): Promise<FormState> {
  const email = String(fd.get("email") ?? "").trim().toLowerCase();
  if (checkEmail(email)) return { error: "Enter the email address of your account." };
  if (!(await smtpConfigured())) return { error: "Password reset by email isn't available on this server. Ask an administrator." };
  const ip = await clientIp();
  if (rateLimited(`forgot-ip:${ip}`, 10, 15 * 60_000)) return { error: "Too many requests from your network. Try again in a few minutes." };
  // Per-address limit answers like a success, so it doesn't reveal anything either.
  if (rateLimited(`forgot-email:${email}`, 3, 60 * 60_000)) return { ok: SENT };
  const [user] = await db()`select id, email from users where email = ${email} and disabled_at is null`;
  if (user) {
    const token = await issueEmailToken(user.id, "reset");
    const { instance_name } = await getSettings();
    await trySendMail(user.email, resetPasswordEmail(instance_name, `${config().dashboardUrl}/reset-password/${token}`));
  }
  return { ok: SENT };
}

export async function resetPasswordAction(_: FormState, fd: FormData): Promise<FormState> {
  const token = String(fd.get("token") ?? "");
  const password = String(fd.get("password") ?? "");
  if (password.length < MIN_PASSWORD_LENGTH) return { error: `Use at least ${MIN_PASSWORD_LENGTH} characters.` };
  if (password !== String(fd.get("confirm") ?? "")) return { error: "The two passwords don't match." };
  const ip = await clientIp();
  if (rateLimited(`reset-ip:${ip}`, 20, 15 * 60_000)) return { error: "Too many attempts. Try again in a few minutes." };
  const hash = await hashPassword(password);
  const t = await consumeEmailToken(token, "reset");
  if (!t.ok) {
    return {
      error:
        t.reason === "expired"
          ? "This link has expired. Ask for a new one."
          : t.reason === "used"
            ? "This link was already used. Ask for a new one if you still need to."
            : "This link isn't valid. Ask for a new one.",
    };
  }
  await db().begin(async (tx) => {
    await tx`update users set password_hash = ${hash} where id = ${t.userId}`;
    // Signs out every browser, including a possible attacker's.
    await tx`delete from sessions where user_id = ${t.userId}`;
    // Following an emailed link proves the address.
    await tx`update users set email_verified_at = coalesce(email_verified_at, now()) where id = ${t.userId}`;
  });
  await audit({ id: t.userId, email: t.email }, "auth.password_reset", t.email);
  redirect("/login?reset=1");
}

export async function verifyEmailAction(_: FormState, fd: FormData): Promise<FormState> {
  const t = await consumeEmailToken(String(fd.get("token") ?? ""), "verify");
  if (!t.ok) {
    return {
      error:
        t.reason === "expired"
          ? "This link has expired. Sign in and send a new one from the banner."
          : t.reason === "used"
            ? "This link was already used."
            : "This link isn't valid.",
    };
  }
  await db()`update users set email_verified_at = coalesce(email_verified_at, now()) where id = ${t.userId}`;
  const user = await getCurrentUser();
  redirect(user ? "/?verified=1" : "/login?next=%2F");
}

export async function resendVerificationAction(): Promise<FormState> {
  const user = await requireUser();
  if (user.emailVerified) return { ok: "Your address is already confirmed." };
  if (rateLimited(`verify:${user.id}`, 3, 10 * 60_000)) return { error: "We just sent a few links. Wait a few minutes before asking again." };
  const sent = await sendVerification(user.id, user.email);
  return sent ? { ok: `Sent a new link to ${user.email}.` } : { error: "Sending the email failed. Try again later or ask an administrator." };
}

"use server";

import { refresh } from "next/cache";
import { redirect } from "next/navigation";
import { getSession, requireUser } from "@/lib/auth";
import { audit } from "@/lib/audit";
import { isOAuthProvider } from "@/lib/oauth-shared";
import { db } from "@/lib/db";
import { hashPassword, MIN_PASSWORD_LENGTH, verifyPassword } from "@/lib/password";
import { checkEmail } from "@/lib/validate";
import type { FormState } from "./auth";

export async function updateProfileAction(_: FormState, fd: FormData): Promise<FormState> {
  const user = await requireUser();
  const name = String(fd.get("name") ?? "").trim().slice(0, 100);
  const email = String(fd.get("email") ?? "").trim().toLowerCase();
  const err = checkEmail(email);
  if (err) return { error: err };
  try {
    await db()`update users set name = ${name}, email = ${email} where id = ${user.id}`;
  } catch (e) {
    if ((e as { code?: string }).code === "23505") return { error: "Another account already uses this email." };
    throw e;
  }
  refresh();
  return { ok: "Profile saved." };
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
           (select count(*)::int from user_identities where user_id = u.id and provider <> ${provider}) as others
    from users u where u.id = ${user.id}`;
  if (!row?.has_password && !row?.others) redirect("/settings?oauth_error=last_method");
  await db()`delete from user_identities where user_id = ${user.id} and provider = ${provider}`;
  await audit({ id: user.id, email: user.email }, "user.identity_unlink", user.email, { provider });
  refresh();
}

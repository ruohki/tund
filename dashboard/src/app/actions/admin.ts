"use server";

import { refresh } from "next/cache";
import { redirect } from "next/navigation";
import { requireAdmin, type User } from "@/lib/auth";
import { audit } from "@/lib/audit";
import { config } from "@/lib/config";
import { db, notify } from "@/lib/db";
import { issueEmailToken } from "@/lib/email-tokens";
import { internalApi, InternalApiError } from "@/lib/internal";
import { resetPasswordEmail, sendMail, smtpConfigured, testEmail } from "@/lib/mail";
import { hashPassword, MIN_PASSWORD_LENGTH } from "@/lib/password";
import { isUuid } from "@/lib/requests";
import {
  coerce,
  encryptSecret,
  getSettings,
  SETTING_DEFS,
  storedKeys,
  writeSettings,
  type EditableKey,
  type OAuthProviderSettings,
  type SmtpSecurity,
  type SmtpSettings,
} from "@/lib/settings";
import { isOAuthProvider, PROVIDER_LABEL } from "@/lib/oauth-shared";
import { removeDomain } from "@/lib/static-hostnames";
import { checkEmail } from "@/lib/validate";
import type { FormState } from "./auth";

const str = (fd: FormData, k: string) => String(fd.get(k) ?? "").trim();
const actorOf = (u: User) => ({ id: u.id, email: u.email });

async function targetUser(id: string) {
  if (!isUuid(id)) return null;
  const [u] = await db()`select id, email, is_admin, disabled_at, trusted, email_verified_at from users where id = ${id}`;
  return u ?? null;
}

// --- users -----------------------------------------------------------------

export async function createUserAction(_: FormState, fd: FormData): Promise<FormState> {
  const admin = await requireAdmin();
  const email = str(fd, "email").toLowerCase();
  const name = str(fd, "name").slice(0, 100);
  const password = String(fd.get("password") ?? "");
  const makeAdmin = fd.get("admin") === "on";
  const err = checkEmail(email);
  if (err) return { error: err };
  if (password.length < MIN_PASSWORD_LENGTH) return { error: `Use at least ${MIN_PASSWORD_LENGTH} characters for the password.` };
  try {
    await db()`
      insert into users (email, name, password_hash, is_admin, email_verified_at)
      values (${email}, ${name}, ${await hashPassword(password)}, ${makeAdmin}, now())`;
  } catch (e) {
    if ((e as { code?: string }).code === "23505") return { error: "An account with this email already exists." };
    throw e;
  }
  await audit(actorOf(admin), "user.create", email, { admin: makeAdmin });
  refresh();
  return { ok: `Created ${email}. Share the password with them; they can change it under Settings.` };
}

type Flag = "disabled" | "trusted" | "admin" | "verified" | "flagged";

/** One entry point for the account switches on the user pages; every change is audited. */
export async function setUserFlagAction(_: FormState, fd: FormData): Promise<FormState> {
  const admin = await requireAdmin();
  const flag = str(fd, "flag") as Flag;
  const on = str(fd, "value") === "on";
  const u = await targetUser(str(fd, "id"));
  if (!u) return { error: "Unknown user." };
  if (u.id === admin.id && (flag === "disabled" || flag === "admin")) return { error: "You can't change that on your own account." };
  let action: string;
  let details: Record<string, unknown> = {};
  switch (flag) {
    case "disabled":
      await db()`update users set disabled_at = ${on ? new Date() : null} where id = ${u.id}`;
      if (on) {
        await db()`delete from sessions where user_id = ${u.id}`;
        await notify("tund_config", { kind: "user_disabled", id: u.id });
      } else {
        await notify("tund_config", { kind: "user_updated", id: u.id });
      }
      action = on ? "user.disable" : "user.enable";
      break;
    case "trusted":
      await db()`update users set trusted = ${on} where id = ${u.id}`;
      await notify("tund_config", { kind: "user_updated", id: u.id });
      action = on ? "user.trust" : "user.untrust";
      break;
    case "admin":
      await db()`update users set is_admin = ${on} where id = ${u.id}`;
      await notify("tund_config", { kind: "user_updated", id: u.id });
      action = on ? "user.admin" : "user.unadmin";
      break;
    case "flagged": {
      // Flags are notes for admins (the edge sets them too); they don't restrict the account.
      const reason = str(fd, "reason").slice(0, 300) || "flagged by an administrator";
      await db()`update users set flagged_at = ${on ? new Date() : null}, flag_reason = ${on ? reason : ""} where id = ${u.id}`;
      action = on ? "user.flag" : "user.unflag";
      if (on) details = { reason };
      break;
    }
    case "verified":
      if (!on) return { error: "Verification can't be undone." };
      await db()`update users set email_verified_at = coalesce(email_verified_at, now()) where id = ${u.id}`;
      await notify("tund_config", { kind: "user_updated", id: u.id });
      action = "user.verify";
      break;
    default:
      return { error: "Unknown setting." };
  }
  await audit(actorOf(admin), action, u.email, details);
  refresh();
  return { ok: "Saved." };
}

export async function sendResetEmailAction(_: FormState, fd: FormData): Promise<FormState> {
  const admin = await requireAdmin();
  const u = await targetUser(str(fd, "id"));
  if (!u) return { error: "Unknown user." };
  if (!(await smtpConfigured())) return { error: "Email isn't configured (Admin → Email)." };
  const token = await issueEmailToken(u.id, "reset");
  const { instance_name } = await getSettings();
  try {
    await sendMail(u.email, resetPasswordEmail(instance_name, `${config().dashboardUrl}/reset-password/${token}`));
  } catch (err) {
    return { error: `Sending failed: ${err instanceof Error ? err.message : String(err)}` };
  }
  await audit(actorOf(admin), "user.reset_password", u.email);
  return { ok: `Sent a reset link to ${u.email}. It works for 1 hour.` };
}

export async function deleteUserAction(fd: FormData) {
  const admin = await requireAdmin();
  const u = await targetUser(str(fd, "id"));
  if (!u || u.id === admin.id) return;
  await db()`delete from users where id = ${u.id}`;
  await notify("tund_config", { kind: "user_disabled", id: u.id });
  await audit(actorOf(admin), "user.delete", u.email);
  if (fd.get("redirect") === "list") redirect("/admin/users");
  refresh();
}

// --- tunnels, teams, domains ---------------------------------------------------

export async function adminStopTunnelAction(_: FormState, fd: FormData): Promise<FormState> {
  const admin = await requireAdmin();
  const id = str(fd, "tunnel_id");
  const reason = str(fd, "reason").slice(0, 200) || "stopped by an administrator";
  if (!isUuid(id)) return { error: "Unknown tunnel." };
  const [t] = await db()`
    select t.hostname, u.email from tunnels t join users u on u.id = t.user_id where t.id = ${id} and t.ended_at is null`;
  if (!t) return { ok: "The tunnel is already offline." };
  try {
    await internalApi("/internal/admin/tunnels/stop", { tunnel_id: id, reason });
  } catch (err) {
    return { error: err instanceof InternalApiError ? err.message : "Could not stop the tunnel." };
  }
  await audit(actorOf(admin), "tunnel.stop", t.hostname, { owner: t.email, reason });
  refresh();
  return { ok: "Stopped." };
}

export async function adminDeleteTeamAction(fd: FormData) {
  const admin = await requireAdmin();
  const id = str(fd, "id");
  if (!isUuid(id)) return;
  const res = await db().begin(async (tx) => {
    const [t] = await tx`select slug from teams where id = ${id}`;
    if (!t) return null;
    const domains = await tx`select id from domains where team_id = ${id}`;
    await tx`delete from teams where id = ${id}`;
    return { slug: t.slug as string, domains: domains.map((d) => d.id as string) };
  });
  if (!res) return;
  await notify("tund_config", { kind: "team", id });
  for (const d of res.domains) await notify("tund_config", { kind: "domain", id: d });
  await audit(actorOf(admin), "team.delete", res.slug, { domains: res.domains.length });
  refresh();
}

export async function adminDeleteDomainAction(fd: FormData) {
  const admin = await requireAdmin();
  const id = str(fd, "id");
  if (!isUuid(id)) return;
  const [d] = await db()`
    select d.id, d.hostname, d.user_id, d.team_id, u.email from domains d join users u on u.id = d.user_id where d.id = ${id}`;
  if (!d) return;
  // Delete as its owner, so a personal default hands over to the next static hostname.
  await removeDomain({ id: d.user_id } as User, d.id, { teamId: d.team_id ?? null });
  await audit(actorOf(admin), "domain.delete", d.hostname, { owner: d.email, team: Boolean(d.team_id) });
  refresh();
}

// --- settings --------------------------------------------------------------

function same(a: unknown, b: unknown) {
  return JSON.stringify(a) === JSON.stringify(b);
}

export async function saveSettingsAction(_: FormState, fd: FormData): Promise<FormState> {
  const admin = await requireAdmin();
  const current = await getSettings();
  const stored = await storedKeys();
  const changes: Partial<Record<EditableKey, unknown>> = {};
  for (const def of SETTING_DEFS) {
    if (!fd.has(`present:${def.key}`)) continue;
    // Secrets are write-only: an empty field keeps the stored value unless "remove" is ticked.
    if (def.kind === "secret" && !String(fd.get(def.key) ?? "") && fd.get(`clear:${def.key}`) !== "on") continue;
    const raw = def.kind === "bool" ? fd.get(def.key) === "on" : fd.get(def.key);
    const value = coerce(def.key, raw);
    if (value === null) {
      const range = def.min !== undefined ? ` (${def.min}–${def.max})` : "";
      return { error: `“${def.label}” needs a valid value${range}.` };
    }
    if (same(value, current[def.key])) continue;
    // Matching the environment default removes the override instead of pinning it.
    changes[def.key] = same(value, def.fallback()) && stored.has(def.key) ? undefined : value;
  }
  const keys = Object.keys(changes);
  if (!keys.length) return { ok: "Nothing changed." };
  await writeSettings(changes, admin.id);
  await audit(actorOf(admin), "settings.update", keys.join(", "), { keys });
  refresh();
  return { ok: `Saved ${keys.length} ${keys.length === 1 ? "setting" : "settings"}. tund-server picks them up right away.` };
}

/** Bound per field from the settings form: `resetSettingAction.bind(null, key)`. */
export async function resetSettingAction(key: string) {
  const admin = await requireAdmin();
  if (!SETTING_DEFS.some((d) => d.key === key)) return;
  await writeSettings({ [key as EditableKey]: undefined }, admin.id);
  await audit(actorOf(admin), "settings.update", key, { keys: [key], reset: true });
  refresh();
}

// --- email -----------------------------------------------------------------

export async function saveSmtpAction(_: FormState, fd: FormData): Promise<FormState> {
  const admin = await requireAdmin();
  const current = (await getSettings()).smtp;
  const host = str(fd, "host");
  const port = Number.parseInt(str(fd, "port"), 10);
  const security = str(fd, "security") as SmtpSecurity;
  const fromEmail = str(fd, "from_email").toLowerCase();
  if (!host) return { error: "Enter the SMTP server's host name." };
  if (!Number.isInteger(port) || port < 1 || port > 65535) return { error: "Enter a port between 1 and 65535 (usually 587 or 465)." };
  if (!["starttls", "tls", "none"].includes(security)) return { error: "Choose how to secure the connection." };
  if (checkEmail(fromEmail)) return { error: "Enter the address emails are sent from." };
  const password = String(fd.get("password") ?? "");
  const smtp: SmtpSettings = {
    host,
    port,
    security,
    username: str(fd, "username"),
    // Write-only: empty keeps the stored password, "clear" removes it.
    password_enc: fd.get("clear_password") === "on" ? "" : password ? encryptSecret(password) : (current?.password_enc ?? ""),
    from_email: fromEmail,
    from_name: str(fd, "from_name").slice(0, 100),
  };
  await writeSettings({ smtp }, admin.id);
  await audit(actorOf(admin), "settings.update", "smtp", { keys: ["smtp"], password_changed: Boolean(password) });
  refresh();
  return { ok: "Saved. Send a test email to check it works." };
}

export async function removeSmtpAction() {
  const admin = await requireAdmin();
  await writeSettings({ smtp: undefined }, admin.id);
  await audit(actorOf(admin), "settings.update", "smtp", { keys: ["smtp"], removed: true });
  refresh();
}

export async function sendTestEmailAction(_: FormState, fd: FormData): Promise<FormState> {
  const admin = await requireAdmin();
  const to = str(fd, "to").toLowerCase();
  if (checkEmail(to)) return { error: "Enter the address to send the test to." };
  const { instance_name } = await getSettings();
  try {
    await sendMail(to, testEmail(instance_name, config().dashboardUrl));
  } catch (err) {
    const e = err as { message?: string; response?: string; code?: string };
    await audit(actorOf(admin), "smtp.test", to, { ok: false });
    // The server's own words are the most useful thing to show here.
    return { error: [e.code, e.response ?? e.message ?? String(err)].filter(Boolean).join(": ") };
  }
  await audit(actorOf(admin), "smtp.test", to, { ok: true });
  return { ok: `Sent a test email to ${to}.` };
}

// --- bandwidth overrides ---------------------------------------------------------

function override(fd: FormData, name: string, max: number): number | null | "invalid" {
  const mode = str(fd, `${name}_mode`);
  if (mode === "inherit") return null;
  if (mode === "unlimited") return 0;
  const n = Number.parseInt(str(fd, name).replace(/[_,\s]/g, ""), 10);
  return Number.isInteger(n) && n > 0 && n <= max ? n : "invalid";
}

/** Per-user bandwidth/transfer overrides: inherit the default (NULL), unlimited (0) or a value. */
export async function setUserLimitsAction(_: FormState, fd: FormData): Promise<FormState> {
  const admin = await requireAdmin();
  const u = await targetUser(str(fd, "id"));
  if (!u) return { error: "Unknown user." };
  const bandwidth = override(fd, "bandwidth", 100_000_000);
  const transfer = override(fd, "transfer", 10_000_000);
  if (bandwidth === "invalid") return { error: "Enter a speed limit in kbit/s (a whole number above 0)." };
  if (transfer === "invalid") return { error: "Enter a monthly transfer in GB (a whole number above 0)." };
  await db()`update users set bandwidth_kbps = ${bandwidth}, transfer_quota_gb = ${transfer} where id = ${u.id}`;
  await notify("tund_config", { kind: "user_updated", id: u.id });
  await audit(actorOf(admin), "user.limits", u.email, { bandwidth_kbps: bandwidth, transfer_quota_gb: transfer });
  refresh();
  return { ok: "Limits saved. They apply to live tunnels right away." };
}

// --- per-account features ----------------------------------------------------------

/**
 * Per-user feature override: custom domains or TCP and TLS tunnels. NULL follows
 * the instance setting. Turning one off disconnects the account's affected tunnels.
 */
export async function setUserFeatureAction(_: FormState, fd: FormData): Promise<FormState> {
  const admin = await requireAdmin();
  const u = await targetUser(str(fd, "id"));
  if (!u) return { error: "Unknown user." };
  const mode = str(fd, "mode");
  if (mode !== "inherit" && mode !== "on" && mode !== "off") return { error: "Choose default, on or off." };
  const value = mode === "inherit" ? null : mode === "on";
  const feature = str(fd, "feature");
  if (feature === "custom_domains") await db()`update users set custom_domains = ${value} where id = ${u.id}`;
  else if (feature === "passthrough") await db()`update users set passthrough = ${value} where id = ${u.id}`;
  else return { error: "Unknown feature." };
  await notify("tund_config", { kind: "user_updated", id: u.id });
  await audit(actorOf(admin), `user.${feature}`, u.email, { [feature]: value });
  refresh();
  return { ok: "Saved. It applies to live tunnels right away." };
}

// --- sign-in providers (Google, GitHub) -------------------------------------------

/** Saves one provider under the `oauth` setting; the secret is write-only like the SMTP password. */
export async function saveOAuthProviderAction(_: FormState, fd: FormData): Promise<FormState> {
  const admin = await requireAdmin();
  const provider = str(fd, "provider");
  if (!isOAuthProvider(provider)) return { error: "Unknown provider." };
  const all = (await getSettings()).oauth;
  const current = all[provider];
  const secret = str(fd, "client_secret");
  const next: OAuthProviderSettings = {
    enabled: fd.get("enabled") === "on",
    client_id: str(fd, "client_id").slice(0, 300),
    client_secret_enc: secret ? encryptSecret(secret) : (current?.client_secret_enc ?? ""),
  };
  if (next.enabled && (!next.client_id || !next.client_secret_enc)) {
    return { error: `Enter the client ID and secret from ${PROVIDER_LABEL[provider]} to turn it on.` };
  }
  await writeSettings({ oauth: { ...all, [provider]: next } }, admin.id);
  await audit(actorOf(admin), "settings.update", `oauth.${provider}`, { enabled: next.enabled, secret_changed: Boolean(secret) });
  refresh();
  return {
    ok: next.enabled
      ? `Saved. “Continue with ${PROVIDER_LABEL[provider]}” is on the sign-in and sign-up pages now.`
      : `Saved. ${PROVIDER_LABEL[provider]} sign-in is off.`,
  };
}

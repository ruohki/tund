import "server-only";
import { createHash, createHmac, randomBytes, randomInt, timingSafeEqual } from "node:crypto";
import { cookies } from "next/headers";
import QRCode from "qrcode";
import type { Sql, TransactionSql } from "postgres";
import { db } from "./db";
import { config } from "./config";
import { decryptSecret, encryptSecret, getSettings } from "./settings";
import { sessionCookieSecure } from "./session-cookie";

// Two-factor authentication (TOTP, RFC 6238, with recovery codes) and the
// short-lived "auth flows" behind signing in with it and with passkeys
// (docs/SPEC.md "Two-factor authentication and passkeys").

const sha256 = (s: string) => createHash("sha256").update(s).digest("hex");

// --- TOTP ----------------------------------------------------------------------

const PERIOD = 30;
const DIGITS = 6;
const B32 = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";

export function base32Encode(buf: Buffer): string {
  let bits = 0;
  let value = 0;
  let out = "";
  for (const byte of buf) {
    value = (value << 8) | byte;
    bits += 8;
    while (bits >= 5) {
      out += B32[(value >>> (bits - 5)) & 31];
      bits -= 5;
    }
  }
  if (bits > 0) out += B32[(value << (5 - bits)) & 31];
  return out;
}

export function base32Decode(s: string): Buffer {
  const clean = s.toUpperCase().replace(/[\s=-]/g, "");
  let bits = 0;
  let value = 0;
  const out: number[] = [];
  for (const ch of clean) {
    const idx = B32.indexOf(ch);
    if (idx < 0) throw new Error("invalid base32");
    value = (value << 5) | idx;
    bits += 5;
    if (bits >= 8) {
      out.push((value >>> (bits - 8)) & 255);
      bits -= 8;
    }
  }
  return Buffer.from(out);
}

/** The TOTP code of a base32 secret for a time step (HMAC-SHA1, 6 digits). */
export function totpCode(secret: string, step: number): string {
  const counter = Buffer.alloc(8);
  counter.writeBigUInt64BE(BigInt(step));
  const mac = createHmac("sha1", base32Decode(secret)).update(counter).digest();
  const offset = mac[mac.length - 1] & 15;
  const n = (mac.readUInt32BE(offset) & 0x7fffffff) % 10 ** DIGITS;
  return n.toString().padStart(DIGITS, "0");
}

const currentStep = () => Math.floor(Date.now() / 1000 / PERIOD);

/** The time step a code belongs to (now, or one step either side for clock drift), or null. */
export function matchTotp(secret: string, code: string, now = currentStep()): number | null {
  const clean = code.replace(/\s/g, "");
  if (!/^\d{6}$/.test(clean)) return null;
  for (const step of [now, now - 1, now + 1]) {
    const expected = totpCode(secret, step);
    if (timingSafeEqual(Buffer.from(expected), Buffer.from(clean))) return step;
  }
  return null;
}

export function newTotpSecret(): string {
  return base32Encode(randomBytes(20));
}

/** otpauth:// URI and a QR code (data URL) for authenticator apps. */
export async function totpEnrollment(secret: string, email: string) {
  const { instance_name } = await getSettings();
  const issuer = instance_name || config().dashboardHost;
  const label = `${encodeURIComponent(issuer)}:${encodeURIComponent(email)}`;
  const uri = `otpauth://totp/${label}?secret=${secret}&issuer=${encodeURIComponent(issuer)}&algorithm=SHA1&digits=${DIGITS}&period=${PERIOD}`;
  const qr = await QRCode.toDataURL(uri, { margin: 1, width: 200, errorCorrectionLevel: "M" });
  return { uri, qr, secret: secret.replace(/(.{4})/g, "$1 ").trim() };
}

export const sealTotpSecret = (secret: string) => encryptSecret(secret);

/**
 * Checks a code against the user's authenticator. A code (time step) is
 * accepted once: the step is recorded so the same code can't be replayed.
 */
export async function verifyUserTotp(userId: string, code: string): Promise<boolean> {
  const [row] = await db()`select totp_secret, totp_last_step from users where id = ${userId}`;
  const secret = row?.totp_secret ? decryptSecret(row.totp_secret as string) : null;
  if (!secret) return false;
  const step = matchTotp(secret, code);
  if (step === null || step <= Number(row.totp_last_step)) return false;
  const res = await db()`update users set totp_last_step = ${step} where id = ${userId} and totp_last_step < ${step}`;
  return res.count === 1;
}

// --- Recovery codes ------------------------------------------------------------

const RECOVERY_COUNT = 10;
const RECOVERY_ALPHABET = "abcdefghjkmnpqrstuvwxyz23456789";

const normalizeRecovery = (code: string) => code.toLowerCase().replace(/[^a-z0-9]/g, "");

/** Replaces the user's recovery codes; returns the new ones (shown once). */
export async function newRecoveryCodes(userId: string, tx: Sql | TransactionSql = db()): Promise<string[]> {
  const codes = Array.from({ length: RECOVERY_COUNT }, () => {
    const chars = Array.from({ length: 10 }, () => RECOVERY_ALPHABET[randomInt(RECOVERY_ALPHABET.length)]).join("");
    return `${chars.slice(0, 5)}-${chars.slice(5)}`;
  });
  await tx`delete from user_recovery_codes where user_id = ${userId}`;
  for (const c of codes) {
    await tx`insert into user_recovery_codes (user_id, code_hash) values (${userId}, ${sha256(normalizeRecovery(c))})`;
  }
  return codes;
}

/** Uses up a recovery code; false when it doesn't exist or was used. */
export async function redeemRecoveryCode(userId: string, code: string): Promise<boolean> {
  const clean = normalizeRecovery(code);
  if (clean.length !== 10) return false;
  const res = await db()`
    update user_recovery_codes set used_at = now()
    where user_id = ${userId} and code_hash = ${sha256(clean)} and used_at is null`;
  return res.count === 1;
}

/** A 6-digit authenticator code or a recovery code. */
export async function verifySecondFactor(userId: string, code: string): Promise<"totp" | "recovery" | null> {
  if (/^\s*\d{3}\s?\d{3}\s*$/.test(code)) return (await verifyUserTotp(userId, code)) ? "totp" : null;
  return (await redeemRecoveryCode(userId, code)) ? "recovery" : null;
}

export type TwoFactorStatus = {
  totp: boolean;
  enabledAt: Date | null;
  recoveryLeft: number;
  passkeys: number;
};

export async function twoFactorStatus(userId: string): Promise<TwoFactorStatus> {
  const [row] = await db()`
    select totp_secret <> '' as totp, totp_enabled_at,
      (select count(*)::int from user_recovery_codes r where r.user_id = u.id and r.used_at is null) as recovery_left,
      (select count(*)::int from user_passkeys p where p.user_id = u.id) as passkeys
    from users u where u.id = ${userId}`;
  return {
    totp: Boolean(row?.totp),
    enabledAt: (row?.totp_enabled_at as Date) ?? null,
    recoveryLeft: Number(row?.recovery_left ?? 0),
    passkeys: Number(row?.passkeys ?? 0),
  };
}

export async function totpEnabled(userId: string): Promise<boolean> {
  const [row] = await db()`select totp_secret <> '' as on from users where id = ${userId}`;
  return Boolean(row?.on);
}

// --- Auth flows ----------------------------------------------------------------

/**
 * second_factor: password (or Google/GitHub) accepted, code still missing.
 * passkey_login / passkey_register: a WebAuthn challenge waiting for its answer.
 * totp_setup: a new secret waiting for its first code.
 */
export type FlowPurpose = "second_factor" | "passkey_login" | "passkey_register" | "totp_setup";

const FLOW_TTL: Record<FlowPurpose, number> = {
  second_factor: 10 * 60_000,
  passkey_login: 5 * 60_000,
  passkey_register: 5 * 60_000,
  totp_setup: 15 * 60_000,
};

/** Wrong codes allowed per sign-in before the password has to be entered again. */
export const SECOND_FACTOR_ATTEMPTS = 5;

function flowCookie(purpose: FlowPurpose) {
  return `${sessionCookieSecure() ? "__Host-" : ""}tund_${purpose}`;
}

export type Flow = { id: string; userId: string | null; data: string; next: string; attempts: number };

/** Starts a flow and sets its cookie. Server Actions and Route Handlers only. */
export async function startFlow(purpose: FlowPurpose, opts: { userId?: string | null; data?: string; next?: string } = {}) {
  const token = randomBytes(32).toString("base64url");
  const ttl = FLOW_TTL[purpose];
  await db()`delete from auth_flows where expires_at < now()`;
  await db()`
    insert into auth_flows (id, purpose, user_id, data, next, expires_at)
    values (${sha256(token)}, ${purpose}, ${opts.userId ?? null}, ${opts.data ?? ""}, ${opts.next ?? "/"}, ${new Date(Date.now() + ttl)})`;
  (await cookies()).set(flowCookie(purpose), token, {
    httpOnly: true,
    secure: sessionCookieSecure(),
    sameSite: "lax",
    path: "/",
    maxAge: Math.floor(ttl / 1000),
  });
}

/** The current flow of a purpose (from its cookie), or null when missing or expired. */
export async function readFlow(purpose: FlowPurpose): Promise<Flow | null> {
  const token = (await cookies()).get(flowCookie(purpose))?.value;
  if (!token) return null;
  const [row] = await db()`
    select id, user_id, data, next, attempts from auth_flows
    where id = ${sha256(token)} and purpose = ${purpose} and expires_at > now()`;
  return row
    ? { id: row.id as string, userId: (row.user_id as string) ?? null, data: row.data as string, next: row.next as string, attempts: row.attempts as number }
    : null;
}

/** Counts a failed attempt; returns how many are left. */
export async function failFlow(flow: Flow): Promise<number> {
  const [row] = await db()`update auth_flows set attempts = attempts + 1 where id = ${flow.id} returning attempts`;
  return SECOND_FACTOR_ATTEMPTS - Number(row?.attempts ?? SECOND_FACTOR_ATTEMPTS);
}

/** Ends a flow: deletes the row and clears the cookie. */
export async function endFlow(purpose: FlowPurpose, flow?: Flow | null) {
  if (flow) await db()`delete from auth_flows where id = ${flow.id}`;
  (await cookies()).set(flowCookie(purpose), "", {
    httpOnly: true,
    secure: sessionCookieSecure(),
    sameSite: "lax",
    path: "/",
    maxAge: 0,
  });
}

// --- Passkeys (WebAuthn relying party) ------------------------------------------

/** The relying party: the dashboard host, and the exact origin assertions must come from. */
export async function relyingParty() {
  const c = config();
  const { instance_name } = await getSettings();
  return { rpID: c.dashboardHost, rpName: instance_name || c.dashboardHost, origin: c.dashboardUrl };
}

// --- Lockout -------------------------------------------------------------------

/** Wrong codes in a row before the second step locks; each further 10 double the wait. */
const LOCK_AFTER = 10;
const LOCK_BASE_MS = 15 * 60_000;
const LOCK_MAX_MS = 24 * 60 * 60_000;
/** The owner is emailed at this many wrong codes in a row. */
export const ALERT_AFTER = 5;

function lockMs(failures: number): number {
  if (failures < LOCK_AFTER) return 0;
  return Math.min(LOCK_MAX_MS, LOCK_BASE_MS * 2 ** Math.floor((failures - LOCK_AFTER) / LOCK_AFTER));
}

/** Milliseconds until the user may try a second-step code again (0 = now). */
export async function secondFactorLockedFor(userId: string): Promise<number> {
  const [row] = await db()`select second_factor_failures, second_factor_failed_at from users where id = ${userId}`;
  if (!row?.second_factor_failed_at) return 0;
  const until = (row.second_factor_failed_at as Date).getTime() + lockMs(Number(row.second_factor_failures));
  return Math.max(0, until - Date.now());
}

/** Records a wrong code; returns the number of wrong codes in a row. */
export async function recordSecondFactorFailure(userId: string): Promise<number> {
  const [row] = await db()`
    update users set second_factor_failures = second_factor_failures + 1, second_factor_failed_at = now()
    where id = ${userId} returning second_factor_failures`;
  return Number(row?.second_factor_failures ?? 0);
}

export async function clearSecondFactorFailures(userId: string) {
  await db()`update users set second_factor_failures = 0, second_factor_failed_at = null where id = ${userId} and second_factor_failures > 0`;
}

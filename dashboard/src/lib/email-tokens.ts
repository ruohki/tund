import "server-only";
import { randomBytes } from "node:crypto";
import { sha256 } from "./auth";
import { db } from "./db";

export type EmailTokenKind = "verify" | "reset";

const TTL: Record<EmailTokenKind, number> = { verify: 24 * 3600_000, reset: 3600_000 };

/** Issues a token; older unused tokens of the same kind for the user stop working. */
export async function issueEmailToken(userId: string, kind: EmailTokenKind): Promise<string> {
  const token = randomBytes(32).toString("base64url");
  await db().begin(async (tx) => {
    await tx`delete from email_tokens where user_id = ${userId} and kind = ${kind} and used_at is null`;
    await tx`
      insert into email_tokens (user_id, kind, token_hash, expires_at)
      values (${userId}, ${kind}, ${sha256(token)}, ${new Date(Date.now() + TTL[kind])})`;
  });
  return token;
}

export type TokenCheck = { ok: true; userId: string; email: string } | { ok: false; reason: "invalid" | "used" | "expired" };

/** Looks a token up without consuming it (for showing the reset form). */
export async function checkEmailToken(token: string, kind: EmailTokenKind): Promise<TokenCheck> {
  if (typeof token !== "string" || token.length > 100) return { ok: false, reason: "invalid" };
  const [t] = await db()`
    select t.user_id, t.used_at, t.expires_at < now() as expired, u.email
    from email_tokens t join users u on u.id = t.user_id
    where t.token_hash = ${sha256(token)} and t.kind = ${kind}`;
  if (!t) return { ok: false, reason: "invalid" };
  if (t.used_at) return { ok: false, reason: "used" };
  if (t.expired) return { ok: false, reason: "expired" };
  return { ok: true, userId: t.user_id as string, email: t.email as string };
}

/** Marks a valid token used, atomically; returns the user id or the reason it can't be used. */
export async function consumeEmailToken(token: string, kind: EmailTokenKind): Promise<TokenCheck> {
  if (typeof token !== "string" || token.length > 100) return { ok: false, reason: "invalid" };
  const [t] = await db()`
    update email_tokens t set used_at = now()
    from users u
    where t.token_hash = ${sha256(token)} and t.kind = ${kind} and t.used_at is null and t.expires_at > now()
      and u.id = t.user_id
    returning t.user_id, u.email`;
  if (t) return { ok: true, userId: t.user_id as string, email: t.email as string };
  const check = await checkEmailToken(token, kind);
  return check.ok ? { ok: false, reason: "used" } : check;
}

// Simple in-process rate limiter (per key, sliding window).
const g = globalThis as unknown as { __tundRate?: Map<string, number[]> };

export function rateLimited(key: string, max: number, windowMs: number): boolean {
  const map = (g.__tundRate ??= new Map<string, number[]>());
  const now = Date.now();
  const hits = (map.get(key) ?? []).filter((t) => now - t < windowMs);
  if (hits.length >= max) {
    map.set(key, hits);
    return true;
  }
  hits.push(now);
  map.set(key, hits);
  if (map.size > 10000) map.clear();
  return false;
}

/** Whether `key` already has `max` hits in the window, without recording one. */
export function rateExceeded(key: string, max: number, windowMs: number): boolean {
  const now = Date.now();
  return ((g.__tundRate ??= new Map<string, number[]>()).get(key) ?? []).filter((t) => now - t < windowMs).length >= max;
}

/** Records one hit for `key` (pairs with rateExceeded when only successes should count). */
export function rateRecord(key: string) {
  const map = (g.__tundRate ??= new Map<string, number[]>());
  map.set(key, [...(map.get(key) ?? []).filter((t) => Date.now() - t < 24 * 3600_000), Date.now()]);
}

import "server-only";
import { createHash, randomBytes } from "node:crypto";
import { cache } from "react";
import { cookies, headers } from "next/headers";
import { notFound, redirect } from "next/navigation";
import { db } from "./db";
import { cookieValues, sessionCookieName, sessionCookieSecure } from "./session-cookie";

const SESSION_DAYS = 30;
const DAY = 24 * 60 * 60 * 1000;

export type User = {
  id: string;
  email: string;
  name: string;
  isAdmin: boolean;
  createdAt: Date;
  /** False only for accounts that still have to confirm their address. */
  emailVerified: boolean;
};

export type Session = { id: string; user: User };

export const sha256 = (s: string) => createHash("sha256").update(s).digest("hex");

export async function usersExist(): Promise<boolean> {
  const [row] = await db()`select exists(select 1 from users) as exists`;
  return Boolean(row?.exists);
}

/** Creates a session row and sets the cookie. Only call from Server Actions / Route Handlers. */
export async function startSession(userId: string) {
  const token = randomBytes(32).toString("base64url");
  const h = await headers();
  const ip = (h.get("x-forwarded-for") ?? "").split(",")[0].trim() || h.get("x-real-ip") || "";
  const ua = (h.get("user-agent") ?? "").slice(0, 300);
  const expires = new Date(Date.now() + SESSION_DAYS * DAY);
  await db()`
    insert into sessions (id, user_id, expires_at, user_agent, ip)
    values (${sha256(token)}, ${userId}, ${expires}, ${ua}, ${ip})`;
  const jar = await cookies();
  jar.set(sessionCookieName(), token, {
    httpOnly: true,
    secure: sessionCookieSecure(),
    sameSite: "lax",
    path: "/",
    maxAge: SESSION_DAYS * 24 * 60 * 60,
  });
}

/** Deletes the current session and clears the cookie. */
export async function endSession() {
  const session = await getSession();
  if (session) await db()`delete from sessions where id = ${session.id}`;
  // A plain delete() omits Secure, which browsers need to overwrite a __Host- cookie.
  (await cookies()).set(sessionCookieName(), "", {
    httpOnly: true,
    secure: sessionCookieSecure(),
    sameSite: "lax",
    path: "/",
    maxAge: 0,
  });
}

/** The session for this request, or null. Memoized per request. */
export const getSession = cache(async (): Promise<Session | null> => {
  // Read the raw header: if several cookies share the name (e.g. one planted by
  // a tunnel subdomain in plain-HTTP dev), try each instead of trusting the first.
  const tokens = cookieValues((await headers()).get("cookie"), sessionCookieName());
  if (!tokens.length) return null;
  const ids = tokens.map(sha256);
  const rows = await db()`
    select s.id as session_id, s.expires_at, u.id, u.email, u.name, u.is_admin, u.created_at, u.email_verified_at
    from sessions s join users u on u.id = s.user_id
    where s.id in ${db()(ids)} and s.expires_at > now() and u.disabled_at is null`;
  const row = ids.map((id) => rows.find((r) => r.session_id === id)).find(Boolean);
  if (!row) return null;
  const id = row.session_id as string;
  // Sliding expiry: push the DB expiry forward at most once a day. The cookie
  // itself is refreshed by proxy.ts on every navigation.
  if ((row.expires_at as Date).getTime() - Date.now() < (SESSION_DAYS - 1) * DAY) {
    await db()`update sessions set expires_at = ${new Date(Date.now() + SESSION_DAYS * DAY)} where id = ${id}`;
  }
  return {
    id,
    user: {
      id: row.id,
      email: row.email,
      name: row.name,
      isAdmin: row.is_admin,
      createdAt: row.created_at,
      emailVerified: Boolean(row.email_verified_at),
    },
  };
});

export async function getCurrentUser(): Promise<User | null> {
  return (await getSession())?.user ?? null;
}

/** For pages, layouts and actions: the signed-in user, or a redirect to /login (or /setup). */
export async function requireUser(): Promise<User> {
  const user = await getCurrentUser();
  if (user) return user;
  if (!(await usersExist())) redirect("/setup");
  const h = await headers();
  redirect(withNext("/login", h.get("x-tund-path")));
}

/** Admin pages and actions: non-admins get a plain 404, as if the area didn't exist. */
export async function requireAdmin(): Promise<User> {
  const user = await requireUser();
  if (!user.isAdmin) notFound();
  return user;
}

const NEXT_BASE = "http://tund.invalid";

/**
 * Only allow same-origin relative redirects after login. Anything that a
 * browser could resolve to another origin ("//evil", "/\\evil", "/\t/evil",
 * absolute URLs) falls back to "/".
 */
export function safeNext(next: unknown): string {
  if (typeof next !== "string" || next.length > 2048 || !next.startsWith("/")) return "/";
  // Browsers treat "\\" like "/" and strip tabs/newlines, so reject them outright.
  if (/[\\\u0000-\u001f\u007f]/.test(next)) return "/";
  try {
    const u = new URL(next, NEXT_BASE);
    if (u.origin !== NEXT_BASE) return "/";
    const out = u.pathname + u.search + u.hash;
    // Dot segments can normalize to a protocol-relative path ("/..//evil" → "//evil").
    return out.startsWith("//") ? "/" : out;
  } catch {
    return "/";
  }
}

/** "/login" or "/login?next=…" for a path that should be returned to afterwards. */
export function withNext(page: "/login" | "/signup" | "/setup", next: unknown): string {
  const target = safeNext(next);
  return target === "/" ? page : `${page}?next=${encodeURIComponent(target)}`;
}

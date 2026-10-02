import "server-only";
import { createHash, randomBytes, timingSafeEqual } from "node:crypto";
import { cookies } from "next/headers";
import { safeNext, signIn, usersExist, type User } from "./auth";
import { notifyAdminsOfSignup } from "./account-mail";
import { audit } from "./audit";
import { clientIp } from "./client-ip";
import { config } from "./config";
import { db } from "./db";
import { isDisposableEmail } from "./disposable-domains";
import { rateExceeded, rateRecord } from "./email-tokens";
import { OAUTH_PROVIDERS, PROVIDER_LABEL, type OAuthProviderId } from "./oauth-shared";
import { decryptSecret, getSettings } from "./settings";
import { signupCheck } from "./signup";

// Sign-in with Google and GitHub (docs/SPEC.md "Sign-in with Google and
// GitHub"): /auth/oauth/<provider>/start sends the browser to the provider,
// /auth/oauth/<provider>/callback comes back with a code, exchanged here with
// the client secret for the account's id and verified email.

type Credentials = { clientId: string; clientSecret: string };

export function callbackUrl(p: OAuthProviderId): string {
  return `${config().dashboardUrl}/auth/oauth/${p}/callback`;
}

async function credentials(p: OAuthProviderId): Promise<Credentials | null> {
  const c = (await getSettings()).oauth[p];
  if (!c?.enabled || !c.client_id) return null;
  const secret = decryptSecret(c.client_secret_enc);
  return secret ? { clientId: c.client_id, clientSecret: secret } : null;
}

/** Providers an admin enabled with complete credentials, in display order. */
export async function enabledProviders(): Promise<OAuthProviderId[]> {
  const out: OAuthProviderId[] = [];
  for (const p of OAUTH_PROVIDERS) if (await credentials(p)) out.push(p);
  return out;
}

// --- the flow cookie ----------------------------------------------------------

const FLOW_COOKIE = "tund_oauth";
const FLOW_PATH = "/auth/oauth";

type Flow = {
  p: OAuthProviderId;
  state: string;
  verifier: string;
  next: string;
  intent: "login" | "link";
};

async function setFlow(flow: Flow | null) {
  (await cookies()).set(FLOW_COOKIE, flow ? Buffer.from(JSON.stringify(flow)).toString("base64url") : "", {
    httpOnly: true,
    secure: config().secureCookies,
    // Lax: sent on the provider's top-level redirect back to us.
    sameSite: "lax",
    path: FLOW_PATH,
    maxAge: flow ? 10 * 60 : 0,
  });
}

async function takeFlow(): Promise<Flow | null> {
  const raw = (await cookies()).get(FLOW_COOKIE)?.value;
  await setFlow(null); // one use
  if (!raw) return null;
  try {
    const f = JSON.parse(Buffer.from(raw, "base64url").toString("utf8")) as Flow;
    return typeof f.state === "string" && typeof f.verifier === "string" ? { ...f, next: safeNext(f.next) } : null;
  } catch {
    return null;
  }
}

const same = (a: string, b: string) => a.length === b.length && timingSafeEqual(Buffer.from(a), Buffer.from(b));

// --- provider endpoints ----------------------------------------------------------

type Profile = { subject: string; email: string | null; name: string };

function authorizeUrl(p: OAuthProviderId, c: Credentials, state: string, verifier: string): string {
  const q = new URLSearchParams({ client_id: c.clientId, redirect_uri: callbackUrl(p), state });
  if (p === "google") {
    q.set("response_type", "code");
    q.set("scope", "openid email profile");
    q.set("code_challenge", createHash("sha256").update(verifier).digest("base64url"));
    q.set("code_challenge_method", "S256");
    q.set("prompt", "select_account");
    return `https://accounts.google.com/o/oauth2/v2/auth?${q}`;
  }
  q.set("scope", "read:user user:email");
  return `https://github.com/login/oauth/authorize?${q}`;
}

async function getJSON(url: string, token: string): Promise<unknown> {
  const res = await fetch(url, {
    headers: { Authorization: `Bearer ${token}`, Accept: "application/json", "User-Agent": "tund-dashboard" },
    signal: AbortSignal.timeout(10_000),
  });
  if (!res.ok) throw new Error(`${url}: HTTP ${res.status}`);
  return res.json();
}

/** Trades the code for the account's id and verified email (null when it has none). */
async function fetchProfile(p: OAuthProviderId, c: Credentials, code: string, verifier: string): Promise<Profile> {
  const form = new URLSearchParams({ client_id: c.clientId, client_secret: c.clientSecret, code, redirect_uri: callbackUrl(p) });
  if (p === "google") {
    form.set("grant_type", "authorization_code");
    form.set("code_verifier", verifier);
  }
  const res = await fetch(p === "google" ? "https://oauth2.googleapis.com/token" : "https://github.com/login/oauth/access_token", {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded", Accept: "application/json" },
    body: form,
    signal: AbortSignal.timeout(10_000),
  });
  // GitHub reports errors with 200 and an "error" field.
  const tok = (await res.json().catch(() => ({}))) as { access_token?: string; error?: string; error_description?: string };
  if (!res.ok || !tok.access_token) throw new Error(`token exchange: HTTP ${res.status} ${tok.error ?? ""} ${tok.error_description ?? ""}`);

  if (p === "google") {
    const u = (await getJSON("https://openidconnect.googleapis.com/v1/userinfo", tok.access_token)) as {
      sub?: string;
      email?: string;
      email_verified?: boolean;
      name?: string;
    };
    if (!u.sub) throw new Error("userinfo without sub");
    return { subject: u.sub, email: u.email && u.email_verified ? u.email.toLowerCase() : null, name: u.name ?? "" };
  }
  const u = (await getJSON("https://api.github.com/user", tok.access_token)) as { id?: number; login?: string; name?: string | null };
  if (typeof u.id !== "number") throw new Error("GitHub user without id");
  const emails = (await getJSON("https://api.github.com/user/emails", tok.access_token)) as {
    email: string;
    primary: boolean;
    verified: boolean;
  }[];
  const verified = Array.isArray(emails) ? emails.filter((e) => e.verified) : [];
  const email = (verified.find((e) => e.primary) ?? verified[0])?.email ?? null;
  return { subject: String(u.id), email: email?.toLowerCase() ?? null, name: u.name || u.login || "" };
}

// --- start and callback -------------------------------------------------------------

/** Where /auth/oauth/<p>/start sends the browser. */
export async function startOAuth(p: OAuthProviderId, intent: "login" | "link", next: string): Promise<string | null> {
  const c = await credentials(p);
  if (!c) return null;
  const flow: Flow = { p, intent, next: safeNext(next), state: randomBytes(24).toString("base64url"), verifier: randomBytes(32).toString("base64url") };
  await setFlow(flow);
  return authorizeUrl(p, c, flow.state, flow.verifier);
}

const loginError = (code: string, next: string) =>
  `/login?error=${code}${next !== "/" ? `&next=${encodeURIComponent(next)}` : ""}`;
const settingsError = (code: string) => `/settings?oauth_error=${code}`;

/**
 * Handles the provider's redirect back and returns where to send the browser:
 * signed in (existing identity, an account with the same verified email, or a
 * new account where sign-up allows it), linked to the signed-in user, or back
 * to /login or /settings with an error code (see OAUTH_ERRORS).
 */
export async function finishOAuth(p: OAuthProviderId, query: URLSearchParams, current: User | null): Promise<string> {
  const flow = await takeFlow();
  const state = query.get("state") ?? "";
  if (!flow || flow.p !== p || !state || !same(state, flow.state)) return current ? settingsError("state") : loginError("state", "/");
  const fail = (code: string) => (flow.intent === "link" ? settingsError(code) : loginError(code, flow.next));
  if (query.get("error")) return fail(query.get("error") === "access_denied" ? "cancelled" : "failed");
  const code = query.get("code");
  const c = await credentials(p);
  if (!c) return fail("unavailable");
  if (!code) return fail("failed");

  let profile: Profile;
  try {
    profile = await fetchProfile(p, c, code, flow.verifier);
  } catch (err) {
    console.error(`tund: ${p} sign-in failed`, err);
    return fail("failed");
  }

  const [existing] = await db()`
    select i.user_id, u.disabled_at from user_identities i join users u on u.id = i.user_id
    where i.provider = ${p} and i.subject = ${profile.subject}`;

  if (flow.intent === "link") {
    if (!current) return loginError("state", "/settings");
    if (existing && existing.user_id !== current.id) return settingsError("taken");
    if (!existing) {
      try {
        await db()`
          insert into user_identities (user_id, provider, subject, email)
          values (${current.id}, ${p}, ${profile.subject}, ${profile.email ?? ""})
          on conflict (user_id, provider) do update set subject = excluded.subject, email = excluded.email`;
      } catch (err) {
        if ((err as { code?: string }).code === "23505") return settingsError("taken");
        throw err;
      }
      await audit({ id: current.id, email: current.email }, "user.identity_link", current.email, { provider: p, email: profile.email });
    }
    return `/settings?oauth_linked=${p}`;
  }

  // Signing in.
  let userId: string | null = null;
  if (existing) {
    if (existing.disabled_at) return fail("disabled");
    userId = existing.user_id as string;
  } else {
    if (!profile.email) return fail("no_email");
    const [byEmail] = await db()`select id, disabled_at from users where email = ${profile.email}`;
    if (byEmail) {
      // The provider verified the address, so it's the same person: connect them.
      if (byEmail.disabled_at) return fail("disabled");
      userId = byEmail.id as string;
      await db()`
        insert into user_identities (user_id, provider, subject, email)
        values (${userId}, ${p}, ${profile.subject}, ${profile.email})
        on conflict (user_id, provider) do update set subject = excluded.subject, email = excluded.email`;
      await db()`update users set email_verified_at = coalesce(email_verified_at, now()) where id = ${userId}`;
      await audit({ id: userId, email: profile.email }, "user.identity_link", profile.email, { provider: p, via: "email" });
    } else {
      const created = await signUp(p, profile, flow.next);
      if ("error" in created) return fail(created.error);
      userId = created.userId;
    }
  }
  await db()`update user_identities set last_used_at = now() where provider = ${p} and subject = ${profile.subject}`;
  // Two-factor applies to Google/GitHub sign-ins too, or they would get around it.
  return signIn(userId, flow.next);
}

/** A new account from a provider profile, under the same rules as the sign-up form. */
async function signUp(p: OAuthProviderId, profile: Profile, next: string): Promise<{ userId: string } | { error: string }> {
  const email = profile.email!;
  if (!(await usersExist())) return { error: "signup_closed" }; // the first account is created on /setup
  const check = await signupCheck(next);
  if (!check.allowed) return { error: (await getSettings()).signup_mode === "invite" ? "signup_invite" : "signup_closed" };
  if (check.inviteEmail && check.inviteEmail !== email) return { error: "invite_email" };
  const s = await getSettings();
  if (s.block_disposable_emails && !check.inviteEmail && isDisposableEmail(email)) return { error: "disposable" };
  const rateKey = `signup-ip:${(await clientIp()) || "unknown"}`;
  if (s.signup_rate_limit > 0 && rateExceeded(rateKey, s.signup_rate_limit, 60 * 60_000)) return { error: "rate" };

  let userId: string;
  try {
    userId = await db().begin(async (tx) => {
      const [row] = await tx`
        insert into users (email, name, password_hash, email_verified_at)
        values (${email}, ${profile.name.slice(0, 100)}, null, now())
        returning id`;
      await tx`
        insert into user_identities (user_id, provider, subject, email, last_used_at)
        values (${row.id}, ${p}, ${profile.subject}, ${email}, now())`;
      return row.id as string;
    });
  } catch (err) {
    // Someone signed up with this address or identity a moment ago.
    if ((err as { code?: string }).code === "23505") return { error: "failed" };
    throw err;
  }
  rateRecord(rateKey);
  await notifyAdminsOfSignup(userId, email);
  await audit({ id: userId, email }, "user.signup", email, { provider: p });
  return { userId };
}

/** The accounts a user connected, for Settings and the admin's user page. */
export async function identitiesOf(userId: string): Promise<{ provider: OAuthProviderId; email: string; label: string }[]> {
  const rows = await db()`select provider, email from user_identities where user_id = ${userId} order by provider`;
  return rows.map((r) => ({ provider: r.provider as OAuthProviderId, email: r.email as string, label: PROVIDER_LABEL[r.provider as OAuthProviderId] }));
}

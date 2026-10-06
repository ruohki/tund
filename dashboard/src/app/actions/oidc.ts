"use server";

import { refresh } from "next/cache";
import { requireUser } from "@/lib/auth";
import { db, notify } from "@/lib/db";
import { isUuid } from "@/lib/requests";
import { checkSlug } from "@/lib/validate";
import { requireTeamRole } from "@/lib/teams";
import type { FormState } from "./auth";

async function changed(id: string, teamId: string | null) {
  await notify("tund_config", { kind: "oidc_provider", id });
  if (teamId) await notify("tund_config", { kind: "team", id: teamId });
}

function normalizeIssuer(input: string): { issuer: string; error: null } | { issuer: null; error: string } {
  let url: URL;
  try {
    url = new URL(input.trim());
  } catch {
    return { issuer: null, error: "Enter the issuer URL, e.g. https://accounts.google.com." };
  }
  const local = url.hostname === "localhost" || url.hostname === "127.0.0.1";
  if (url.protocol !== "https:" && !(url.protocol === "http:" && local)) {
    return { issuer: null, error: "The issuer must use https://." };
  }
  // Keep exactly what the user typed apart from a trailing slash on a bare origin:
  // the issuer has to match the provider's discovery document byte for byte.
  const s = url.toString();
  return { issuer: url.pathname === "/" && !input.trim().endsWith("/") ? s.replace(/\/$/, "") : s, error: null };
}

export async function saveProviderAction(_: FormState, fd: FormData): Promise<FormState> {
  const user = await requireUser();
  const id = String(fd.get("id") ?? "");
  const name = String(fd.get("name") ?? "").trim().slice(0, 80);
  const slug = String(fd.get("slug") ?? "").trim().toLowerCase();
  const clientId = String(fd.get("client_id") ?? "").trim();
  const clientSecret = String(fd.get("client_secret") ?? "");
  const scopes = String(fd.get("scopes") ?? "").trim().replace(/\s+/g, " ") || "openid email profile";

  if (!name) return { error: "Give the provider a name." };
  const slugError = checkSlug(slug);
  if (slugError) return { error: `Slug: ${slugError}` };
  const iss = normalizeIssuer(String(fd.get("issuer") ?? ""));
  if (iss.error !== null) return { error: iss.error };
  if (!clientId) return { error: "Enter the client ID from your identity provider." };
  if (!scopes.split(" ").includes("openid")) return { error: "Scopes must include openid." };

  // Team providers (form carries team_id) need the team admin role.
  const teamId = String(fd.get("team_id") ?? "") || null;
  if (teamId) {
    const m = isUuid(teamId) ? await requireTeamRole(user, teamId, "admin") : "Unknown team.";
    if (typeof m === "string") return { error: m };
  }
  const owned = teamId ? db()`team_id = ${teamId}` : db()`user_id = ${user.id} and team_id is null`;

  try {
    if (isUuid(id)) {
      const res = clientSecret
        ? await db()`
            update oidc_providers set name = ${name}, slug = ${slug}, issuer = ${iss.issuer}, client_id = ${clientId},
              client_secret = ${clientSecret}, scopes = ${scopes}
            where id = ${id} and ${owned}`
        : await db()`
            update oidc_providers set name = ${name}, slug = ${slug}, issuer = ${iss.issuer}, client_id = ${clientId},
              scopes = ${scopes}
            where id = ${id} and ${owned}`;
      if (!res.count) return { error: "That provider no longer exists." };
      await changed(id, teamId);
    } else {
      const [row] = await db()`
        insert into oidc_providers (user_id, team_id, name, slug, issuer, client_id, client_secret, scopes)
        values (${user.id}, ${teamId}, ${name}, ${slug}, ${iss.issuer}, ${clientId}, ${clientSecret}, ${scopes})
        returning id`;
      await changed(row.id, teamId);
    }
  } catch (err) {
    if ((err as { code?: string }).code === "23505") {
      return { error: teamId ? `This team already has a provider with the slug “${slug}”.` : `You already have a provider with the slug “${slug}”.` };
    }
    throw err;
  }
  refresh();
  return { ok: isUuid(id) ? "Provider updated." : `Added ${name}. Tunnels can use it with the slug “${slug}”.` };
}

export async function deleteProviderAction(fd: FormData) {
  const user = await requireUser();
  const id = String(fd.get("id") ?? "");
  if (!isUuid(id)) return;
  const [p] = await db()`select id, user_id, team_id from oidc_providers where id = ${id}`;
  if (!p) return;
  if (p.team_id) {
    if (typeof (await requireTeamRole(user, p.team_id, "admin")) === "string") return;
  } else if (p.user_id !== user.id) {
    return;
  }
  // The team requires sign-in with it: deleting it would lock every team hostname (the UI offers no button).
  const [required] = await db()`select 1 from teams where auth_oidc_provider_id = ${id} and auth_oidc_required`;
  if (required) return;
  // Domains that used it (personal or team, any member's) fall back to "none"; ON DELETE SET NULL
  // would leave auth_mode = 'oidc' without a provider.
  const affected = await db().begin(async (tx) => {
    const rows = await tx`
      update domains set auth_mode = 'none', auth_oidc_allow = '{}' where auth_oidc_provider_id = ${id} returning id`;
    await tx`delete from oidc_providers where id = ${id}`;
    return rows.map((r) => r.id as string);
  });
  for (const d of affected) await notify("tund_config", { kind: "domain", id: d });
  await changed(id, p.team_id as string | null);
  refresh();
}

export type DiscoveryResult =
  | { ok: true; issuer: string; authorization: string; token: string; matches: boolean; scopes: string[] }
  | { ok: false; error: string };

export async function testDiscoveryAction(issuerInput: string): Promise<DiscoveryResult> {
  await requireUser();
  const iss = normalizeIssuer(issuerInput);
  if (iss.error !== null) return { ok: false, error: iss.error };
  const url = `${iss.issuer.replace(/\/$/, "")}/.well-known/openid-configuration`;
  try {
    const res = await fetch(url, { signal: AbortSignal.timeout(6000), cache: "no-store", headers: { Accept: "application/json" } });
    if (!res.ok) return { ok: false, error: `${url} answered HTTP ${res.status}.` };
    const doc = (await res.json()) as Record<string, unknown>;
    if (typeof doc.issuer !== "string" || typeof doc.authorization_endpoint !== "string") {
      return { ok: false, error: "The response isn't an OpenID Connect discovery document." };
    }
    return {
      ok: true,
      issuer: doc.issuer,
      authorization: doc.authorization_endpoint,
      token: String(doc.token_endpoint ?? ""),
      matches: doc.issuer === iss.issuer,
      scopes: Array.isArray(doc.scopes_supported) ? (doc.scopes_supported as string[]).slice(0, 12) : [],
    };
  } catch (err) {
    return { ok: false, error: `Couldn't fetch ${url}: ${err instanceof Error ? err.message : String(err)}` };
  }
}

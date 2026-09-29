"use server";

import { randomBytes } from "node:crypto";
import { refresh } from "next/cache";
import { requireUser } from "@/lib/auth";
import { config } from "@/lib/config";
import { db } from "@/lib/db";
import { checkRouting, checkTxt, challengeName, type RoutingStatus } from "@/lib/dnscheck";
import { hashPassword } from "@/lib/password";
import { isUuid } from "@/lib/requests";
import { checkCustomHostname, parseAllowList } from "@/lib/validate";
import {
  domainChanged,
  lockAndCheckLimit,
  makeDefault,
  pinLabel,
  pinRandom,
  removeDomain,
  type DomainOwner,
} from "@/lib/static-hostnames";
import { providerOptions, requireTeamRole } from "@/lib/teams";
import { releaseTcpPort, reserveTcpPort } from "@/lib/tcp";
import type { User } from "@/lib/auth";
import type { FormState } from "./auth";

/** Personal unless the form carries team_id; team changes need the team admin role. */
async function ownerFromForm(user: User, fd: FormData): Promise<DomainOwner | string> {
  const teamId = String(fd.get("team_id") ?? "");
  if (!teamId) return { teamId: null };
  if (!isUuid(teamId)) return "Unknown team.";
  const m = await requireTeamRole(user, teamId, "admin");
  return typeof m === "string" ? m : { teamId };
}

type ManagedDomain = {
  id: string;
  user_id: string;
  hostname: string;
  kind: string;
  team_id: string | null;
  verification_token: string;
  verified_at: Date | null;
  auth_password_hash: string | null;
};

/** A domain the user may manage: their own personal one, or a team domain where they're owner/admin. */
async function loadManagedDomain(user: User, id: string): Promise<{ d: ManagedDomain; owner: DomainOwner } | string> {
  if (!isUuid(id)) return "Unknown domain.";
  const [d] = await db()<ManagedDomain[]>`
    select id, hostname, kind, team_id, user_id, verification_token, verified_at, auth_password_hash
    from domains where id = ${id}`;
  if (!d) return "Unknown domain.";
  if (d.team_id) {
    const m = await requireTeamRole(user, d.team_id, "admin");
    if (typeof m === "string") return m;
    return { d, owner: { teamId: d.team_id } };
  }
  if (d.user_id !== user.id) return "Unknown domain.";
  return { d, owner: { teamId: null } };
}

const changed = domainChanged;

function uniqueViolation(err: unknown) {
  return (err as { code?: string }).code === "23505";
}

export async function addSubdomainAction(_: FormState, fd: FormData): Promise<FormState> {
  const user = await requireUser();
  const c = config();
  const label = String(fd.get("label") ?? "")
    .trim()
    .toLowerCase()
    .replace(new RegExp(`\\.${c.baseDomain.replace(/\./g, "\\.")}$`), "");
  const owner = await ownerFromForm(user, fd);
  if (typeof owner === "string") return { error: owner };
  const res = await pinLabel(user, label, owner);
  if (!res.ok) return { error: res.error };
  refresh();
  return { ok: res.isDefault ? `Pinned ${res.hostname}. It's your default, so tund http uses it.` : `Pinned ${res.hostname}.` };
}

export async function claimRandomAction(_: FormState, fd: FormData): Promise<FormState> {
  const user = await requireUser();
  const owner = await ownerFromForm(user, fd);
  if (typeof owner === "string") return { error: owner };
  const res = await pinRandom(user, owner);
  if (!res.ok) return { error: res.error };
  refresh();
  return { ok: res.isDefault ? `Pinned ${res.hostname}. It's your default, so tund http uses it.` : `Pinned ${res.hostname}.` };
}

export async function makeDefaultAction(fd: FormData) {
  const user = await requireUser();
  const id = String(fd.get("id") ?? "");
  if (isUuid(id)) await makeDefault(user, id);
  refresh();
}

export async function addCustomDomainAction(_: FormState, fd: FormData): Promise<FormState> {
  const user = await requireUser();
  const c = config();
  const res = checkCustomHostname(String(fd.get("hostname") ?? ""), c.baseDomain, c.dashboardHost);
  if (res.error !== null) return { error: res.error };
  const owner = await ownerFromForm(user, fd);
  if (typeof owner === "string") return { error: owner };
  try {
    const out = await db().begin(async (tx) => {
      const limitError = await lockAndCheckLimit(tx, user, "custom", owner);
      if (limitError) return { error: limitError };
      const [row] = await tx`
        insert into domains (user_id, team_id, hostname, kind, verification_token)
        values (${user.id}, ${owner.teamId}, ${res.hostname}, 'custom', ${randomBytes(16).toString("hex")}) returning id`;
      return { id: row.id as string };
    });
    if ("error" in out) return { error: out.error };
    await domainChanged(out.id, owner);
  } catch (err) {
    if (uniqueViolation(err)) return { error: `${res.hostname} has already been added.` };
    throw err;
  }
  refresh();
  return { ok: `Added ${res.hostname}. Create the DNS records below, then verify.` };
}

export type VerifyResult = {
  verified: boolean;
  message: string;
  routing: RoutingStatus;
  txtValues: string[];
};

export async function verifyDomainAction(id: string): Promise<VerifyResult> {
  const user = await requireUser();
  const found = await loadManagedDomain(user, id);
  if (typeof found === "string" || found.d.kind !== "custom") {
    return { verified: false, message: typeof found === "string" ? found : "Unknown domain.", routing: { state: "missing" }, txtValues: [] };
  }
  const { d, owner } = found;
  const [txt, routing] = await Promise.all([
    checkTxt(d.hostname, d.verification_token),
    checkRouting(d.hostname, config().serverIp),
  ]);
  if (txt.found) {
    if (!d.verified_at) {
      await db()`update domains set verified_at = now() where id = ${id}`;
      await changed(id, owner);
    }
    refresh();
    return { verified: true, message: "Ownership verified.", routing, txtValues: txt.values };
  }
  return {
    verified: Boolean(d.verified_at),
    message:
      txt.error ??
      (txt.values.length
        ? `Found a TXT record at ${challengeName(d.hostname)}, but not the expected value.`
        : `No TXT record at ${challengeName(d.hostname)} yet. DNS changes can take a few minutes to appear.`),
    routing,
    txtValues: txt.values,
  };
}

export async function deleteDomainAction(fd: FormData) {
  const user = await requireUser();
  const found = await loadManagedDomain(user, String(fd.get("id") ?? ""));
  if (typeof found !== "string") await removeDomain(user, found.d.id, found.owner);
  refresh();
}

export async function updatePolicyAction(_: FormState, fd: FormData): Promise<FormState> {
  const user = await requireUser();
  const found = await loadManagedDomain(user, String(fd.get("id") ?? ""));
  if (typeof found === "string") return { error: found };
  const { d, owner } = found;
  const id = d.id;

  const mode = String(fd.get("mode") ?? "none");
  if (mode === "none") {
    await db()`
      update domains set auth_mode = 'none', auth_password_hash = null, auth_oidc_provider_id = null, auth_oidc_allow = '{}'
      where id = ${id}`;
  } else if (mode === "password") {
    const password = String(fd.get("password") ?? "");
    if (!password && !d.auth_password_hash) return { error: "Set a password visitors will need to enter." };
    if (password && password.length < 6) return { error: "Use at least 6 characters for the visitor password." };
    const hash = password ? await hashPassword(password) : d.auth_password_hash;
    await db()`
      update domains set auth_mode = 'password', auth_password_hash = ${hash}, auth_oidc_provider_id = null, auth_oidc_allow = '{}'
      where id = ${id}`;
  } else if (mode === "oidc") {
    const providerId = String(fd.get("provider") ?? "");
    if (!isUuid(providerId)) return { error: "Choose an identity provider." };
    // Team domains may only use that team's providers; personal ones the user's own and their teams'.
    const options = await providerOptions(user.id, owner.teamId);
    if (!options.some((o) => o.id === providerId)) return { error: "That identity provider isn't available for this domain." };
    const allow = parseAllowList(String(fd.get("allow") ?? ""));
    if (allow.error) return { error: allow.error };
    await db()`
      update domains set auth_mode = 'oidc', auth_password_hash = null, auth_oidc_provider_id = ${providerId},
        auth_oidc_allow = ${allow.entries}
      where id = ${id}`;
  } else {
    return { error: "Unknown access mode." };
  }
  await changed(id, owner);
  refresh();
  return {
    ok:
      mode === "none"
        ? "Anyone with the address can reach this domain."
        : "Saved. Visitors who already signed in will be asked again.",
  };
}

// --- static TCP ports ----------------------------------------------------------

export async function reserveTcpAction(_: FormState, fd: FormData): Promise<FormState> {
  const user = await requireUser();
  const owner = await ownerFromForm(user, fd);
  if (typeof owner === "string") return { error: owner };
  const raw = String(fd.get("port") ?? "").trim();
  const port = raw ? Number.parseInt(raw, 10) : null;
  if (raw && !Number.isInteger(port)) return { error: "Enter a port number, or leave it empty for a random one." };
  const res = await reserveTcpPort(user, owner, port);
  if (!res.ok) return { error: res.error };
  refresh();
  return { ok: `Reserved port ${res.port}.` };
}

export async function releaseTcpAction(fd: FormData) {
  const user = await requireUser();
  const owner = await ownerFromForm(user, fd);
  if (typeof owner === "string") return;
  const port = Number.parseInt(String(fd.get("port") ?? ""), 10);
  if (Number.isInteger(port)) await releaseTcpPort(user, owner, port);
  refresh();
}

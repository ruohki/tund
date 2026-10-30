import "server-only";
import { createHmac } from "node:crypto";
import type { User } from "./auth";
import { db } from "./db";
import type { Settings } from "./settings";

type Details = Record<string, string | number | boolean>;

const b64url = (value: unknown) => Buffer.from(JSON.stringify(value)).toString("base64url");

/** Signed identity, so nobody can ask in someone else's name. The widget redeems it once, when it starts. */
function userToken(user: User, secret: string, attributes: Details) {
  const now = Math.floor(Date.now() / 1000);
  const header = b64url({ alg: "HS256", typ: "JWT" });
  const payload = b64url({
    email: user.email,
    name: user.name || undefined,
    user_id: user.id,
    attributes,
    iat: now,
    exp: now + 60 * 60,
  });
  const signature = createHmac("sha256", secret).update(`${header}.${payload}`).digest("base64url");
  return `${header}.${payload}.${signature}`;
}

/**
 * Props of the dashboard's support widget, null while no key is configured.
 * Agents see the account in the token and its current use in the context:
 * counts and the client version, nothing about what runs through the tunnels.
 */
export async function appSupportWidget(user: User, pro: boolean, s: Settings) {
  const widgetKey = s.support_app_widget_key;
  if (!widgetKey) return null;
  const [usage] = await db()`
    select
      (select count(*)::int from tunnels where user_id = ${user.id} and ended_at is null) as tunnels_online,
      (select count(*)::int from domains where user_id = ${user.id} and team_id is null and kind = 'subdomain') as static_hostnames,
      (select count(*)::int from domains where user_id = ${user.id} and team_id is null and kind = 'custom') as custom_domains,
      (select count(*)::int from team_members where user_id = ${user.id}) as teams,
      (select trim(client_version || ' ' || client_os) from agent_sessions
        where user_id = ${user.id} order by connected_at desc limit 1) as client`;
  return {
    widgetKey,
    userToken: s.support_widget_secret
      ? userToken(user, s.support_widget_secret, {
          plan: pro ? "pro" : "free",
          admin: user.isAdmin,
          emailVerified: user.emailVerified,
          trusted: user.trusted,
          memberSince: user.createdAt.toISOString().slice(0, 10),
        })
      : undefined,
    context: {
      tunnelsOnline: usage.tunnels_online as number,
      staticHostnames: usage.static_hostnames as number,
      customDomains: usage.custom_domains as number,
      teams: usage.teams as number,
      client: (usage.client as string | null) || "never connected",
    },
  };
}

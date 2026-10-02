import "server-only";

/**
 * Static configuration read from the environment (see docs/SPEC.md). Instance
 * settings that admins can change at runtime (sign-up, limits, abuse protection,
 * branding) live in lib/settings.ts instead.
 */
export function config() {
  const baseDomain = (process.env.TUND_BASE_DOMAIN ?? "localhost").toLowerCase();
  const dashboardHost = (process.env.TUND_DASHBOARD_HOST || `dashboard.${baseDomain}`).toLowerCase();
  const scheme = process.env.TUND_PUBLIC_SCHEME === "http" ? "http" : "https";
  const port = process.env.TUND_PUBLIC_PORT?.trim() || "";
  const suffix = port && !((scheme === "https" && port === "443") || (scheme === "http" && port === "80")) ? `:${port}` : "";
  return {
    baseDomain,
    dashboardHost,
    scheme,
    port,
    dashboardUrl: `${scheme}://${dashboardHost}${suffix}`,
    publicUrl: (hostname: string) => `${scheme}://${hostname}${suffix}`,
    internalUrl: (process.env.TUND_INTERNAL_URL || "http://server:4040").replace(/\/$/, ""),
    internalSecret: process.env.TUND_INTERNAL_SECRET || "",
    serverIp: process.env.TUND_SERVER_IP || "",
    secureCookies: scheme === "https",
  };
}

export type PublicConfig = {
  baseDomain: string;
  dashboardHost: string;
  dashboardUrl: string;
  scheme: string;
  portSuffix: string;
  serverIp: string;
  /** Every public address of this instance (lib/servers.ts); set by pages that show DNS instructions. */
  serverIps?: string[];
};

/** The subset that is safe to hand to client components. */
export function publicConfig(): PublicConfig {
  const c = config();
  return {
    baseDomain: c.baseDomain,
    dashboardHost: c.dashboardHost,
    dashboardUrl: c.dashboardUrl,
    scheme: c.scheme,
    portSuffix: c.dashboardUrl.slice(`${c.scheme}://${c.dashboardHost}`.length),
    serverIp: c.serverIp,
  };
}

/** "Report abuse" target: mailto: for an email address, otherwise the URL as given (http(s) only). */
export function abuseHref(contact: string): string | null {
  if (!contact) return null;
  if (/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(contact)) return `mailto:${contact}`;
  if (/^https?:\/\//i.test(contact)) return contact;
  return null;
}

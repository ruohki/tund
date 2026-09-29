// Validation shared by server actions. Keep in sync with docs/SPEC.md.

export const RESERVED_LABELS = [
  "dashboard", "www", "api", "admin", "app", "connect", "edge", "tund", "mail", "smtp", "imap",
  "ftp", "ns", "ns1", "ns2", "status", "docs", "static", "assets", "cdn",
];

const LABEL_RE = /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/;

export function isValidLabel(label: string) {
  return LABEL_RE.test(label);
}

export function reservedLabels(dashboardHost: string) {
  return new Set([...RESERVED_LABELS, dashboardHost.split(".")[0]]);
}

/** Returns an error message, or null when the subdomain label is acceptable. */
export function checkSubdomain(label: string, dashboardHost: string): string | null {
  if (!label) return "Enter a subdomain.";
  if (!isValidLabel(label)) {
    return "Use lowercase letters, digits and hyphens (1–63 characters, no hyphen at the start or end).";
  }
  if (reservedLabels(dashboardHost).has(label)) return `“${label}” is reserved by the server.`;
  return null;
}

/** Normalizes and validates a custom hostname (optionally "*.example.com"). */
export function checkCustomHostname(
  input: string,
  baseDomain: string,
  dashboardHost: string,
): { hostname: string; error: null } | { hostname: null; error: string } {
  let host = input.trim().toLowerCase().replace(/\.$/, "");
  host = host.replace(/^https?:\/\//, "").replace(/\/.*$/, "");
  if (!host) return { hostname: null, error: "Enter a domain name." };
  const wildcard = host.startsWith("*.");
  const rest = wildcard ? host.slice(2) : host;
  const labels = rest.split(".");
  if (labels.length < 2 || !labels.every(isValidLabel) || rest.length > 253) {
    return { hostname: null, error: "That doesn't look like a domain name, e.g. api.example.com or *.dev.example.com." };
  }
  if (/^\d+$/.test(labels[labels.length - 1])) {
    return { hostname: null, error: "IP addresses can't be used as custom domains." };
  }
  if (rest === baseDomain || rest.endsWith(`.${baseDomain}`) || host === dashboardHost) {
    return { hostname: null, error: `Names under ${baseDomain} belong under Static hostnames. Pin the name there instead.` };
  }
  return { hostname: host, error: null };
}

export function checkEmail(input: string): string | null {
  if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(input)) return "Enter a valid email address.";
  return null;
}

/**
 * OIDC allow list entries: "alice@example.com", "@example.com" or "group:<name>".
 * Entries are separated by commas or new lines; group names are matched exactly
 * (case-sensitive, may contain spaces), emails and domains case-insensitively.
 */
export function parseAllowList(input: string): { entries: string[]; error: string | null } {
  const entries: string[] = [];
  for (const raw of input.split(/[,\n]+/)) {
    const e = raw.trim();
    if (!e) continue;
    if (/^group:/i.test(e)) {
      const name = e.slice(e.indexOf(":") + 1).trim();
      if (!name) return { entries: [], error: "“group:” needs a group name, e.g. group:engineering." };
      if (name.length > 200) return { entries: [], error: "Group names can be at most 200 characters." };
      entries.push(`group:${name}`);
      continue;
    }
    const lower = e.toLowerCase();
    const ok = lower.startsWith("@")
      ? /^@[a-z0-9.-]+\.[a-z]{2,}$/.test(lower)
      : /^[^\s@]+@[a-z0-9.-]+\.[a-z]{2,}$/.test(lower);
    if (!ok) return { entries: [], error: `“${e}” is not an email address, @domain or group:<name>.` };
    entries.push(lower);
  }
  return { entries: [...new Set(entries)], error: null };
}

export const ALLOW_LIST_HINT =
  "Emails, @domains or group:<name>, separated by commas. Leave empty to allow everyone who can sign in. Groups come from the provider's groups claim.";

export function checkSlug(slug: string): string | null {
  if (!/^[a-z0-9](?:[a-z0-9-]{0,38}[a-z0-9])?$/.test(slug)) {
    return "Use lowercase letters, digits and hyphens (up to 40 characters).";
  }
  return null;
}

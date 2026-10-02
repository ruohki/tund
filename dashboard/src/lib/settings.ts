import "server-only";
import { createCipheriv, createDecipheriv, createHash, randomBytes } from "node:crypto";
import { db, notify } from "./db";
import type { OAuthProviderId } from "./oauth-shared";

// Instance settings (docs/SPEC.md "Admin area, settings and email"): a stored
// row wins over the TUND_* environment variable, which only provides the
// default. Read through getSettings(); the admin UI is the source of truth.

export type SignupMode = "open" | "invite" | "closed";
export type SmtpSecurity = "starttls" | "tls" | "none";

export type SmtpSettings = {
  host: string;
  port: number;
  security: SmtpSecurity;
  username: string;
  password_enc: string;
  from_email: string;
  from_name: string;
};

/** One sign-in provider (Admin → Sign-in); the secret is stored encrypted like the SMTP password. */
export type OAuthProviderSettings = {
  enabled: boolean;
  client_id: string;
  client_secret_enc: string;
};

export type OAuthSettings = Partial<Record<OAuthProviderId, OAuthProviderSettings>>;

export type Settings = {
  signup_mode: SignupMode;
  require_email_verification: boolean;
  limit_tunnels: number;
  limit_pinned: number;
  custom_domains: boolean;
  passthrough: boolean;
  limit_domains: number;
  limit_teams: number;
  limit_bandwidth_kbps: number;
  limit_transfer_gb: number;
  limit_tunnel_lifetime: number;
  auto_pin: boolean;
  browser_warning: boolean;
  abuse_contact: string;
  retention_days: number;
  capture_max_body: number;
  instance_name: string;
  notify_admins_on_signup: boolean;
  // Abuse protection (docs/SPEC.md "Abuse protection")
  untrusted_custom_domains: "allow" | "review" | "deny";
  warn_custom_domains: boolean;
  custom_domain_min_age_days: number;
  untrusted_tcp: boolean;
  untrusted_tls: boolean;
  blocked_hostname_words: string[];
  safe_browsing_api_key: string;
  phishing_heuristics: boolean;
  phishing_auto_block: boolean;
  turnstile_site_key: string;
  turnstile_secret_key: string;
  block_disposable_emails: boolean;
  signup_rate_limit: number;
  // Public legal pages, Markdown; empty = the built-in text.
  terms_markdown: string;
  acceptable_use_markdown: string;
  smtp: SmtpSettings | null;
  oauth: OAuthSettings;
};

export type SettingKey = keyof Settings;
export type EditableKey = Exclude<SettingKey, "smtp" | "oauth">;

type Kind = "bool" | "int" | "string" | "signup" | "enum" | "list" | "secret" | "markdown";

export type SettingDef = {
  key: EditableKey;
  group: "Sign-up & accounts" | "Limits" | "Abuse protection" | "Traffic capture & retention" | "Branding" | "Legal pages";
  label: string;
  help: string;
  kind: Kind;
  env?: string;
  min?: number;
  max?: number;
  /** For kind "enum": value → label. */
  options?: [string, string][];
  /** Default when nothing is stored: from the environment, else built in. */
  fallback: () => Settings[EditableKey];
};

const env = (k: string) => process.env[k]?.trim() ?? "";
const envInt = (k: string, dflt: number) => {
  const n = Number.parseInt(env(k), 10);
  return Number.isFinite(n) && n >= 0 ? n : dflt;
};
const envBool = (k: string, dflt: boolean) => (env(k) ? env(k) === "true" : dflt);

// Mirrors defaultBlockedWords in internal/server/abuse.go. Entries starting
// with "=" match a whole dash-separated label part; others match anywhere
// (case-insensitive, dashes ignored).
export const DEFAULT_BLOCKED_WORDS = [
  "paypal", "microsoft", "office365", "outlook", "hotmail", "icloud", "=apple", "appleid", "google", "gmail",
  "amazon", "netflix", "facebook", "instagram", "whatsapp", "telegram", "linkedin", "twitter", "coinbase",
  "binance", "kraken", "metamask", "trustwallet", "ledger", "trezor", "blockchain", "opensea", "seedphrase",
  "wallet", "sparkasse", "volksbank", "commerzbank", "deutschebank", "postbank", "=ing", "chase", "wellsfargo",
  "citibank", "=hsbc", "barclays", "revolut", "=n26", "klarna", "=dhl", "fedex", "=ups", "=usps", "=dpd",
  "ebay", "steam", "roblox", "discord", "login", "signin", "logon", "verify", "verification", "password",
  "banking", "=bank", "recovery", "unlock", "=secure",
];

export const SETTING_DEFS: SettingDef[] = [
  {
    key: "signup_mode",
    group: "Sign-up & accounts",
    label: "Who can create an account",
    help: "Invite only: people can sign up only through a valid team invite link. The very first account is always allowed.",
    kind: "signup",
    env: "TUND_ALLOW_SIGNUP",
    fallback: () => (env("TUND_ALLOW_SIGNUP") === "true" ? "open" : "closed"),
  },
  {
    key: "require_email_verification",
    group: "Sign-up & accounts",
    label: "Require email verification",
    help: "New accounts must click the emailed link before they can log in a terminal or create tokens. Only applies while email is configured.",
    kind: "bool",
    fallback: () => false,
  },
  {
    key: "notify_admins_on_signup",
    group: "Sign-up & accounts",
    label: "Email admins about new sign-ups",
    help: "Sends a short notice to every administrator when someone creates an account.",
    kind: "bool",
    fallback: () => false,
  },
  {
    key: "limit_tunnels",
    group: "Limits",
    label: "Online tunnels per account",
    help: "0 = unlimited. Admins are exempt.",
    kind: "int",
    env: "TUND_MAX_TUNNELS_PER_USER",
    min: 0,
    max: 100000,
    fallback: () => envInt("TUND_MAX_TUNNELS_PER_USER", 0),
  },
  {
    key: "limit_pinned",
    group: "Limits",
    label: "Static hostnames per account or team",
    help: "0 = unlimited. Admins are exempt.",
    kind: "int",
    env: "TUND_MAX_PINNED_PER_USER",
    min: 0,
    max: 100000,
    fallback: () => envInt("TUND_MAX_PINNED_PER_USER", 0),
  },
  {
    key: "custom_domains",
    group: "Limits",
    label: "Custom domains",
    help: "Let accounts bring their own domains. Off: only admins and accounts you turn it on for under Users can add and use them. Turning it off disconnects custom-domain tunnels of accounts without it.",
    kind: "bool",
    env: "TUND_CUSTOM_DOMAINS",
    fallback: () => envBool("TUND_CUSTOM_DOMAINS", false),
  },
  {
    key: "passthrough",
    group: "Limits",
    label: "TCP and TLS tunnels",
    help: "Let accounts open raw TCP tunnels and TLS passthrough tunnels. Off: only admins and accounts you turn it on for under Users can open them. Turning it off disconnects such tunnels of accounts without it.",
    kind: "bool",
    env: "TUND_PASSTHROUGH",
    fallback: () => envBool("TUND_PASSTHROUGH", false),
  },
  {
    key: "limit_domains",
    group: "Limits",
    label: "Custom domains per account or team",
    help: "0 = unlimited. Admins are exempt.",
    kind: "int",
    env: "TUND_MAX_DOMAINS_PER_USER",
    min: 0,
    max: 100000,
    fallback: () => envInt("TUND_MAX_DOMAINS_PER_USER", 0),
  },
  {
    key: "limit_teams",
    group: "Limits",
    label: "Teams an account may own",
    help: "0 = unlimited. Admins are exempt.",
    kind: "int",
    env: "TUND_MAX_TEAMS_PER_USER",
    min: 0,
    max: 100000,
    fallback: () => envInt("TUND_MAX_TEAMS_PER_USER", 3),
  },
  {
    key: "limit_bandwidth_kbps",
    group: "Limits",
    label: "Throughput per account (kbit/s, each direction)",
    help: "0 = unlimited. Admins are exempt unless they have their own override.",
    kind: "int",
    env: "TUND_BANDWIDTH_KBPS",
    min: 0,
    max: 100_000_000,
    fallback: () => envInt("TUND_BANDWIDTH_KBPS", 0),
  },
  {
    key: "limit_transfer_gb",
    group: "Limits",
    label: "Transfer per account and month (GB)",
    help: "Both directions together, 1 GB = 10⁹ bytes, reset on the 1st (UTC). 0 = unlimited.",
    kind: "int",
    env: "TUND_TRANSFER_GB",
    min: 0,
    max: 10_000_000,
    fallback: () => envInt("TUND_TRANSFER_GB", 0),
  },
  {
    key: "limit_tunnel_lifetime",
    group: "Limits",
    label: "Maximum tunnel lifetime (minutes)",
    help: "Tunnels are closed this many minutes after they start; the client can start them again. 0 = unlimited. Admins are exempt unless you set a value for them under Users. Applies to running tunnels too.",
    kind: "int",
    env: "TUND_MAX_TUNNEL_LIFETIME",
    min: 0,
    max: 525_600,
    fallback: () => envInt("TUND_MAX_TUNNEL_LIFETIME", 0),
  },
  {
    key: "auto_pin",
    group: "Limits",
    label: "Give new accounts a static hostname on first run",
    help: "tund http then keeps the same URL between runs.",
    kind: "bool",
    env: "TUND_AUTO_PIN",
    fallback: () => envBool("TUND_AUTO_PIN", true),
  },
  {
    key: "browser_warning",
    group: "Abuse protection",
    label: "Browser warning page",
    help: "Browsers visiting unprotected tunnels on the base domain see a one-time warning first.",
    kind: "bool",
    env: "TUND_BROWSER_WARNING",
    fallback: () => envBool("TUND_BROWSER_WARNING", false),
  },
  {
    key: "abuse_contact",
    group: "Abuse protection",
    label: "Abuse contact",
    help: "Email address or https URL for “Report abuse” links. Empty hides them.",
    kind: "string",
    env: "TUND_ABUSE_CONTACT",
    fallback: () => env("TUND_ABUSE_CONTACT"),
  },
  {
    key: "retention_days",
    group: "Traffic capture & retention",
    label: "Keep captured requests for (days)",
    help: "Older requests are deleted by the server.",
    kind: "int",
    env: "TUND_RETENTION_DAYS",
    min: 1,
    max: 3650,
    fallback: () => envInt("TUND_RETENTION_DAYS", 7),
  },
  {
    key: "capture_max_body",
    group: "Traffic capture & retention",
    label: "Captured body size (bytes)",
    help: "How much of each request and response body is stored. 1,024 to 10,485,760.",
    kind: "int",
    env: "TUND_CAPTURE_MAX_BODY",
    min: 1024,
    max: 10485760,
    fallback: () => envInt("TUND_CAPTURE_MAX_BODY", 262144),
  },
  {
    key: "turnstile_site_key",
    group: "Sign-up & accounts",
    label: "Cloudflare Turnstile site key",
    help: "With both keys set, sign-up, password reset and the abuse form ask for a Turnstile check.",
    kind: "string",
    fallback: () => "",
  },
  {
    key: "turnstile_secret_key",
    group: "Sign-up & accounts",
    label: "Cloudflare Turnstile secret key",
    help: "Stored on the server and never shown again.",
    kind: "secret",
    fallback: () => "",
  },
  {
    key: "block_disposable_emails",
    group: "Sign-up & accounts",
    label: "Reject disposable email addresses",
    help: "Sign-ups from throwaway mail services (built-in list) are refused.",
    kind: "bool",
    fallback: () => true,
  },
  {
    key: "signup_rate_limit",
    group: "Sign-up & accounts",
    label: "Sign-ups per IP address and hour",
    help: "0 = no limit.",
    kind: "int",
    min: 0,
    max: 10000,
    fallback: () => 5,
  },
  {
    key: "untrusted_custom_domains",
    group: "Abuse protection",
    label: "Custom domains of non-trusted accounts",
    help: "Review: each new custom domain waits for an admin's approval, with risk signals, under Admin → Abuse and Domains.",
    kind: "enum",
    env: "TUND_UNTRUSTED_CUSTOM_DOMAINS",
    options: [
      ["review", "Allowed after admin review"],
      ["allow", "Allowed right away"],
      ["deny", "Not allowed"],
    ],
    fallback: () => {
      const v = env("TUND_UNTRUSTED_CUSTOM_DOMAINS");
      return v === "allow" || v === "deny" ? v : v === "true" ? "allow" : v === "false" ? "deny" : "review";
    },
  },
  {
    key: "warn_custom_domains",
    group: "Abuse protection",
    label: "Browser warning on custom domains of non-trusted accounts",
    help: "Otherwise the warning page only appears on the base domain.",
    kind: "bool",
    fallback: () => true,
  },
  {
    key: "custom_domain_min_age_days",
    group: "Abuse protection",
    label: "Flag custom domains younger than (days)",
    help: "A review hint: recently registered domains are marked high risk.",
    kind: "int",
    min: 0,
    max: 3650,
    fallback: () => 30,
  },
  {
    key: "untrusted_tcp",
    group: "Abuse protection",
    label: "Non-trusted accounts may open TCP tunnels",
    help: "Off: only trusted accounts and admins get TCP tunnels.",
    kind: "bool",
    env: "TUND_UNTRUSTED_TCP",
    fallback: () => envBool("TUND_UNTRUSTED_TCP", true),
  },
  {
    key: "untrusted_tls",
    group: "Abuse protection",
    label: "Non-trusted accounts may open TLS passthrough tunnels",
    help: "TLS passthrough bypasses the browser warning, so consider turning this off on public servers.",
    kind: "bool",
    env: "TUND_UNTRUSTED_TLS",
    fallback: () => envBool("TUND_UNTRUSTED_TLS", true),
  },
  {
    key: "blocked_hostname_words",
    group: "Abuse protection",
    label: "Words non-trusted accounts can't use in hostnames",
    help: "One per line or comma separated. Matches anywhere in a label, ignoring dashes; start a word with = to match only a whole dash-separated part (=bank blocks my-bank but not bankrupt).",
    kind: "list",
    fallback: () => DEFAULT_BLOCKED_WORDS,
  },
  {
    key: "safe_browsing_api_key",
    group: "Abuse protection",
    label: "Google Safe Browsing API key",
    help: "Online tunnel URLs are checked every 10 minutes; hits are blocked and reported. Google's Lookup API is for non-commercial use (commercial operators: Web Risk API).",
    kind: "secret",
    env: "TUND_SAFE_BROWSING_API_KEY",
    fallback: () => env("TUND_SAFE_BROWSING_API_KEY"),
  },
  {
    key: "phishing_heuristics",
    group: "Abuse protection",
    label: "Scan pages of non-trusted accounts for phishing",
    help: "Scores captured HTML (password fields, brand names, “verify your account”, …) and files reports.",
    kind: "bool",
    fallback: () => true,
  },
  {
    key: "phishing_auto_block",
    group: "Abuse protection",
    label: "Block hostnames on a phishing match",
    help: "Off: matches are only reported and the account is flagged.",
    kind: "bool",
    fallback: () => false,
  },
  {
    key: "terms_markdown",
    group: "Legal pages",
    label: "Terms of service (/terms)",
    help: "Markdown. Leave empty to use the generic default text. Sign-up asks people to accept these and the acceptable use policy.",
    kind: "markdown",
    fallback: () => "",
  },
  {
    key: "acceptable_use_markdown",
    group: "Legal pages",
    label: "Acceptable use policy (/acceptable-use)",
    help: "Markdown. Leave empty to use the generic default text.",
    kind: "markdown",
    fallback: () => "",
  },
  {
    key: "instance_name",
    group: "Branding",
    label: "Instance name",
    help: "Shown in page titles and emails.",
    kind: "string",
    fallback: () => "TUNd",
  },
];

const DEF = new Map(SETTING_DEFS.map((d) => [d.key, d]));

export function defaults(): Settings {
  const out = Object.fromEntries(SETTING_DEFS.map((d) => [d.key, d.fallback()])) as Omit<Settings, "smtp" | "oauth">;
  return { ...out, smtp: null, oauth: {} };
}

/** Validates a stored or submitted value for `key`; returns null when it's unusable. */
export function coerce(key: EditableKey, raw: unknown): Settings[EditableKey] | null {
  const d = DEF.get(key);
  if (!d) return null;
  switch (d.kind) {
    case "bool":
      return typeof raw === "boolean" ? raw : raw === "true" || raw === "on" ? true : raw === "false" || raw === "" ? false : null;
    case "int": {
      const n = typeof raw === "number" ? raw : Number.parseInt(String(raw ?? "").replace(/[_,\s]/g, ""), 10);
      if (!Number.isInteger(n) || (d.min !== undefined && n < d.min) || (d.max !== undefined && n > d.max)) return null;
      return n;
    }
    case "string":
      return typeof raw === "string" ? raw.trim().slice(0, 200) : null;
    case "signup":
      return raw === "open" || raw === "invite" || raw === "closed" ? raw : null;
    case "enum":
      // untrusted_custom_domains also accepts the legacy booleans (true = allow, false = deny).
      if (key === "untrusted_custom_domains" && typeof raw === "boolean") return raw ? "allow" : "deny";
      return d.options?.some(([v]) => v === raw) ? (raw as Settings[EditableKey]) : null;
    case "list": {
      const items = Array.isArray(raw) ? raw : typeof raw === "string" ? raw.split(/[\n,]+/) : null;
      if (!items) return null;
      const words = items.map((w) => String(w).trim().toLowerCase()).filter(Boolean).slice(0, 2000);
      return [...new Set(words)];
    }
    case "secret":
      return typeof raw === "string" ? raw.trim().slice(0, 500) : null;
    case "markdown":
      return typeof raw === "string" ? raw.replace(/\r\n/g, "\n").slice(0, 100_000) : null;
  }
}

type Cache = { at: number; value: Settings; stored: Set<string> };
const g = globalThis as unknown as { __tundSettings?: Cache };
const TTL = 5000;

async function load(): Promise<Cache> {
  const rows = await db()`select key, value from settings`;
  const value = defaults();
  const stored = new Set<string>();
  for (const r of rows) {
    const key = r.key as SettingKey;
    if (key === "smtp") {
      const v = r.value as SmtpSettings;
      if (v && typeof v.host === "string" && v.host) {
        value.smtp = {
          host: v.host,
          port: Number(v.port) || 587,
          security: v.security === "tls" || v.security === "none" ? v.security : "starttls",
          username: v.username ?? "",
          password_enc: v.password_enc ?? "",
          from_email: v.from_email ?? "",
          from_name: v.from_name ?? "",
        };
        stored.add(key);
      }
      continue;
    }
    if (key === "oauth") {
      const v = (r.value ?? {}) as Record<string, Partial<OAuthProviderSettings>>;
      for (const p of ["google", "github"] as const) {
        const c = v[p];
        if (c && typeof c === "object") {
          value.oauth[p] = {
            enabled: c.enabled === true,
            client_id: typeof c.client_id === "string" ? c.client_id : "",
            client_secret_enc: typeof c.client_secret_enc === "string" ? c.client_secret_enc : "",
          };
        }
      }
      stored.add(key);
      continue;
    }
    if (!DEF.has(key as EditableKey)) continue;
    const c = coerce(key as EditableKey, r.value);
    if (c !== null) {
      (value as Record<string, unknown>)[key] = c;
      stored.add(key);
    }
  }
  return { at: Date.now(), value, stored };
}

/** Effective settings, cached for a few seconds per process. */
export async function getSettings(): Promise<Settings> {
  if (!g.__tundSettings || Date.now() - g.__tundSettings.at > TTL) g.__tundSettings = await load();
  return g.__tundSettings.value;
}

/** Keys that currently have a stored value (the rest come from env/defaults). */
export async function storedKeys(): Promise<Set<string>> {
  await getSettings();
  return g.__tundSettings!.stored;
}

export function invalidateSettings() {
  g.__tundSettings = undefined;
}

/** Upserts (value !== undefined) or deletes (value === undefined) rows, then tells tund-server. */
export async function writeSettings(changes: Partial<Record<SettingKey, unknown>>, actorId: string | null) {
  const sql = db();
  await sql.begin(async (tx) => {
    for (const [key, value] of Object.entries(changes)) {
      if (value === undefined) await tx`delete from settings where key = ${key}`;
      else
        await tx`
          insert into settings (key, value, updated_by) values (${key}, ${tx.json(value as never)}, ${actorId})
          on conflict (key) do update set value = excluded.value, updated_at = now(), updated_by = excluded.updated_by`;
    }
  });
  invalidateSettings();
  await notify("tund_config", { kind: "settings" });
}

// --- SMTP password encryption ------------------------------------------------

function smtpKey(): Buffer {
  return createHash("sha256").update(`tund-settings\x00${process.env.TUND_INTERNAL_SECRET ?? ""}`).digest();
}

export function encryptSecret(plain: string): string {
  const nonce = randomBytes(12);
  const c = createCipheriv("aes-256-gcm", smtpKey(), nonce);
  const body = Buffer.concat([c.update(plain, "utf8"), c.final(), c.getAuthTag()]);
  return Buffer.concat([nonce, body]).toString("base64url");
}

export function decryptSecret(enc: string): string | null {
  if (!enc) return "";
  try {
    const raw = Buffer.from(enc, "base64url");
    const nonce = raw.subarray(0, 12);
    const tag = raw.subarray(raw.length - 16);
    const body = raw.subarray(12, raw.length - 16);
    const d = createDecipheriv("aes-256-gcm", smtpKey(), nonce);
    d.setAuthTag(tag);
    return Buffer.concat([d.update(body), d.final()]).toString("utf8");
  } catch {
    return null;
  }
}

/** Env var name and default shown next to a field in the admin UI. */
export function envDefaultLabel(def: SettingDef): string {
  const v = def.fallback();
  const shown =
    def.kind === "secret"
      ? v
        ? "set"
        : "not set"
      : def.kind === "markdown"
        ? "the built-in text"
        : Array.isArray(v)
          ? `built-in list (${v.length} words)`
          : def.kind === "enum"
            ? (def.options?.find(([o]) => o === v)?.[1] ?? String(v))
            : typeof v === "boolean"
              ? v
                ? "on"
                : "off"
              : v === ""
                ? "empty"
                : String(v);
  return def.env ? `${shown} (${def.env}${env(def.env) ? "" : " not set"})` : `${shown} (built in)`;
}

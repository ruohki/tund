import "server-only";
import { Marked } from "marked";
import { abuseHref, config } from "./config";
import { formatBytes } from "./format";
import { smtpConfigured, verificationRequired } from "./mail";
import { enabledProviders } from "./oauth";
import { PROVIDER_LABEL } from "./oauth-shared";
import { getSettings } from "./settings";

// /terms, /acceptable-use and /privacy: operator markdown from settings, or
// generic defaults naming the instance. The defaults describe how the service
// works and make no legal claims; operators replace them under Admin →
// Settings. /imprint has no default: only the operator knows who they are.

const escapeHtml = (s: string) =>
  s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;").replace(/'/g, "&#39;");

/** http(s), mailto and relative links only. */
function safeHref(href: string): string | null {
  // Browsers ignore whitespace and control characters inside a scheme ("java\tscript:").
  const probe = href.replace(/[\u0000- \u007f]/g, "");
  if (/^[a-z][a-z0-9+.-]*:/i.test(probe) && !/^(https?|mailto):/i.test(probe)) return null;
  if (probe.startsWith("//") || probe.startsWith("\\")) return null;
  return href;
}

const markdown = new Marked({
  gfm: true,
  renderer: {
    // Raw HTML in the source is shown as text, never rendered.
    html({ text }) {
      return escapeHtml(text);
    },
    link({ href, title, tokens }) {
      const inner = this.parser.parseInline(tokens);
      const safe = safeHref(href);
      if (!safe) return inner;
      const external = /^https?:/i.test(safe);
      return `<a href="${escapeHtml(safe)}"${title ? ` title="${escapeHtml(title)}"` : ""}${external ? ' rel="noopener noreferrer nofollow"' : ""}>${inner}</a>`;
    },
    // No remote images (tracking pixels); the alt text stays.
    image({ text }) {
      return escapeHtml(text);
    },
  },
});

export function renderMarkdown(src: string): string {
  return markdown.parse(src, { async: false });
}

export type Page = "terms" | "acceptable-use" | "privacy" | "imprint";

async function defaults() {
  const s = await getSettings();
  const c = config();
  return { name: s.instance_name || "TUNd", host: c.dashboardHost, retention: s.retention_days };
}

async function defaultTerms(): Promise<string> {
  const { name, host, retention } = await defaults();
  return `# Terms of Service

These terms apply to your use of ${name} at ${host}, a service that makes programs running on your computer reachable through public addresses ("tunnels"). By creating an account you agree to them.

## Your account

- You're responsible for everything done with your account, your auth tokens and the terminals you log in.
- Keep your password and auth tokens secret, and revoke tokens you no longer use.
- Use an email address you can receive mail at; we use it for sign-in links and important notices.

## Your tunnels

- You're responsible for everything served through your tunnels and custom domains, including content produced by software you run.
- Follow the [Acceptable Use Policy](/acceptable-use).
- The operators of ${name} may stop tunnels, block addresses, remove domains or disable accounts that break these terms or put visitors at risk, with or without notice.

## Traffic data

- Requests through your tunnels are recorded (headers, and bodies up to a size limit) so you can inspect and replay them in the dashboard.${retention > 0 ? ` Records are deleted after ${retention} ${retention === 1 ? "day" : "days"}.` : ""}
- The operators can look at this data when it's needed to run the service or to investigate abuse.
- The [Privacy Policy](/privacy) describes all personal data ${name} processes.

## The service

- ${name} is provided as is. Addresses, limits and features can change, and the service can be unavailable at times.
- Limits on tunnels, addresses and transfer apply as shown in your dashboard.

## Changes

These terms can change. The current version is always on this page; continuing to use ${name} after a change means you accept it.

## Contact

Questions about these terms, or a tunnel that breaks them? [Report abuse](/report) or contact the operators of ${name}.
`;
}

async function defaultAcceptableUse(): Promise<string> {
  const { name } = await defaults();
  return `# Acceptable Use Policy

${name} is for sharing your own work: development servers, demos, webhooks, remote access to your own machines. Don't use it to harm visitors or other people.

## Not allowed

- **Phishing and impersonation**: pages that imitate another organization's sign-in, payment or account pages, or that ask for passwords, payment details or wallet recovery phrases under false pretenses.
- **Malware**: distributing viruses, ransomware, spyware or other harmful software, or running command-and-control servers.
- **Fraud and scams**: fake shops, investment or giveaway scams, fake support pages.
- **Spam**: sending unsolicited bulk messages, or hosting pages advertised through them.
- **Illegal content**: anything that is illegal where ${name} operates or where you are, including content that sexually exploits minors.
- **Attacks**: scanning, attacking or overloading other systems, or running open proxies that hide where traffic comes from.
- **Infringing others' rights**: sharing content you don't have the rights to, or collecting personal data without a lawful reason.
- **Getting around restrictions**: creating accounts or rotating addresses to evade limits, blocks or a disabled account.

## Enforcement

Automated checks and reports from visitors can lead to tunnels being stopped, hostnames being blocked and accounts being disabled. Severe cases may be passed on to the responsible authorities.

## Reporting

Seen a tunnel that breaks these rules? [Report it](/report) and include the address and what you saw.
`;
}

const days = (n: number) => `${n} ${n === 1 ? "day" : "days"}`;

/**
 * Generated from the live settings, so it only names what this instance does:
 * third parties appear only while they're configured. Every statement follows
 * the code (retention in internal/server/store.go Prune and
 * lib/inactive-accounts.ts, cookies in lib/auth.ts, lib/oauth.ts,
 * lib/two-factor.ts and internal/server/access.go + warning.go).
 */
async function defaultPrivacy(): Promise<string> {
  const s = await getSettings();
  const { dashboardHost } = config();
  const name = s.instance_name || "TUNd";
  const [providers, mail, verifying] = await Promise.all([enabledProviders(), smtpConfigured(), verificationRequired()]);
  const retention = days(s.retention_days);
  const auditDays = s.audit_retention_days;
  const contact = abuseHref(s.abuse_contact);
  const imprint = Boolean(s.imprint_markdown.trim());
  const reachOperator = imprint
    ? "the operator named in the [imprint](/imprint)"
    : contact
      ? `the operators of ${name} ([${s.abuse_contact}](${contact}))`
      : `the operators of ${name}`;
  const responsible = imprint
    ? "The operator named in the [imprint](/imprint) is responsible for your data (the controller under the GDPR). Contact them with any question about your data."
    : `The operators of ${name} are responsible for your data (the controller under the GDPR).${contact ? ` Write to [${s.abuse_contact}](${contact}) with any question about your data.` : ""}`;
  const review = s.untrusted_custom_domains === "review";
  const turnstile = Boolean(s.turnstile_site_key && s.turnstile_secret_key);
  const inactive = s.inactive_account_months > 0 && mail;
  const unconfirmed = s.unverified_account_days > 0 && verifying;
  const providerNames = providers.map((p) => PROVIDER_LABEL[p]).join(" or ");

  const others = [
    s.billing.enabled
      ? `- **Stripe** handles payments. When you subscribe, Stripe receives your email address and name and collects your billing address and payment details, which it processes under its own privacy policy. Invoices and billing records are kept for the retention periods of tax and commercial law (Art. 6(1)(c) GDPR).`
      : "",
    providers.length
      ? `- **${providerNames}**: if you sign in with ${providerNames}, you're sent to them, and they tell ${name} your account id, your verified email address and your name.`
      : "",
    turnstile
      ? `- **Cloudflare Turnstile** checks the sign-up, password reset and abuse report forms. It loads from Cloudflare, which receives your IP address and information about your browser.`
      : "",
    s.safe_browsing_api_key
      ? `- **Google Safe Browsing**: the addresses of online tunnels of accounts that aren't trusted${review ? ", and of custom domains under review," : ""} are sent to Google to check them against its list of dangerous sites.`
      : "",
    review
      ? `- **RDAP** (rdap.org): when a custom domain is reviewed, its registration date is looked up there, which sends the domain name.`
      : "",
  ].filter(Boolean);

  return `# Privacy Policy

This policy explains which personal data ${name} at ${dashboardHost} processes, why, and for how long. It covers people with an account and visitors of the tunnels it serves.

## Who is responsible

${responsible}

## Your account

- **Account details**: your email address, your name, a hash of your password (never the password itself) and when you last used the account. If you connect ${providers.length ? providerNames : "Google or GitHub"}, the account id and email address they report. With two-factor authentication, its secret (encrypted) and hashed recovery codes; with passkeys, their public keys. The teams you belong to, and invitations with the address they were sent to. Kept while your account exists, to provide the service (Art. 6(1)(b) GDPR).
- **Signed-in browsers**: the IP address and browser of each dashboard session, so you can see and end them. Deleted when the session expires, 30 days after its last use (Art. 6(1)(b) and (f) GDPR: the security of your account).
- **Terminals**: auth tokens are stored as hashes with the time they were last used. For each client connection, the client version, operating system, machine name and IP address, deleted ${retention} after it disconnects. Logging in a terminal keeps its machine name, operating system and IP address until the login is done or has expired (Art. 6(1)(b) GDPR).
- **Tunnels and usage**: hostnames, the local address each tunnel forwards to, when it ran, and how much data it transferred per day, for the limits of your plan. Kept while your account exists (Art. 6(1)(b) GDPR).${
    mail
      ? `
- **Email**: confirmation links, password resets, team invitations and security notices${inactive ? ", and the notice before an inactive account is deleted," : ""} are sent through ${name}'s mail server (Art. 6(1)(b) GDPR).`
      : ""
  }

## Traffic through tunnels

Visitors of tunnels are affected too, without an account.

- **Recorded requests**: requests to HTTP tunnels are recorded with their headers and up to ${formatBytes(s.capture_max_body)} of each request and response body, so the tunnel's owner can inspect and replay them. That includes the visitor's IP address and whatever the request carries, such as cookies and form fields. Deleted after ${retention}.
- **TCP and TLS tunnels**: the visitor's IP address, the duration and the bytes of each connection, deleted after ${retention}.
- **Protected tunnels**: after a visitor enters a tunnel's password or signs in, a cookie on its hostname keeps them signed in for 7 days. With single sign-on, the identity that the owner's sign-in provider reports (such as email address and name) is passed on to the owner's service.

${name} does this for the account holder who runs the tunnel (Art. 6(1)(b) GDPR) and in its legitimate interest in running a tunnel service (Art. 6(1)(f) GDPR). What a tunnel's own service does with visitors' data is up to its owner; ask them.

## Security and abuse prevention

To keep the service and its visitors safe (Art. 6(1)(f) GDPR):

- **Audit log**: security-relevant changes to accounts (sign-up, sign-in methods, passwords, two-factor authentication) and the actions of administrators, with the email address and IP address of whoever made them. ${auditDays > 0 ? `Deleted after ${days(auditDays)}.` : "Kept without a time limit."}
- **Abuse reports**: reports about tunnels, with the reporter's email address (if given) and IP address. ${auditDays > 0 ? `${days(auditDays)} after a report is closed, the reporter's email and IP address are removed.` : "Kept without a time limit."}${
    s.phishing_heuristics
      ? `
- **Phishing checks**: pages served by tunnels of accounts that aren't trusted are scanned for signs of phishing.`
      : ""
  }

## Deleting your account

You can delete your account at any time under [Settings](/settings). This removes your tunnels, hostnames, domains, tokens and recorded traffic.${
    inactive
      ? ` Accounts that nobody signs in to, uses a token of or connects a client with for ${s.inactive_account_months} ${s.inactive_account_months === 1 ? "month" : "months"} are deleted; the owner gets an email 30 days before, and signing in keeps the account.`
      : ""
  }${unconfirmed ? ` Accounts whose email address isn't confirmed are deleted ${days(s.unverified_account_days)} after the last confirmation link.` : ""}
${
  others.length
    ? `
## Services of other companies

${others.join("\n")}
`
    : ""
}
## Cookies and storage

${name} uses no analytics, advertising or tracking. It only stores what signing in and using the service need:

- a cookie that keeps you signed in to the dashboard, for 30 days after your last visit;
- short-lived cookies while you sign in${providers.length ? ` (also with ${providerNames})` : ""}, confirm a second factor or use a passkey, and while visitors sign in to a protected tunnel: at most 10 minutes;
- on tunnel hostnames, the 7-day cookie of a protected tunnel${s.browser_warning ? ", and a 7-day cookie that remembers a visitor saw the warning page before a tunnel" : ""};
- your choice of light or dark theme, in your browser's local storage.

## Your rights

You have the right to access your data, to have it corrected or deleted, to restrict or object to its processing, and to receive it in a portable format (Art. 15 to 21 GDPR). Where processing is based on your consent, you can withdraw it at any time. You can change your name and email address and delete your account under [Settings](/settings); for anything else, contact ${reachOperator}. You also have the right to complain to a data protection supervisory authority, for example the one where you live.

## Changes

This policy changes when ${name} changes. The current version is always on this page.
`;
}

/** The page's markdown: the operator's text from settings, or the built-in default (none for the imprint). */
export async function legalMarkdown(page: Page): Promise<{ markdown: string; custom: boolean }> {
  const s = await getSettings();
  const custom = {
    terms: s.terms_markdown,
    "acceptable-use": s.acceptable_use_markdown,
    privacy: s.privacy_markdown,
    imprint: s.imprint_markdown,
  }[page].trim();
  if (custom) return { markdown: custom, custom: true };
  const fallback = { terms: defaultTerms, "acceptable-use": defaultAcceptableUse, privacy: defaultPrivacy, imprint: async () => "" }[page];
  return { markdown: await fallback(), custom: false };
}

/** Whether the operator published an imprint; links to /imprint only appear then. */
export async function hasImprint(): Promise<boolean> {
  return Boolean((await getSettings()).imprint_markdown.trim());
}

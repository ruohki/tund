import "server-only";
import { Marked } from "marked";
import { abuseHref, config } from "./config";
import { formatBytes } from "./format";
import { smtpConfigured, verificationRequired } from "./mail";
import { enabledProviders } from "./oauth";
import { PROVIDER_LABEL } from "./oauth-shared";
import { getSettings, type Settings } from "./settings";

// /terms, /acceptable-use, /privacy and /imprint: operator markdown from
// settings, or defaults naming the instance. The defaults describe how the
// service works and make no legal claims; operators replace them under Admin →
// Settings. The imprint is generated from the operator's details (Admin →
// Settings → Imprint) and doesn't exist until they're filled in.

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

/** Escapes operator input so it shows as typed instead of turning into Markdown. */
const md = (s: string) => s.replace(/[\\`*_{}[\]()#+\-.!|<>~]/g, "\\$&");

const EMAIL = /^[^\s@<>()[\]\\,;:"]+@[^\s@<>()[\]\\,;:"]+\.[^\s@<>()[\]\\,;:"]+$/;
const mailLink = (email: string) => (EMAIL.test(email) ? `[${md(email)}](mailto:${email})` : md(email));

/** The address field's non-empty lines. */
const addressLines = (s: Settings) =>
  s.imprint_address
    .split("\n")
    .map((l) => l.trim())
    .filter(Boolean);

/** Operator, address and email are filled in, so the imprint can be generated. */
const imprintDetails = (s: Settings) => Boolean(s.imprint_operator.trim() && addressLines(s).length && s.imprint_email.trim());

/**
 * Whether /imprint exists (the operator's own text, or the details to generate
 * it); links to it only appear then. Mirrored in internal/server/settings.go.
 */
export function imprintReady(s: Settings): boolean {
  return Boolean(s.imprint_markdown.trim()) || imprintDetails(s);
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
  // retention_days 0 (only possible through TUND_RETENTION_DAYS) turns pruning off.
  const retention = days(s.retention_days);
  const pruned = (when: string) => (s.retention_days > 0 ? `deleted ${retention} ${when}` : "kept without a time limit");
  const auditDays = s.audit_retention_days;
  const contact = abuseHref(s.abuse_contact);
  const imprint = imprintReady(s);
  const operator = md(s.imprint_operator.trim());
  const email = mailLink(s.imprint_email.trim());
  const reachOperator = imprintDetails(s)
    ? `${operator} (${email})`
    : imprint
      ? "the operator named in the [imprint](/imprint)"
      : contact
        ? `the operators of ${name} ([${s.abuse_contact}](${contact}))`
        : `the operators of ${name}`;
  const responsible = imprintDetails(s)
    ? `**${operator}**, ${addressLines(s).map(md).join(", ")}, is responsible for your data (the controller under the GDPR). Write to ${email} with any question about your data; the [imprint](/imprint) has all contact details.`
    : imprint
      ? "The operator named in the [imprint](/imprint) is responsible for your data (the controller under the GDPR). Contact them with any question about your data."
      : `The operators of ${name} are responsible for your data (the controller under the GDPR).${contact ? ` Write to [${s.abuse_contact}](${contact}) with any question about your data.` : ""}`;
  const review = s.untrusted_custom_domains === "review";
  const turnstile = Boolean(s.turnstile_site_key && s.turnstile_secret_key);
  const supportPages = [s.support_landing_widget_key ? "the landing page" : "", s.support_app_widget_key ? "the dashboard" : ""].filter(Boolean);
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
    supportPages.length
      ? `- **Support chat** (support.till.md) is offered on ${supportPages.join(" and ")}. It loads from support.till.md, which receives your IP address and information about your browser. When you write to support, it also receives what you enter (your message, attachments${s.support_landing_widget_key ? ", and on the landing page your name and email address" : ""}) and the address, title and referrer of the page you're on, your language, time zone and screen size.${
          s.support_app_widget_key
            ? ` While you're signed in, it receives your email address, name and account id, your plan, whether you're an administrator or trusted and have confirmed your email address, and when your account was created, so support knows who is asking. A request from the dashboard also carries how many tunnels you have online, your static hostnames, custom domains and teams, and the version and operating system of your last client.`
            : ""
        }`
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

- **Account details**: your email address, your name, a hash of your password (never the password itself) and when you last used the account. If you connect ${providers.length ? providerNames : "Google or GitHub"}, the account id and email address they report. With two-factor authentication, its secret (encrypted) and hashed recovery codes; with passkeys, their public keys. The teams you belong to. Kept while your account exists, to provide the service (Art. 6(1)(b) GDPR).
- **Team invitations**: who sent an invitation, its role and the email address it was sent to, if it names one. Deleted 30 days after it expires; invitations are valid for 7 days (Art. 6(1)(b) and (f) GDPR).
- **Signed-in browsers**: the IP address and browser of each dashboard session, so you can see and end them. Deleted when the session expires, 30 days after its last use (Art. 6(1)(b) and (f) GDPR: the security of your account).
- **Terminals**: auth tokens are stored as hashes with the time they were last used; a token created by logging in a terminal is named after its machine. For each client connection, the client version, operating system, machine name and IP address, ${pruned("after it disconnects")}. While a terminal login is pending, its machine name, operating system and IP address are kept until it's done or has expired (Art. 6(1)(b) GDPR).
- **Tunnels and usage**: for each tunnel, its hostname, the local address it forwards to and when it ran, ${s.retention_days > 0 ? `deleted with its client connection (${retention} after it disconnects)` : "kept without a time limit"}. Your reserved hostnames and domains, the access rules of protected tunnels (such as allowed email addresses and IP addresses) and how much data you transferred per day, for the limits of your plan, are kept while your account exists (Art. 6(1)(b) GDPR).${
    mail
      ? `
- **Email**: confirmation links, password resets, team invitations and security notices${inactive ? ", and the notice before an inactive account is deleted," : ""} are sent through ${name}'s mail server. Links are stored as hashes with the address they were sent to, while your account exists, to know which addresses you confirmed (Art. 6(1)(b) GDPR).`
      : ""
  }

## Traffic through tunnels

Visitors of tunnels are affected too, without an account.

- **Recorded requests**: requests to HTTP tunnels are recorded with their headers and up to ${formatBytes(s.capture_max_body)} of each request and response body, so the tunnel's owner can inspect and replay them. That includes the visitor's IP address and whatever the request carries, such as cookies and form fields. ${s.retention_days > 0 ? `Deleted after ${retention}.` : "Kept without a time limit."}
- **TCP and TLS tunnels**: the visitor's IP address, the duration and the bytes of each connection, ${s.retention_days > 0 ? `deleted after ${retention}` : "kept without a time limit"}.
- **Protected tunnels**: after a visitor enters a tunnel's password or signs in, a cookie on its hostname keeps them signed in for 7 days. With single sign-on, the identity that the owner's sign-in provider reports (such as email address and name) is passed on to the owner's service and recorded with each request.

${name} does this for the account holder who runs the tunnel (Art. 6(1)(b) GDPR) and in its legitimate interest in running a tunnel service (Art. 6(1)(f) GDPR). What a tunnel's own service does with visitors' data is up to its owner; ask them.

## Security and abuse prevention

To keep the service and its visitors safe (Art. 6(1)(f) GDPR):

- **Audit log**: security-relevant events of accounts (sign-up, passkey sign-ins, sign-in methods, passwords, email address changes, two-factor authentication) and the actions of administrators, with the email address and IP address of whoever made them. ${auditDays > 0 ? `Deleted after ${days(auditDays)}.` : "Kept without a time limit."}
- **Abuse reports**: reports about tunnels, with the reporter's email address (if given) and IP address. ${auditDays > 0 ? `${days(auditDays)} after a report is closed, the reporter's email and IP address are removed; the report itself is kept to recognize repeated abuse.` : "Kept without a time limit."}
- **Trust and flags**: administrators can mark accounts as trusted, or flag them for review with a reason, also automatically, for example after a phishing match. Kept while the account exists.${
    s.phishing_heuristics
      ? `
- **Phishing checks**: pages served by tunnels of accounts that aren't trusted are scanned for signs of phishing.`
      : ""
  }

## Deleting your account

You can delete your account at any time under [Settings](/settings). This removes your tunnels, hostnames, domains, tokens and recorded traffic. Domains, sign-in providers and TCP ports you added to a team with other members stay with the team. Audit log entries and abuse reports are kept for the periods above${s.billing.enabled ? ", and billing records for those of tax law" : ""}. A disabled account can't sign in and is kept with the reason it was disabled; to have it deleted, contact ${reachOperator}.${
    inactive
      ? ` Accounts that nobody signs in to, uses a token of or connects a client with for ${s.inactive_account_months} ${s.inactive_account_months === 1 ? "month" : "months"} are deleted; the owner gets an email 30 days before, and signing in keeps the account. This doesn't apply to administrators, disabled accounts, and accounts with a subscription or a granted Pro plan.`
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
- short-lived cookies while you sign in${providers.length ? ` (also with ${providerNames})` : ""}, confirm or set up a second factor or use a passkey, and while visitors sign in to a protected tunnel: at most 15 minutes;
- on tunnel hostnames, the 7-day cookie of a protected tunnel${s.browser_warning ? ", and a 7-day cookie that remembers a visitor saw the warning page before a tunnel" : ""};
- your choice of light or dark theme, and of the language of code examples, in your browser's local storage${supportPages.length ? `;\n- a token for your support conversation, in your browser's local storage for support.till.md${s.support_app_widget_key ? ", which signing out of the dashboard removes" : ""}` : ""}.

## Your rights

You have the right to access your data, to have it corrected or deleted, to restrict or object to its processing, and to receive it in a portable format (Art. 15 to 21 GDPR). Where processing is based on your consent, you can withdraw it at any time. You can change your name and email address and delete your account under [Settings](/settings); for anything else, contact ${reachOperator}. You also have the right to complain to a data protection supervisory authority, for example the one where you live.

## Changes

This policy changes when ${name} changes. The current version is always on this page.
`;
}

/**
 * § 5 DDG imprint from Admin → Settings → Imprint, plus the Digital Services
 * Act contact point (Art. 11 and 12 DSA) and the consumer dispute resolution
 * statement (§ 36 VSBG). No link to the EU ODR platform: it closed on
 * 20 July 2025 and the duty to link it ended with it.
 */
async function defaultImprint(): Promise<string> {
  const s = await getSettings();
  const { baseDomain } = config();
  const name = md(s.instance_name || "TUNd");
  const operator = md(s.imprint_operator.trim());
  const email = mailLink(s.imprint_email.trim());
  const [rep, phone, register, vat, responsible] = [
    s.imprint_representative,
    s.imprint_phone,
    s.imprint_register,
    s.imprint_vat_id,
    s.imprint_responsible,
  ].map((v) => v.trim());
  // A business ID (Wirtschafts-Identifikationsnummer) is the VAT ID's format plus "-00001".
  const vatLabel = /-\d{5}$/.test(vat) ? "Business identification number (§ 139c AO)" : "VAT identification number (§ 27a UStG)";
  const languages = md(s.imprint_languages.trim() || "German or English");

  return [
    "# Imprint",
    "Information under § 5 DDG.",
    `## Operator\n\n${operator}  \n${addressLines(s).map(md).join("  \n")}${rep ? `\n\nRepresented by ${md(rep)}` : ""}`,
    `## Contact\n\nEmail: ${email}${phone ? `  \nPhone: ${md(phone)}` : ""}`,
    register ? `## Register entry\n\n${md(register)}` : "",
    vat ? `## ${vatLabel}\n\n${md(vat)}` : "",
    responsible ? `## Responsible for editorial content (§ 18(2) MStV)\n\n${md(responsible)}` : "",
    `## Content of tunnels\n\nSites reached through tunnels (addresses under \`*.${baseDomain}\`, and custom domains of users) are run by the users who opened them, not by ${operator}, and those users are responsible for what they serve. To report a tunnel that breaks the law or the [Acceptable Use Policy](/acceptable-use), use the [report form](/report).`,
    `## Contact point under the Digital Services Act\n\nThe single point of contact for authorities of the EU member states, the European Commission and the European Board for Digital Services (Art. 11 DSA), and for users of ${name} (Art. 12 DSA), is ${email}. You can write to it in ${languages}.`,
    `## Consumer dispute resolution\n\n${operator} is neither obliged nor willing to take part in dispute resolution proceedings before a consumer arbitration board.`,
  ]
    .filter(Boolean)
    .join("\n\n")
    .concat("\n");
}

/** The page's markdown: the operator's text from settings, or the built-in default. */
export async function legalMarkdown(page: Page): Promise<{ markdown: string; custom: boolean }> {
  const s = await getSettings();
  const custom = {
    terms: s.terms_markdown,
    "acceptable-use": s.acceptable_use_markdown,
    privacy: s.privacy_markdown,
    imprint: s.imprint_markdown,
  }[page].trim();
  if (custom) return { markdown: custom, custom: true };
  const fallback = { terms: defaultTerms, "acceptable-use": defaultAcceptableUse, privacy: defaultPrivacy, imprint: defaultImprint }[page];
  return { markdown: await fallback(), custom: false };
}

/** Whether /imprint exists; links to it only appear then. */
export async function hasImprint(): Promise<boolean> {
  return imprintReady(await getSettings());
}

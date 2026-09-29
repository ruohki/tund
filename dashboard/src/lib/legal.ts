import "server-only";
import { Marked } from "marked";
import { config } from "./config";
import { getSettings } from "./settings";

// /terms and /acceptable-use: operator markdown from settings, or generic
// defaults naming the instance. The defaults describe how the service works
// and make no legal claims; operators replace them under Admin → Settings.

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

type Page = "terms" | "acceptable-use";

async function defaults() {
  const s = await getSettings();
  const c = config();
  return { name: s.instance_name || "tund", host: c.dashboardHost, retention: s.retention_days };
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

/** The page's markdown: the operator's text from settings, or the built-in default. */
export async function legalMarkdown(page: Page): Promise<{ markdown: string; custom: boolean }> {
  const s = await getSettings();
  const custom = (page === "terms" ? s.terms_markdown : s.acceptable_use_markdown).trim();
  if (custom) return { markdown: custom, custom: true };
  return { markdown: page === "terms" ? await defaultTerms() : await defaultAcceptableUse(), custom: false };
}

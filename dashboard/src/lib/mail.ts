import "server-only";
import nodemailer from "nodemailer";
import { decryptSecret, getSettings, type SmtpSettings } from "./settings";

// Outgoing email (docs/SPEC.md "Email flows"). Everything is optional: without
// SMTP the dashboard hides flows that depend on it.

export async function smtpConfigured(): Promise<boolean> {
  const s = await getSettings();
  return Boolean(s.smtp?.host && s.smtp.from_email);
}

/** Verification is only enforced while email can actually be sent. */
export async function verificationRequired(): Promise<boolean> {
  const s = await getSettings();
  return s.require_email_verification && (await smtpConfigured());
}

function transport(smtp: SmtpSettings) {
  const password = decryptSecret(smtp.password_enc);
  return nodemailer.createTransport({
    host: smtp.host,
    port: smtp.port,
    secure: smtp.security === "tls",
    requireTLS: smtp.security === "starttls",
    ignoreTLS: smtp.security === "none",
    auth: smtp.username ? { user: smtp.username, pass: password ?? "" } : undefined,
    connectionTimeout: 10_000,
    greetingTimeout: 10_000,
    socketTimeout: 20_000,
  });
}

export type Mail = { subject: string; html: string; text: string };

/** Sends one email; throws with the SMTP server's message on failure. */
export async function sendMail(to: string, mail: Mail, smtpOverride?: SmtpSettings): Promise<void> {
  const s = await getSettings();
  const smtp = smtpOverride ?? s.smtp;
  if (!smtp?.host || !smtp.from_email) throw new Error("Email is not configured on this server (Admin → Email).");
  if (smtp.password_enc && decryptSecret(smtp.password_enc) === null) {
    throw new Error("The stored SMTP password can't be decrypted (TUND_INTERNAL_SECRET changed?). Enter it again.");
  }
  await transport(smtp).sendMail({
    from: smtp.from_name ? { name: smtp.from_name, address: smtp.from_email } : smtp.from_email,
    to,
    subject: mail.subject,
    text: mail.text,
    html: mail.html,
  });
}

/** Best effort: logs instead of failing the surrounding action. */
export async function trySendMail(to: string, mail: Mail): Promise<boolean> {
  try {
    await sendMail(to, mail);
    return true;
  } catch (err) {
    console.error("tund: sending email failed", mail.subject, err);
    return false;
  }
}

// --- templates ---------------------------------------------------------------

const esc = (s: string) =>
  s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");

type Block = { heading: string; paragraphs: string[]; action?: { label: string; url: string }; footer?: string };

function render(instance: string, b: Block): { html: string; text: string } {
  const button = b.action
    ? `<tr><td style="padding:8px 0 24px"><a href="${esc(b.action.url)}" style="display:inline-block;background:#14171c;color:#f6f7f8;text-decoration:none;font-weight:600;font-size:14px;padding:11px 18px;border-radius:6px">${esc(b.action.label)}</a></td></tr>
<tr><td style="font-size:12px;line-height:18px;color:#737b89;padding-bottom:8px">If the button doesn't work, paste this link into your browser:<br><a href="${esc(b.action.url)}" style="color:#474e5a;word-break:break-all">${esc(b.action.url)}</a></td></tr>`
    : "";
  const html = `<!doctype html><html><body style="margin:0;padding:0;background:#edeff2;font-family:-apple-system,Segoe UI,Roboto,Helvetica,Arial,sans-serif;color:#14171c">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background:#edeff2;padding:32px 12px"><tr><td align="center">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="max-width:520px;background:#fbfcfc;border:1px solid #dde1e6;border-radius:10px">
<tr><td style="padding:22px 28px 0;font-size:15px;font-weight:700;letter-spacing:-0.02em"><span style="display:inline-block;width:10px;height:10px;border-radius:10px;background:#f0a81c;margin-right:8px"></span>${esc(instance)}</td></tr>
<tr><td style="padding:18px 28px 4px"><table role="presentation" width="100%" cellpadding="0" cellspacing="0">
<tr><td style="font-size:20px;line-height:26px;font-weight:600;padding-bottom:12px">${esc(b.heading)}</td></tr>
${b.paragraphs.map((p) => `<tr><td style="font-size:14px;line-height:22px;color:#474e5a;padding-bottom:14px">${p}</td></tr>`).join("\n")}
${button}
</table></td></tr>
<tr><td style="padding:14px 28px 22px;border-top:1px solid #dde1e6;font-size:12px;line-height:18px;color:#737b89">${esc(b.footer ?? `Sent by ${instance}. If you didn't expect this email, you can ignore it.`)}</td></tr>
</table></td></tr></table></body></html>`;
  const strip = (s: string) => s.replace(/<[^>]+>/g, "").replace(/&amp;/g, "&").replace(/&lt;/g, "<").replace(/&gt;/g, ">").replace(/&quot;/g, '"');
  const text = [
    b.heading,
    "",
    ...b.paragraphs.map(strip).flatMap((p) => [p, ""]),
    ...(b.action ? [`${b.action.label}: ${b.action.url}`, ""] : []),
    "-- ",
    b.footer ?? `Sent by ${instance}. If you didn't expect this email, you can ignore it.`,
  ].join("\n");
  return { html, text };
}

export function verifyEmail(instance: string, url: string): Mail {
  return {
    subject: `Confirm your email address for ${instance}`,
    ...render(instance, {
      heading: "Confirm your email address",
      paragraphs: [
        "Click the button to confirm this address. Until you do, your account can't log in a terminal or create auth tokens.",
        "The link works for 24 hours.",
      ],
      action: { label: "Confirm email address", url },
    }),
  };
}

export function resetPasswordEmail(instance: string, url: string): Mail {
  return {
    subject: `Reset your ${instance} password`,
    ...render(instance, {
      heading: "Reset your password",
      paragraphs: [
        "Someone asked to reset the password of your account. Choose a new one with the button below; this signs you out everywhere.",
        "The link works once, for 1 hour. If you didn't ask for this, ignore this email: your password stays the same.",
      ],
      action: { label: "Choose a new password", url },
    }),
  };
}

export function inviteEmail(instance: string, url: string, team: string, inviter: string, role: string): Mail {
  return {
    subject: `${inviter} invited you to ${team} on ${instance}`,
    ...render(instance, {
      heading: `Join ${team}`,
      paragraphs: [
        `<strong>${esc(inviter)}</strong> invited you to the team <strong>${esc(team)}</strong> as ${role === "admin" ? "an admin" : "a member"}. Members share identity providers and domains for their tunnels and see the traffic on them.`,
        "The invite works once and expires in 7 days.",
      ],
      action: { label: "Accept the invite", url },
    }),
  };
}

export function signupNoticeEmail(instance: string, email: string, url: string): Mail {
  return {
    subject: `New account on ${instance}: ${email}`,
    ...render(instance, {
      heading: "Someone created an account",
      paragraphs: [`<strong>${esc(email)}</strong> just signed up.`],
      action: { label: "Open the user", url },
      footer: `You get this because “Email admins about new sign-ups” is on in ${instance}.`,
    }),
  };
}

export function testEmail(instance: string, dashboardUrl: string): Mail {
  return {
    subject: `Test email from ${instance}`,
    ...render(instance, {
      heading: "Email works",
      paragraphs: [
        `This is a test sent from the admin area of ${esc(instance)}. Verification, password reset and invite emails will look like this.`,
      ],
      action: { label: "Open the dashboard", url: dashboardUrl },
    }),
  };
}

/** Error text when an unverified account tries something that needs a confirmed address. */
export async function unverifiedError(user: { emailVerified: boolean; email: string }): Promise<string | null> {
  if (user.emailVerified || !(await verificationRequired())) return null;
  return `Confirm your email address first: open the link we sent to ${user.email}, or send a new one from the banner above.`;
}

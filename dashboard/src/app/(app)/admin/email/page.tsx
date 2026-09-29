import type { Metadata } from "next";
import { config } from "@/lib/config";
import { inviteEmail, resetPasswordEmail, signupNoticeEmail, testEmail, verifyEmail } from "@/lib/mail";
import { decryptSecret, getSettings } from "@/lib/settings";
import { Panel } from "@/components/ui";
import { SmtpForm, TemplatePreviews, TestEmailForm } from "./email-forms";

export const metadata: Metadata = { title: "Email" };

export default async function AdminEmailPage() {
  const s = await getSettings();
  const url = config().dashboardUrl;
  const smtp = s.smtp;
  const configured = Boolean(smtp?.host && smtp.from_email);
  const name = s.instance_name;
  const templates = [
    { id: "verify", label: "Email verification", ...verifyEmail(name, `${url}/verify-email/EXAMPLE`) },
    { id: "reset", label: "Password reset", ...resetPasswordEmail(name, `${url}/reset-password/EXAMPLE`) },
    { id: "invite", label: "Team invite", ...inviteEmail(name, `${url}/invite/EXAMPLE`, "Acme platform", "Ada Lovelace", "member") },
    { id: "signup", label: "New sign-up notice", ...signupNoticeEmail(name, "new.person@example.com", `${url}/admin/users`) },
    { id: "test", label: "Test email", ...testEmail(name, url) },
  ];

  return (
    <>
      {!configured ? (
        <p className="mb-6 rounded-md border border-sodium/60 bg-sodium-wash px-4 py-3 text-[13.5px] text-ink">
          Email isn&apos;t set up. Until it is, “Forgot password” is hidden, email verification isn&apos;t enforced, and
          team invite links have to be shared by hand.
        </p>
      ) : null}
      <div className="mb-6 grid grid-cols-1 gap-6 xl:grid-cols-[minmax(0,1.3fr)_minmax(0,1fr)]">
        <Panel title="SMTP server" description="Used for verification, password reset, invite and sign-up notice emails." bodyClassName="p-4">
          <SmtpForm
            current={
              smtp
                ? {
                    host: smtp.host,
                    port: smtp.port,
                    security: smtp.security,
                    username: smtp.username,
                    hasPassword: Boolean(smtp.password_enc),
                    passwordBroken: Boolean(smtp.password_enc) && decryptSecret(smtp.password_enc) === null,
                    from_email: smtp.from_email,
                    from_name: smtp.from_name,
                  }
                : null
            }
            defaultFromName={name}
          />
        </Panel>
        <Panel title="Send a test email" description="Checks the saved settings end to end." bodyClassName="p-4">
          <TestEmailForm disabled={!configured} />
        </Panel>
      </div>
      <Panel title="Templates" description={`Branded with the instance name “${name}” (Settings → Branding).`}>
        <TemplatePreviews templates={templates} />
      </Panel>
    </>
  );
}

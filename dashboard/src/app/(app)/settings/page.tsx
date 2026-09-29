import type { Metadata } from "next";
import { getSession, requireUser } from "@/lib/auth";
import { db } from "@/lib/db";
import { formatDateTime, timeAgo } from "@/lib/format";
import { Badge, PageHeader, Panel } from "@/components/ui";
import { ConfirmSubmit } from "@/components/client-ui";
import { revokeSessionAction } from "@/app/actions/account";
import { PasswordForm, ProfileForm } from "./forms";
import { TransferUsage } from "@/components/transfer-usage";

export const metadata: Metadata = { title: "Settings" };

function describeAgent(ua: string) {
  if (!ua) return "Unknown browser";
  const browser = /Edg\//.test(ua) ? "Edge" : /Firefox\//.test(ua) ? "Firefox" : /Chrome\//.test(ua) ? "Chrome" : /Safari\//.test(ua) ? "Safari" : "Browser";
  const os = /Mac OS X/.test(ua) ? "macOS" : /Windows/.test(ua) ? "Windows" : /Android/.test(ua) ? "Android" : /iPhone|iPad/.test(ua) ? "iOS" : /Linux/.test(ua) ? "Linux" : "";
  return os ? `${browser} on ${os}` : browser;
}

export default async function SettingsPage() {
  const user = await requireUser();
  const session = await getSession();
  const sessions = await db()`
    select id, user_agent, ip, created_at, expires_at from sessions
    where user_id = ${user.id} and expires_at > now() order by created_at desc`;

  return (
    <>
      <PageHeader title="Settings" description="Your profile, password and signed-in browsers." />
      <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
        <Panel title="Profile" bodyClassName="p-4">
          <ProfileForm name={user.name} email={user.email} />
        </Panel>
        <Panel title="Password" description="Changing it signs out your other sessions." bodyClassName="p-4">
          <PasswordForm />
        </Panel>
      </div>
      <TransferUsage user={user} className="mt-6" />
      <Panel
        title="Sessions"
        description="Browsers signed in to this dashboard."
        className="mt-6"
        actions={
          sessions.length > 1 ? (
            <form action={revokeSessionAction}>
              <input type="hidden" name="id" value="others" />
              <ConfirmSubmit variant="secondary" confirmText="Sign out all others?">
                Sign out other sessions
              </ConfirmSubmit>
            </form>
          ) : null
        }
      >
        <ul className="divide-y divide-line">
          {sessions.map((s) => (
            <li key={s.id} className="flex flex-wrap items-center justify-between gap-3 px-4 py-2.5">
              <div className="min-w-0">
                <p className="flex items-center gap-2 text-[13.5px] text-ink">
                  {describeAgent(s.user_agent)}
                  {s.id === session?.id ? <Badge tone="ok">This browser</Badge> : null}
                </p>
                <p className="text-[12px] text-muted">
                  {s.ip || "unknown IP"}, signed in <span title={formatDateTime(s.created_at)}>{timeAgo(s.created_at)}</span>
                </p>
              </div>
              {s.id !== session?.id ? (
                <form action={revokeSessionAction}>
                  <input type="hidden" name="id" value={s.id} />
                  <ConfirmSubmit variant="ghost" confirmText="Sign out?">
                    Sign out
                  </ConfirmSubmit>
                </form>
              ) : null}
            </li>
          ))}
        </ul>
      </Panel>
    </>
  );
}

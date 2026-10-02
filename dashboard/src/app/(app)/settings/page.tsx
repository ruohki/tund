import type { Metadata } from "next";
import { getSession, requireUser } from "@/lib/auth";
import { db } from "@/lib/db";
import { formatDateTime, timeAgo } from "@/lib/format";
import { Badge, buttonClass, PageHeader, Panel } from "@/components/ui";
import { ConfirmSubmit } from "@/components/client-ui";
import { disconnectIdentityAction, revokeSessionAction } from "@/app/actions/account";
import { enabledProviders, identitiesOf } from "@/lib/oauth";
import { OAUTH_PROVIDERS, oauthError, PROVIDER_LABEL } from "@/lib/oauth-shared";
import { ProviderIcon } from "@/components/provider-icons";
import { PasswordForm, ProfileForm } from "./forms";
import { TransferUsage } from "@/components/transfer-usage";
import { twoFactorStatus } from "@/lib/two-factor";
import { removePasskeyAction } from "@/app/actions/two-factor";
import { AddPasskey, TwoFactorSettings } from "./security-forms";

export const metadata: Metadata = { title: "Settings" };

function describeAgent(ua: string) {
  if (!ua) return "Unknown browser";
  const browser = /Edg\//.test(ua) ? "Edge" : /Firefox\//.test(ua) ? "Firefox" : /Chrome\//.test(ua) ? "Chrome" : /Safari\//.test(ua) ? "Safari" : "Browser";
  const os = /Mac OS X/.test(ua) ? "macOS" : /Windows/.test(ua) ? "Windows" : /Android/.test(ua) ? "Android" : /iPhone|iPad/.test(ua) ? "iOS" : /Linux/.test(ua) ? "Linux" : "";
  return os ? `${browser} on ${os}` : browser;
}

export default async function SettingsPage({ searchParams }: PageProps<"/settings">) {
  const user = await requireUser();
  const session = await getSession();
  const { oauth_error: oauthErr, oauth_linked: linkedNow, passkey_error: passkeyErr } = await searchParams;
  const [sessions, providers, identities, [pw], twoFactor, passkeys] = await Promise.all([
    db()`
      select id, user_agent, ip, created_at, expires_at from sessions
      where user_id = ${user.id} and expires_at > now() order by created_at desc`,
    enabledProviders(),
    identitiesOf(user.id),
    db()`select password_hash is not null as has from users where id = ${user.id}`,
    twoFactorStatus(user.id),
    db()`select id, name, created_at, last_used_at, backed_up from user_passkeys where user_id = ${user.id} order by created_at`,
  ]);
  const hasPassword = Boolean(pw?.has);
  // Enabled providers, plus ones still connected after an admin turned them off.
  const methods = OAUTH_PROVIDERS.filter((p) => providers.includes(p) || identities.some((i) => i.provider === p));
  const methodError = oauthError(oauthErr);
  const linkedLabel = typeof linkedNow === "string" && linkedNow in PROVIDER_LABEL ? PROVIDER_LABEL[linkedNow as keyof typeof PROVIDER_LABEL] : null;

  return (
    <>
      <PageHeader title="Settings" description="Your profile, how you sign in, and signed-in browsers." />
      <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
        <Panel title="Profile" bodyClassName="p-4">
          <ProfileForm name={user.name} email={user.email} />
        </Panel>
        <Panel
          title="Password"
          description={
            hasPassword
              ? "Changing it signs out your other sessions."
              : "You sign in with a connected account. Set a password to also sign in with your email."
          }
          bodyClassName="p-4"
        >
          <PasswordForm hasPassword={hasPassword} />
        </Panel>
      </div>
      <div id="security" className="mt-6 grid scroll-mt-6 grid-cols-1 gap-6 lg:grid-cols-2">
        <Panel
          title="Two-factor authentication"
          description="A second step after your password: a code from an authenticator app, a passkey or a recovery code."
          bodyClassName="p-4"
        >
          <TwoFactorSettings
            on={twoFactor.totp}
            enabledAt={twoFactor.enabledAt ? twoFactor.enabledAt.toISOString() : null}
            recoveryLeft={twoFactor.recoveryLeft}
          />
        </Panel>
        <Panel
          title="Passkeys"
          description="Sign in with your fingerprint, face or device PIN instead of a password. They also count as the second step of two-factor."
        >
          {passkeyErr === "last_method" ? (
            <p role="alert" className="border-b border-line bg-danger-wash px-4 py-2.5 text-[13px] text-danger">
              That&apos;s your only way to sign in. Set a password or add another passkey first.
            </p>
          ) : null}
          {passkeys.length ? (
            <ul className="divide-y divide-line border-b border-line">
              {passkeys.map((k) => (
                <li key={k.id} className="flex flex-wrap items-center justify-between gap-3 px-4 py-2.5">
                  <div className="min-w-0">
                    <p className="flex items-center gap-2 text-[13.5px] text-ink">
                      {k.name || "Passkey"}
                      {k.backed_up ? <Badge tone="outline">Synced</Badge> : null}
                    </p>
                    <p className="text-[12px] text-muted">
                      Added <span title={formatDateTime(k.created_at)}>{timeAgo(k.created_at)}</span>
                      {k.last_used_at ? (
                        <>
                          , last used <span title={formatDateTime(k.last_used_at)}>{timeAgo(k.last_used_at)}</span>
                        </>
                      ) : ", never used"}
                    </p>
                  </div>
                  <form action={removePasskeyAction}>
                    <input type="hidden" name="id" value={k.id} />
                    <ConfirmSubmit variant="ghost" confirmText="Remove this passkey?">
                      Remove
                    </ConfirmSubmit>
                  </form>
                </li>
              ))}
            </ul>
          ) : null}
          <div className="p-4">
            <AddPasskey />
          </div>
        </Panel>
      </div>
      {methods.length ? (
        <Panel title="Sign-in methods" description="Accounts you can sign in with besides your password." className="mt-6">
          {methodError || linkedLabel ? (
            <p
              role={methodError ? "alert" : "status"}
              className={
                methodError
                  ? "border-b border-line bg-danger-wash px-4 py-2.5 text-[13px] text-danger"
                  : "border-b border-line bg-ok-wash px-4 py-2.5 text-[13px] text-ok"
              }
            >
              {methodError ?? `Connected your ${linkedLabel} account.`}
            </p>
          ) : null}
          <ul className="divide-y divide-line">
            {methods.map((p) => {
              const id = identities.find((i) => i.provider === p);
              return (
                <li key={p} className="flex flex-wrap items-center justify-between gap-3 px-4 py-2.5">
                  <span className="flex min-w-0 items-center gap-2.5 text-[13.5px] text-ink">
                    <ProviderIcon provider={p} />
                    {PROVIDER_LABEL[p]}
                    {id ? <span className="truncate text-[12.5px] text-muted">{id.email || "connected"}</span> : null}
                  </span>
                  {id ? (
                    <form action={disconnectIdentityAction}>
                      <input type="hidden" name="provider" value={p} />
                      <ConfirmSubmit variant="ghost" confirmText={`Disconnect ${PROVIDER_LABEL[p]}?`}>
                        Disconnect
                      </ConfirmSubmit>
                    </form>
                  ) : providers.includes(p) ? (
                    <a href={`/auth/oauth/${p}/start?intent=link`} className={buttonClass("secondary", "sm")}>
                      Connect
                    </a>
                  ) : null}
                </li>
              );
            })}
          </ul>
        </Panel>
      ) : null}
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

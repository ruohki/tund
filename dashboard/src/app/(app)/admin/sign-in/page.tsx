import type { Metadata } from "next";
import { db } from "@/lib/db";
import { callbackUrl } from "@/lib/oauth";
import { OAUTH_PROVIDERS, PROVIDER_LABEL, type OAuthProviderId } from "@/lib/oauth-shared";
import { decryptSecret, getSettings } from "@/lib/settings";
import { Panel } from "@/components/ui";
import { ProviderIcon } from "@/components/provider-icons";
import { OAuthProviderForm } from "./sign-in-forms";

export const metadata: Metadata = { title: "Sign-in" };

const SETUP: Record<OAuthProviderId, { console: string; consoleLabel: string; steps: string }> = {
  google: {
    console: "https://console.cloud.google.com/apis/credentials",
    consoleLabel: "Google Cloud console → Credentials",
    steps:
      "Create an OAuth client ID of type “Web application” and add the redirect URI below under “Authorized redirect URIs”. The consent screen only needs the email and profile scopes.",
  },
  github: {
    console: "https://github.com/settings/applications/new",
    consoleLabel: "GitHub → Developer settings → New OAuth App",
    steps:
      "Register an OAuth App (under your organization's settings if it should belong to it), use the URL below as the “Authorization callback URL”, then generate a client secret.",
  },
};

export default async function AdminSignInPage() {
  const s = await getSettings();
  const counts = await db()`select provider, count(*)::int as n from user_identities group by provider`;
  const linked = Object.fromEntries(counts.map((r) => [r.provider as string, r.n as number]));
  return (
    <>
      <p className="mb-6 max-w-3xl text-[13.5px] text-ink-2">
        Let people sign in and sign up with Google or GitHub. New accounts follow the sign-up rules under Settings (open,
        invite only or closed, disposable addresses, rate limit) and start with a verified email. An existing account is
        connected when the provider confirms the same email address.
      </p>
      <div className="grid grid-cols-1 gap-6 xl:grid-cols-2">
        {OAUTH_PROVIDERS.map((p) => {
          const c = s.oauth[p];
          const setup = SETUP[p];
          return (
            <Panel
              key={p}
              title={
                <span className="flex items-center gap-2">
                  <ProviderIcon provider={p} /> {PROVIDER_LABEL[p]}
                </span>
              }
              description={`${linked[p] ?? 0} connected ${linked[p] === 1 ? "account" : "accounts"}`}
              bodyClassName="p-4"
            >
              <p className="mb-4 text-[13px] text-ink-2">
                In{" "}
                <a href={setup.console} target="_blank" rel="noreferrer" className="font-medium text-ink underline underline-offset-4">
                  {setup.consoleLabel}
                </a>
                : {setup.steps}
              </p>
              <OAuthProviderForm
                provider={p}
                callbackUrl={callbackUrl(p)}
                current={{
                  enabled: Boolean(c?.enabled),
                  clientId: c?.client_id ?? "",
                  hasSecret: Boolean(c?.client_secret_enc),
                  secretBroken: Boolean(c?.client_secret_enc) && decryptSecret(c!.client_secret_enc) === null,
                }}
              />
            </Panel>
          );
        })}
      </div>
    </>
  );
}

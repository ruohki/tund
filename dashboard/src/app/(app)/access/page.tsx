import type { Metadata } from "next";
import Link from "next/link";
import { ShieldCheck } from "lucide-react";
import { IDENTITY_EXAMPLE, IdentityHeaderTable } from "@/components/identity-headers";
import { requireUser } from "@/lib/auth";
import { publicConfig } from "@/lib/config";
import { siteInfo } from "@/lib/seo";
import { db } from "@/lib/db";
import { EmptyState, PageHeader, Panel } from "@/components/ui";
import { Command, CopyButton } from "@/components/client-ui";
import { ProviderForm, ProviderRow, ProviderHints, type ProviderItem } from "./provider-forms";

export const metadata: Metadata = { title: "Access control" };

export default async function AccessPage() {
  const user = await requireUser();
  const cfg = publicConfig();
  const { name: brand } = await siteInfo();
  const [rows, teamRows] = await Promise.all([
    db()`
      select p.*, (select count(*) from domains d where d.auth_oidc_provider_id = p.id) as domains
      from oidc_providers p where p.user_id = ${user.id} and p.team_id is null order by p.created_at`,
    db()`
      select p.*, t.slug as team_slug, t.name as team_name, m.role,
        (select count(*) from domains d where d.auth_oidc_provider_id = p.id) as domains
      from oidc_providers p join teams t on t.id = p.team_id
        join team_members m on m.team_id = p.team_id and m.user_id = ${user.id}
      order by t.name, p.name`,
  ]);
  const teamProviders = teamRows.map((r) => ({
    provider: {
      id: r.id,
      name: r.name,
      slug: r.slug,
      issuer: r.issuer,
      clientId: r.client_id,
      hasSecret: Boolean(r.client_secret),
      scopes: r.scopes,
      domains: Number(r.domains),
    } satisfies ProviderItem,
    teamSlug: r.team_slug as string,
    teamName: r.team_name as string,
    canManage: r.role === "owner" || r.role === "admin",
  }));
  const providers: ProviderItem[] = rows.map((r) => ({
    id: r.id,
    name: r.name,
    slug: r.slug,
    issuer: r.issuer,
    clientId: r.client_id,
    hasSecret: Boolean(r.client_secret),
    scopes: r.scopes,
    domains: Number(r.domains),
  }));
  const callback = `${cfg.dashboardUrl}/_tund/oidc/callback`;
  const example = providers[0]?.slug ?? "company";

  return (
    <>
      <PageHeader
        title="Access control"
        description="Keep tunnels private: ask visitors for a password, or make them sign in with your identity provider before a request reaches your machine."
      />

      <div className="mb-6 grid grid-cols-1 gap-6 lg:grid-cols-2">
        <Panel title="Password" description="One shared secret. Good for showing work to a client.">
          <div className="flex flex-col gap-3 p-4 text-[13px] text-ink-2">
            <Command>tund http 3000 --password &apos;correct horse&apos;</Command>
            <p>
              Browsers get a sign-in page on the tunnel; scripts can send the password as HTTP basic auth with any user
              name, e.g. <code className="font-mono text-[12.5px] text-ink">curl -u x:&apos;correct horse&apos; …</code>
            </p>
          </div>
        </Panel>
        <Panel title="Single sign-on (OIDC)" description="Visitors log in with Google, Entra ID, Keycloak, Authentik…">
          <div className="flex flex-col gap-3 p-4 text-[13px] text-ink-2">
            <Command>{`tund http 3000 --oidc ${example} --oidc-allow @company.com`}</Command>
            <p>
              <code className="font-mono text-[12.5px] text-ink">--oidc-allow</code> takes emails, @domains or{" "}
              <code className="font-mono text-[12.5px] text-ink">group:&lt;name&gt;</code>; without it, anyone who can sign in
              to the provider gets through. Team providers are named{" "}
              <code className="font-mono text-[12.5px] text-ink">team/provider</code>. Static hostnames and custom domains can
              carry the same rules permanently under Domains → Access. Your app learns who signed in from{" "}
              <a href="#identity-headers" className="font-medium text-ink underline underline-offset-4">
                identity headers
              </a>
              .
            </p>
          </div>
        </Panel>
      </div>

      <Panel
        title="Identity providers"
        description={`Register ${brand} as a web application at your provider, then add it here.`}
        actions={null}
      >
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1 border-b border-line bg-surface-2 px-4 py-2.5 text-[13px]">
          <span className="text-ink-2">Redirect URI to register at the provider:</span>
          <span className="inline-flex min-w-0 max-w-full items-center gap-1 break-all font-mono text-[12.5px] text-ink">
            {callback}
            <CopyButton value={callback} />
          </span>
          <span className="text-[12px] text-muted">The same URI works for every tunnel and custom domain.</span>
        </div>
        {providers.length ? (
          <ul className="divide-y divide-line">
            {providers.map((p) => (
              <ProviderRow key={p.id} provider={p} />
            ))}
          </ul>
        ) : (
          <EmptyState icon={<ShieldCheck size={22} />} title="No identity providers yet">
            Add one below. You can use it on any number of tunnels and domains.
          </EmptyState>
        )}
        <div className="grid grid-cols-1 gap-6 border-t border-line p-4 xl:grid-cols-[minmax(0,1.3fr)_minmax(0,1fr)]">
          <div>
            <h3 className="mb-3 text-[14px] font-semibold text-ink">Add a provider</h3>
            <ProviderForm />
          </div>
          <ProviderHints callback={callback} />
        </div>
      </Panel>

      <Panel
        title="Team providers"
        description="Providers shared by your teams. Reference them as team/provider; team owners and admins manage them on the team page."
        className="mt-6"
      >
        {teamProviders.length ? (
          <ul className="divide-y divide-line">
            {teamProviders.map((t) => (
              <ProviderRow
                key={t.provider.id}
                provider={t.provider}
                teamSlug={t.teamSlug}
                canManage={false}
                teamLink={{ href: `/teams/${t.teamSlug}#providers`, label: t.canManage ? `Manage in ${t.teamName}` : t.teamName }}
              />
            ))}
          </ul>
        ) : (
          <p className="px-4 py-4 text-[13px] text-muted">
            None yet. Providers added on a{" "}
            <Link href="/teams" className="font-medium text-ink underline underline-offset-4">
              team
            </Link>{" "}
            page are shared with all its members.
          </p>
        )}
      </Panel>

      <Panel
        id="identity-headers"
        title="Who is visiting: identity headers"
        description="After a visitor passes single sign-on, tund tells your app who they are with request headers. Any X-Tund-* header the visitor sends is removed first, so your app can trust them on requests that come through tund."
        className="mt-6"
      >
        <div className="grid grid-cols-1 gap-6 p-4 xl:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]">
          <IdentityHeaderTable />
          <div className="flex flex-col gap-3 text-[13px] text-ink-2">
            <pre className="overflow-x-auto scroll-thin rounded-md border border-line bg-surface-2 p-3 font-mono text-[12px] leading-5 text-ink">
              {IDENTITY_EXAMPLE}
            </pre>
            <p>
              Claims your provider doesn&apos;t send are left out. Name and username need the{" "}
              <code className="font-mono text-[12.5px] text-ink">profile</code> scope; for groups many providers need a{" "}
              <code className="font-mono text-[12.5px] text-ink">groups</code> scope or a claim mapping. Allow lists can then
              use <code className="font-mono text-[12.5px] text-ink">group:&lt;name&gt;</code> entries.
            </p>
          </div>
        </div>
      </Panel>
    </>
  );
}

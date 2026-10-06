"use client";

import { useActionState, useRef, useState, useTransition } from "react";
import Link from "next/link";
import { CheckCircle2, CircleAlert, Loader2 } from "lucide-react";
import {
  deleteProviderAction,
  saveProviderAction,
  testDiscoveryAction,
  type DiscoveryResult,
} from "@/app/actions/oidc";
import { buttonClass, cn, Field, FormMessage, inputClass } from "@/components/ui";
import { ConfirmSubmit, SubmitButton } from "@/components/client-ui";

export type ProviderItem = {
  id: string;
  name: string;
  slug: string;
  issuer: string;
  clientId: string;
  hasSecret: boolean;
  scopes: string;
  domains: number;
  /** The team requires sign-in with this provider on its hostnames. */
  teamRequired?: boolean;
};

function slugify(s: string) {
  return s
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 40);
}

function DiscoveryLine({ result }: { result: DiscoveryResult }) {
  if (!result.ok)
    return (
      <p className="flex items-start gap-1.5 text-[12.5px] text-danger">
        <CircleAlert size={14} className="mt-0.5 shrink-0" />
        {result.error}
      </p>
    );
  return (
    <div className="text-[12.5px]">
      <p className="flex items-center gap-1.5 text-ok">
        <CheckCircle2 size={14} /> Discovery document found.
      </p>
      {!result.matches ? (
        <p className="mt-1 text-danger">
          The provider calls itself <code className="font-mono">{result.issuer}</code>. Use exactly that as the issuer.
        </p>
      ) : null}
      <p className="mt-1 truncate text-muted">Authorization endpoint: {result.authorization}</p>
    </div>
  );
}

export function ProviderForm({
  provider,
  onDone,
  teamId,
  teamSlug,
}: {
  provider?: ProviderItem;
  onDone?: () => void;
  /** Set for a team's providers; the action checks the team admin role. */
  teamId?: string;
  teamSlug?: string;
}) {
  const [slug, setSlug] = useState(provider?.slug ?? "");
  const [slugTouched, setSlugTouched] = useState(Boolean(provider));
  const [issuer, setIssuer] = useState(provider?.issuer ?? "");
  const [discovery, setDiscovery] = useState<DiscoveryResult | null>(null);
  const formRef = useRef<HTMLFormElement>(null);
  const [state, action] = useActionState(async (s: Parameters<typeof saveProviderAction>[0], fd: FormData) => {
    const res = await saveProviderAction(s, fd);
    if (res?.ok && !provider) {
      formRef.current?.reset();
      setSlug("");
      setSlugTouched(false);
      setIssuer("");
      setDiscovery(null);
    }
    if (res?.ok && onDone) onDone();
    return res;
  }, null);
  const [testing, startTest] = useTransition();
  const idp = provider?.id ?? "new";

  return (
    <form ref={formRef} action={action} className="flex flex-col gap-3.5">
      {provider ? <input type="hidden" name="id" value={provider.id} /> : null}
      {teamId ? <input type="hidden" name="team_id" value={teamId} /> : null}
      <div className="grid gap-3.5 sm:grid-cols-2">
        <Field label="Name" htmlFor={`name-${idp}`}>
          <input
            id={`name-${idp}`}
            name="name"
            required
            defaultValue={provider?.name}
            placeholder="Company Google"
            className={inputClass}
            onChange={(e) => {
              if (!slugTouched) setSlug(slugify(e.target.value));
            }}
          />
        </Field>
        <Field label="Slug" htmlFor={`slug-${idp}`} hint={<>Used on the command line: <code className="font-mono">--oidc {teamSlug ? `${teamSlug}/` : ""}{slug || "<slug>"}</code></>}>
          <input
            id={`slug-${idp}`}
            name="slug"
            required
            value={slug}
            onChange={(e) => {
              setSlugTouched(true);
              setSlug(e.target.value.toLowerCase());
            }}
            placeholder="company"
            spellCheck={false}
            className={cn(inputClass, "font-mono text-[12.5px]")}
          />
        </Field>
      </div>
      <Field label="Issuer URL" htmlFor={`issuer-${idp}`}>
        <div className="flex gap-2">
          <input
            id={`issuer-${idp}`}
            name="issuer"
            required
            value={issuer}
            onChange={(e) => {
              setIssuer(e.target.value);
              setDiscovery(null);
            }}
            placeholder="https://accounts.google.com"
            spellCheck={false}
            className={cn(inputClass, "font-mono text-[12.5px]")}
          />
          <button
            type="button"
            disabled={!issuer || testing}
            onClick={() => startTest(async () => setDiscovery(await testDiscoveryAction(issuer)))}
            className={buttonClass("secondary", "md", "shrink-0")}
          >
            {testing ? <Loader2 size={14} className="animate-spin" /> : null}
            Test discovery
          </button>
        </div>
      </Field>
      {discovery ? <DiscoveryLine result={discovery} /> : null}
      <div className="grid gap-3.5 sm:grid-cols-2">
        <Field label="Client ID" htmlFor={`cid-${idp}`}>
          <input
            id={`cid-${idp}`}
            name="client_id"
            required
            defaultValue={provider?.clientId}
            spellCheck={false}
            autoComplete="off"
            className={cn(inputClass, "font-mono text-[12.5px]")}
          />
        </Field>
        <Field
          label="Client secret"
          htmlFor={`secret-${idp}`}
          hint={provider?.hasSecret ? "Leave empty to keep the stored secret." : "Leave empty for public clients using PKCE only."}
        >
          <input
            id={`secret-${idp}`}
            name="client_secret"
            type="password"
            autoComplete="new-password"
            placeholder={provider?.hasSecret ? "••••••••" : ""}
            className={cn(inputClass, "font-mono text-[12.5px]")}
          />
        </Field>
      </div>
      <Field label="Scopes" htmlFor={`scopes-${idp}`} hint="Space separated. email is needed for allow lists.">
        <input
          id={`scopes-${idp}`}
          name="scopes"
          defaultValue={provider?.scopes ?? "openid email profile"}
          spellCheck={false}
          className={cn(inputClass, "font-mono text-[12.5px]")}
        />
      </Field>
      <FormMessage state={state} />
      <div className="flex items-center gap-2">
        <SubmitButton pendingText="Saving…">{provider ? "Save changes" : "Add provider"}</SubmitButton>
        {onDone ? (
          <button type="button" onClick={onDone} className={buttonClass("ghost")}>
            Cancel
          </button>
        ) : null}
      </div>
    </form>
  );
}

export function ProviderRow({
  provider,
  teamId,
  teamSlug,
  canManage = true,
  teamLink,
}: {
  provider: ProviderItem;
  teamId?: string;
  teamSlug?: string;
  canManage?: boolean;
  /** Read-only listing elsewhere (e.g. /access): link to the team page instead of editing inline. */
  teamLink?: { href: string; label: string };
}) {
  const [editing, setEditing] = useState(false);
  const ref = teamSlug ? `${teamSlug}/${provider.slug}` : provider.slug;
  return (
    <li className="px-4 py-3">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="min-w-0">
          <p className="flex items-center gap-2 text-[14px] font-medium text-ink">
            {provider.name}
            <span className="rounded-[4px] bg-surface-3 px-1.5 font-mono text-[12px] font-normal text-ink-2">
              --oidc {ref}
            </span>
          </p>
          <p className="mt-0.5 truncate font-mono text-[12px] text-muted">{provider.issuer}</p>
        </div>
        <div className="flex items-center gap-3">
          <span className="text-[12px] text-muted">
            {provider.teamRequired
              ? "Required on all team hostnames"
              : provider.domains
                ? `Protects ${provider.domains} ${provider.domains === 1 ? "domain" : "domains"}`
                : "Not used by a domain"}
          </span>
          {teamLink ? (
            <Link href={teamLink.href} className={buttonClass("ghost", "sm")}>
              {teamLink.label}
            </Link>
          ) : null}
          {canManage ? (
            <>
              <button type="button" onClick={() => setEditing((e) => !e)} className={buttonClass("ghost", "sm")}>
                {editing ? "Close" : "Edit"}
              </button>
              {provider.teamRequired ? (
                <span title="The team requires sign-in with this provider. Turn that off or choose another provider under Single sign-on first.">
                  <button type="button" disabled className={buttonClass("ghost", "sm")}>
                    Remove
                  </button>
                </span>
              ) : (
                <form action={deleteProviderAction}>
                  <input type="hidden" name="id" value={provider.id} />
                  <ConfirmSubmit variant="ghost" confirmText={provider.domains ? "Remove and unprotect?" : "Remove?"}>
                    Remove
                  </ConfirmSubmit>
                </form>
              )}
            </>
          ) : null}
        </div>
      </div>
      {editing && canManage ? (
        <div className="mt-3 rounded-md border border-line p-4">
          <ProviderForm provider={provider} onDone={() => setEditing(false)} teamId={teamId} teamSlug={teamSlug} />
        </div>
      ) : null}
    </li>
  );
}

const HINTS: { id: string; name: string; issuer: string; steps: string }[] = [
  {
    id: "google",
    name: "Google",
    issuer: "https://accounts.google.com",
    steps: "Google Cloud console → APIs & Services → Credentials → Create OAuth client ID → Web application. Add the redirect URI.",
  },
  {
    id: "entra",
    name: "Microsoft Entra ID",
    issuer: "https://login.microsoftonline.com/<tenant-id>/v2.0",
    steps: "App registrations → New registration → Web platform with the redirect URI. Create a client secret under Certificates & secrets.",
  },
  {
    id: "keycloak",
    name: "Keycloak",
    issuer: "https://keycloak.example.com/realms/<realm>",
    steps: "Clients → Create client (OpenID Connect) → turn on Client authentication → set Valid redirect URIs.",
  },
  {
    id: "authentik",
    name: "Authentik",
    issuer: "https://authentik.example.com/application/o/<app-slug>/",
    steps: "Applications → Providers → OAuth2/OpenID Provider (confidential) → add the redirect URI, then create an application for it.",
  },
  {
    id: "github",
    name: "GitHub",
    issuer: "https://dex.example.com",
    steps: "GitHub logins aren't OpenID Connect. Put Dex (or Authentik) in front with its GitHub connector and register Dex here.",
  },
];

export function ProviderHints({ callback }: { callback: string }) {
  const [active, setActive] = useState(HINTS[0].id);
  const hint = HINTS.find((h) => h.id === active)!;
  return (
    <div className="rounded-md border border-line bg-surface-2 p-4">
      <h3 className="text-[14px] font-semibold text-ink">Setup notes</h3>
      <div className="mt-2 flex flex-wrap gap-1">
        {HINTS.map((h) => (
          <button
            key={h.id}
            type="button"
            onClick={() => setActive(h.id)}
            aria-pressed={active === h.id}
            className={cn(
              "rounded-[4px] px-2 py-0.5 text-[12.5px] transition-colors",
              active === h.id ? "bg-surface-3 font-medium text-ink" : "text-muted hover:text-ink",
            )}
          >
            {h.name}
          </button>
        ))}
      </div>
      <dl className="mt-3 flex flex-col gap-2 text-[13px]">
        <div>
          <dt className="text-[12px] text-muted">Issuer</dt>
          <dd className="break-all font-mono text-[12.5px] text-ink">{hint.issuer}</dd>
        </div>
        <div>
          <dt className="text-[12px] text-muted">Where</dt>
          <dd className="text-ink-2">{hint.steps}</dd>
        </div>
        <div>
          <dt className="text-[12px] text-muted">Redirect URI</dt>
          <dd className="break-all font-mono text-[12.5px] text-ink">{callback}</dd>
        </div>
      </dl>
    </div>
  );
}

"use client";

import { useActionState, useState, useTransition } from "react";
import Link from "next/link";
import { CheckCircle2, ChevronDown, CircleAlert, CircleDashed, Dices, Loader2, ShieldCheck, Star } from "lucide-react";
import {
  addCustomDomainAction,
  addSubdomainAction,
  claimRandomAction,
  deleteDomainAction,
  makeDefaultAction,
  updatePolicyAction,
  verifyDomainAction,
  type VerifyResult,
} from "@/app/actions/domains";
import type { PublicConfig } from "@/lib/config";
import { ALLOW_LIST_HINT } from "@/lib/validate";
import { AuthBadge, Badge, buttonClass, cn, Field, FormMessage, inputClass, Select } from "@/components/ui";
import { Command, ConfirmSubmit, CopyButton, SubmitButton, submitKeepingValues } from "@/components/client-ui";

export type DomainItem = {
  id: string;
  hostname: string;
  kind: "subdomain" | "custom";
  verified: boolean;
  token: string;
  authMode: string;
  hasPassword: boolean;
  providerId: string;
  providerName: string;
  allow: string[];
  online: boolean;
  isDefault: boolean;
  /** Set for team-owned domains. */
  teamId: string | null;
  createdAt: string;
  /** Custom domains of non-trusted accounts may need an admin's approval. */
  approval: "approved" | "pending" | "rejected";
  reviewReason: string;
  /** Why the server withdrew the verification (the TXT record went away or changed); "" if it didn't. */
  lostReason: string;
  /** "3h ago": when it was withdrawn, and when the record was last confirmed (formatted on the server). */
  lostAgo: string;
  checkedAgo: string;
};

/** The sign-in a team requires on all its hostnames. */
export type TeamSsoNote = { providerName: string };

/** A provider the domain may use; `ref` is how the CLI names it (slug, or team/slug). */
export type Provider = { id: string; name: string; slug: string; ref: string };

function TeamField({ teamId }: { teamId?: string | null }) {
  return teamId ? <input type="hidden" name="team_id" value={teamId} /> : null;
}

function LimitNote({ children }: { children: React.ReactNode }) {
  return <p className="rounded-md border border-line bg-surface-2 px-3 py-2 text-[13px] text-ink-2">{children}</p>;
}

/** "Claim random" and "Choose a name" for static hostnames. */
export function StaticHostnameForms({
  baseDomain,
  full,
  teamId,
}: {
  baseDomain: string;
  full: string | null;
  teamId?: string | null;
}) {
  const [named, nameAction] = useActionState(addSubdomainAction, null);
  const [random, randomAction] = useActionState(claimRandomAction, null);
  if (full) return <LimitNote>{full}</LimitNote>;
  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap items-center gap-2">
        <form action={nameAction} className="flex min-w-0 flex-1 flex-wrap items-center gap-2 sm:flex-none">
          <TeamField teamId={teamId} />
          <div className="flex min-w-0 flex-1 items-center rounded-[5px] border border-line-strong bg-surface focus-within:border-focus focus-within:ring-2 focus-within:ring-focus/20 sm:w-[26rem] sm:flex-none">
            <input
              name="label"
              required
              aria-label="Name"
              placeholder="my-app"
              autoComplete="off"
              spellCheck={false}
              className="h-8.5 min-w-0 flex-1 bg-transparent px-2.5 font-mono text-[13px] text-ink outline-none placeholder:text-muted/80"
            />
            <span className="truncate border-l border-line bg-surface-2 px-2.5 py-2 font-mono text-[12.5px] text-muted">
              .{baseDomain}
            </span>
          </div>
          <SubmitButton pendingText="Pinning…">Pin this name</SubmitButton>
        </form>
        <span className="text-[12.5px] text-muted">or</span>
        <form action={randomAction}>
          <TeamField teamId={teamId} />
          <SubmitButton variant="secondary" pendingText="Claiming…">
            <Dices size={14} /> Claim a random name
          </SubmitButton>
        </form>
      </div>
      <FormMessage state={named ?? random} />
    </div>
  );
}

export function AddCustomDomainForm({ full, teamId }: { full: string | null; teamId?: string | null }) {
  const [state, action] = useActionState(addCustomDomainAction, null);
  if (full) return <LimitNote>{full}</LimitNote>;
  return (
    <form action={action} className="flex flex-col gap-2">
      <TeamField teamId={teamId} />
      <div className="flex flex-wrap items-center gap-2">
        <input
          name="hostname"
          required
          aria-label="Domain"
          placeholder="api.example.com or *.dev.example.com"
          autoComplete="off"
          spellCheck={false}
          className={cn(inputClass, "min-w-0 flex-1 font-mono text-[13px] sm:max-w-md")}
        />
        <SubmitButton pendingText="Adding…">Add domain</SubmitButton>
      </div>
      <FormMessage state={state} />
    </form>
  );
}

function RoutingLine({ result }: { result: VerifyResult["routing"] }) {
  if (result.state === "ok" && result.via === "cname")
    return (
      <p className="flex items-center gap-1.5 text-[12.5px] text-ok">
        <CheckCircle2 size={14} /> Points here through a CNAME, so it reaches every server of this instance.
      </p>
    );
  if (result.state === "ok" && result.missing.length)
    return (
      <p className="flex items-center gap-1.5 text-[12.5px] text-ink-2">
        <CircleAlert size={14} /> Points at {result.addresses.join(", ")}, but not at {result.missing.join(", ")}. Tunnels work, but
        only those servers serve this domain; use the CNAME, or add the missing addresses.
      </p>
    );
  if (result.state === "ok")
    return (
      <p className="flex items-center gap-1.5 text-[12.5px] text-ok">
        <CheckCircle2 size={14} /> Points at this server ({result.addresses.join(", ")}).
      </p>
    );
  if (result.state === "elsewhere")
    return (
      <p className="flex items-center gap-1.5 text-[12.5px] text-danger">
        <CircleAlert size={14} />
        {result.foreign.length === result.addresses.length
          ? ` Resolves to ${result.addresses.join(", ")}, not this server. Tunnels won't be reachable yet.`
          : ` Also resolves to ${result.foreign.join(", ")}, which isn't this server. Visitors sent there won't reach your tunnels.`}
      </p>
    );
  if (result.state === "missing")
    return (
      <p className="flex items-center gap-1.5 text-[12.5px] text-muted">
        <CircleDashed size={14} /> No A/AAAA or CNAME record found yet.
      </p>
    );
  return <p className="text-[12.5px] text-muted">Routing check skipped: {result.reason}</p>;
}

function DnsRecords({ domain, cfg }: { domain: DomainItem; cfg: PublicConfig }) {
  const bare = domain.hostname.replace(/^\*\./, "");
  const recordName = domain.hostname; // "*.dev.example.com" for wildcards
  const rows: { type: string; name: string; value: string; note: string }[] = [
    {
      type: "TXT",
      name: `_tund-challenge.${bare}`,
      value: `tund-verify=${domain.token}`,
      note: "Proves the domain is yours. Keep it: the server re-checks it, and the domain stops working if it goes away.",
    },
  ];
  const ips = cfg.serverIps ?? (cfg.serverIp ? [cfg.serverIp] : []);
  rows.push({
    type: "CNAME",
    name: recordName,
    value: cfg.dashboardHost,
    note: ips.length > 1 ? "Recommended: reaches every server of this instance." : "Recommended: routes traffic to this server.",
  });
  for (const ip of ips) {
    rows.push({
      type: ip.includes(":") ? "AAAA" : "A",
      name: recordName,
      value: ip,
      note: ips.length > 1 ? "Instead of the CNAME where none is allowed (zone apex): add all of these." : "Instead of the CNAME where none is allowed (zone apex).",
    });
  }
  return (
    <div className="overflow-x-auto scroll-thin rounded-md border border-line">
      <table className="w-full min-w-[640px] text-[12.5px]">
        <thead className="bg-surface-2 text-left text-[12px] text-muted">
          <tr>
            <th className="px-3 py-1.5 font-medium">Type</th>
            <th className="px-3 py-1.5 font-medium">Name</th>
            <th className="px-3 py-1.5 font-medium">Value</th>
            <th className="px-3 py-1.5 font-medium">Purpose</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-line">
          {rows.map((r) => (
            <tr key={`${r.type}-${r.value}`}>
              <td className="px-3 py-1.5 font-mono font-medium text-ink">{r.type}</td>
              <td className="px-3 py-1.5">
                <span className="inline-flex items-center gap-1 font-mono text-ink">
                  {r.name}
                  <CopyButton value={r.name} />
                </span>
              </td>
              <td className="px-3 py-1.5">
                <span className="inline-flex items-center gap-1 font-mono text-ink">
                  {r.value}
                  <CopyButton value={r.value} />
                </span>
              </td>
              <td className="px-3 py-1.5 text-muted">{r.note}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function PolicyEditor({
  domain,
  providers,
  passthrough,
  teamSso,
}: {
  domain: DomainItem;
  providers: Provider[];
  passthrough: boolean;
  teamSso?: TeamSsoNote | null;
}) {
  const [state, action, pending] = useActionState(updatePolicyAction, null);
  const [mode, setMode] = useState(domain.authMode);
  const modes = [
    { id: "none", label: "Public", hint: "Anyone with the address." },
    { id: "password", label: "Password", hint: "Visitors enter a shared password." },
    { id: "oidc", label: "Single sign-on", hint: "Visitors log in with your identity provider." },
  ];
  return (
    <form onSubmit={submitKeepingValues(action)} className="flex flex-col gap-4">
      <input type="hidden" name="id" value={domain.id} />
      {teamSso ? (
        <p className="rounded-md border border-line bg-surface-2 px-3 py-2 text-[13px] text-ink-2">
          The team requires sign-in with {teamSso.providerName || "its identity provider"} on all its hostnames, so Public
          and Password don&apos;t apply here. Choose Single sign-on to use another provider or allow list for this{" "}
          {domain.kind === "custom" ? "domain" : "hostname"}.
        </p>
      ) : null}
      <fieldset>
        <legend className="mb-2 text-[13px] font-medium text-ink">Who can open this domain</legend>
        <div className="grid gap-2 sm:grid-cols-3">
          {modes.map((m) => (
            <label
              key={m.id}
              className={cn(
                "flex cursor-pointer flex-col rounded-md border px-3 py-2 transition-colors",
                mode === m.id ? "border-ink bg-surface-2" : "border-line hover:border-line-strong",
              )}
            >
              <span className="flex items-center gap-2 text-[13px] font-medium text-ink">
                <input
                  type="radio"
                  name="mode"
                  value={m.id}
                  checked={mode === m.id}
                  onChange={() => setMode(m.id)}
                  className="accent-[var(--ink)]"
                />
                {m.label}
              </span>
              <span className="mt-0.5 pl-5 text-[12px] text-muted">{m.hint}</span>
            </label>
          ))}
        </div>
      </fieldset>

      {mode === "password" ? (
        <Field
          label={domain.hasPassword ? "New password" : "Password"}
          htmlFor={`pw-${domain.id}`}
          hint={
            domain.hasPassword
              ? "Leave empty to keep the current password. Scripts can send it as HTTP basic auth with any user name."
              : "Scripts can send it as HTTP basic auth with any user name."
          }
        >
          <input
            id={`pw-${domain.id}`}
            name="password"
            type="password"
            autoComplete="new-password"
            minLength={6}
            required={!domain.hasPassword}
            className={cn(inputClass, "max-w-sm")}
          />
        </Field>
      ) : null}

      {mode === "oidc" ? (
        providers.length ? (
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="Identity provider" htmlFor={`prov-${domain.id}`}>
              <Select
                id={`prov-${domain.id}`}
                name="provider"
                defaultValue={domain.providerId}
                required
                placeholder="Choose…"
                options={providers.map((p) => ({ value: p.id, label: `${p.name} (${p.ref})` }))}
              />
            </Field>
            <Field
              label="Allowed people"
              htmlFor={`allow-${domain.id}`}
              hint={ALLOW_LIST_HINT}
            >
              <input
                id={`allow-${domain.id}`}
                name="allow"
                defaultValue={domain.allow.join(", ")}
                placeholder="@company.com, alex@example.org, group:engineering"
                className={cn(inputClass, "font-mono text-[12.5px]")}
              />
            </Field>
          </div>
        ) : (
          <p className="rounded-md border border-line bg-surface-2 px-3 py-2 text-[13px] text-ink-2">
            Add an identity provider under{" "}
            <Link href="/access" className="font-medium text-ink underline underline-offset-4">
              Access control
            </Link>{" "}
            first.
          </p>
        )
      ) : null}

      <div className="flex flex-wrap items-center gap-3">
        <SubmitButton size="sm" pendingText="Saving…" pending={pending} disabled={mode === "oidc" && providers.length === 0}>
          Save access
        </SubmitButton>
        <p className="text-[12px] text-muted">
          {teamSso ? (
            <>
              Applies to HTTP tunnels on this {domain.kind === "custom" ? "domain" : "hostname"}; members&apos;{" "}
              <code className="font-mono">--password</code> and <code className="font-mono">--oidc</code> flags are
              ignored while the team requires sign-in.
            </>
          ) : (
            <>
              Applies to HTTP tunnels on this {domain.kind === "custom" ? "domain" : "hostname"} unless the client passes
              its own <code className="font-mono">--password</code> or <code className="font-mono">--oidc</code>.
            </>
          )}
          {passthrough ? (
            <>
              {" "}
              TCP and TLS tunnels can limit visitors by IP address with <code className="font-mono">--allow-ip</code> on the
              command line.
            </>
          ) : null}
        </p>
      </div>
      <FormMessage state={state} />
    </form>
  );
}

export function DomainRow({
  domain,
  providers,
  cfg,
  canManage = true,
  teamSso,
}: {
  domain: DomainItem;
  providers: Provider[];
  cfg: PublicConfig;
  /** False for plain team members: they can use the domain but not change it. */
  canManage?: boolean;
  /** Set when the owning team requires sign-in on its hostnames. */
  teamSso?: TeamSsoNote | null;
}) {
  // A team's required sign-in beats the domain's own Public/Password setting.
  const enforced = teamSso && domain.authMode !== "oidc" ? teamSso : null;
  const [open, setOpen] = useState<"dns" | "access" | null>(
    canManage && domain.kind === "custom" && !domain.verified ? "dns" : null,
  );
  const [verify, setVerify] = useState<VerifyResult | null>(null);
  const [verifying, startVerify] = useTransition();
  const label = domain.hostname.slice(0, -(cfg.baseDomain.length + 1));
  const usage =
    domain.kind === "subdomain"
      ? domain.isDefault
        ? "tund http 3000"
        : `tund http 3000 --subdomain ${label}`
      : `tund http 3000 --domain ${domain.hostname.startsWith("*.") ? `app.${domain.hostname.slice(2)}` : domain.hostname}`;

  return (
    <li className="px-4 py-3">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex min-w-0 flex-wrap items-center gap-2.5">
          {domain.online ? <span className="live-dot shrink-0" title="A tunnel is online on this name" /> : null}
          <a
            href={domain.hostname.startsWith("*.") ? undefined : `${cfg.scheme}://${domain.hostname}${cfg.portSuffix}`}
            target="_blank"
            rel="noreferrer"
            className="truncate font-mono text-[13.5px] font-medium text-ink hover:underline"
          >
            {domain.hostname}
          </a>
          {domain.isDefault ? (
            <Badge tone="outline" title="tund http <port> uses this hostname unless you pass --subdomain or --random">
              <Star size={11} className="fill-current" /> Default
            </Badge>
          ) : null}
          {domain.kind === "custom" ? (
            domain.verified ? (
              <Badge
                tone="ok"
                title={`The server keeps checking the TXT record; last confirmed ${domain.checkedAgo || "at verification"}.`}
              >
                <ShieldCheck size={12} /> Verified
              </Badge>
            ) : domain.lostReason ? (
              <Badge tone="danger">Verification lost</Badge>
            ) : (
              <Badge tone="live">Waiting for DNS</Badge>
            )
          ) : null}
          {domain.approval === "pending" ? (
            <Badge tone="live" title="An administrator reviews new custom domains before the server serves them">
              Pending review
            </Badge>
          ) : domain.approval === "rejected" ? (
            <Badge tone="danger">Rejected</Badge>
          ) : null}
          <AuthBadge mode={enforced ? "oidc" : domain.authMode} />
          {enforced ? (
            <span className="text-[12px] text-muted">
              {enforced.providerName ? `via ${enforced.providerName}, ` : ""}required by the team
            </span>
          ) : domain.authMode === "oidc" && domain.providerName ? (
            <span className="text-[12px] text-muted">via {domain.providerName}</span>
          ) : null}
        </div>
        {canManage ? (
        <div className="flex items-center gap-1">
          {domain.kind === "subdomain" && !domain.isDefault && !domain.teamId ? (
            <form action={makeDefaultAction}>
              <input type="hidden" name="id" value={domain.id} />
              <SubmitButton variant="ghost" size="sm" pendingText="Saving…">
                Make default
              </SubmitButton>
            </form>
          ) : null}
          {domain.kind === "custom" ? (
            <button
              type="button"
              onClick={() => setOpen(open === "dns" ? null : "dns")}
              aria-expanded={open === "dns"}
              className={buttonClass("ghost", "sm")}
            >
              DNS <ChevronDown size={13} className={cn("transition-transform", open === "dns" && "rotate-180")} />
            </button>
          ) : null}
          <button
            type="button"
            onClick={() => setOpen(open === "access" ? null : "access")}
            aria-expanded={open === "access"}
            className={buttonClass("ghost", "sm")}
          >
            Access <ChevronDown size={13} className={cn("transition-transform", open === "access" && "rotate-180")} />
          </button>
          <form action={deleteDomainAction}>
            <input type="hidden" name="id" value={domain.id} />
            <ConfirmSubmit variant="ghost" confirmText={domain.kind === "subdomain" ? "Release this name?" : "Remove?"}>
              Remove
            </ConfirmSubmit>
          </form>
        </div>
        ) : null}
      </div>

      {open === "dns" ? (
        <div className="mt-3 flex flex-col gap-3 rounded-md border border-line bg-surface p-3">
          <p className="text-[13px] text-ink-2">
            Create these records at your DNS provider.{" "}
            {domain.hostname.startsWith("*.")
              ? "The wildcard record routes every name under the domain to this server."
              : "Use the CNAME record; only where your DNS doesn't allow one (on the zone apex), use the address records instead."}
          </p>
          <DnsRecords domain={domain} cfg={cfg} />
          <div className="flex flex-wrap items-center gap-3">
            <button
              type="button"
              disabled={verifying}
              onClick={() => startVerify(async () => setVerify(await verifyDomainAction(domain.id)))}
              className={buttonClass(domain.verified ? "secondary" : "primary", "sm")}
            >
              {verifying ? <Loader2 size={13} className="animate-spin" /> : null}
              {domain.verified ? "Check DNS again" : "Verify"}
            </button>
            {verify ? (
              <p className={cn("text-[12.5px]", verify.verified ? "text-ok" : "text-ink-2")}>{verify.message}</p>
            ) : null}
          </div>
          {verify ? <RoutingLine result={verify.routing} /> : null}
        </div>
      ) : null}

      {open === "access" ? (
        <div className="mt-3 rounded-md border border-line p-3">
          <PolicyEditor domain={domain} providers={providers} passthrough={Boolean(cfg.passthrough)} teamSso={teamSso} />
        </div>
      ) : null}

      {domain.approval === "pending" ? (
        <p className="mt-2 text-[12.5px] text-ink-2">
          Waiting for an administrator to approve this domain. Tunnels can use it once it&apos;s approved; you can set up
          DNS in the meantime.
        </p>
      ) : domain.approval === "rejected" ? (
        <p className="mt-2 text-[12.5px] text-danger">
          An administrator rejected this domain{domain.reviewReason ? `: ${domain.reviewReason}` : "."} Tunnels can&apos;t use it.
        </p>
      ) : null}
      {domain.kind === "custom" && !domain.verified && domain.lostReason ? (
        <p className="mt-2 text-[12.5px] text-danger">
          Verification withdrawn {domain.lostAgo}: {domain.lostReason}. Tunnels can&apos;t use the domain until the record is
          back and you verify it again.
        </p>
      ) : null}
      {domain.verified ? <Command className="mt-2.5 max-w-xl">{usage}</Command> : null}
    </li>
  );
}

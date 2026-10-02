// The identity headers tund-server adds for visitors who passed an access policy
// (docs/SPEC.md "OIDC identity headers").
export const IDENTITY_HEADERS: [string, string][] = [
  ["X-Tund-Auth", "oidc or password: how the visitor got in."],
  ["X-Tund-User-Email", "The email claim."],
  ["X-Tund-User-Email-Verified", "true or false, when the provider sends email_verified."],
  ["X-Tund-User-Name", "The name claim."],
  ["X-Tund-User-Username", "preferred_username (or nickname)."],
  ["X-Tund-User-Id", "The sub claim, a stable id at the provider."],
  ["X-Tund-User-Groups", "The groups claim, comma-separated."],
  ["X-Tund-Idp", "The issuer URL of the provider."],
];

export function IdentityHeaderTable() {
  return (
    <dl className="divide-y divide-line rounded-md border border-line">
      {IDENTITY_HEADERS.map(([name, desc]) => (
        <div key={name} className="grid gap-0.5 px-3 py-2 sm:grid-cols-[14rem_minmax(0,1fr)] sm:gap-3">
          <dt className="font-mono text-[12.5px] text-ink">{name}</dt>
          <dd className="text-[13px] text-ink-2">{desc}</dd>
        </div>
      ))}
    </dl>
  );
}

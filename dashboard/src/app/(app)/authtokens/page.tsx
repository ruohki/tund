import type { Metadata } from "next";
import Link from "next/link";
import { KeyRound } from "lucide-react";
import { requireUser } from "@/lib/auth";
import { publicConfig } from "@/lib/config";
import { db } from "@/lib/db";
import { formatDateTime, timeAgo } from "@/lib/format";
import { EmptyState, PageHeader, Panel } from "@/components/ui";
import { ConfirmSubmit } from "@/components/client-ui";
import { revokeTokenAction } from "@/app/actions/tokens";
import { CreateTokenForm } from "./create-token";

export const metadata: Metadata = { title: "Auth tokens" };

export default async function TokensPage() {
  const user = await requireUser();
  const cfg = publicConfig();
  const tokens = await db()`
    select t.id, t.name, t.token_prefix, t.created_at, t.last_used_at,
      (select count(*) from agent_sessions a where a.authtoken_id = t.id and a.disconnected_at is null) as connected
    from authtokens t where t.user_id = ${user.id} order by t.created_at desc`;

  return (
    <>
      <PageHeader
        title="Auth tokens"
        description="Each machine running the tund client signs in with a token. tund login creates one through the browser; create one here for CI or machines without a browser. Revoking a token disconnects its tunnels immediately."
      />
      <p className="-mt-3 mb-6 max-w-[70ch] text-[13px] text-ink-2">
        Tokens also authenticate the{" "}
        <Link href="/get-started#api" className="font-medium text-ink underline underline-offset-4">
          HTTP API
        </Link>{" "}
        at <code className="font-mono text-[12.5px] text-ink">{cfg.dashboardUrl}/_tund/api/v1</code>: send one as{" "}
        <code className="font-mono text-[12.5px] text-ink">Authorization: Bearer &lt;token&gt;</code>.
      </p>
      <Panel title="Create a token" className="mb-6" bodyClassName="p-4">
        <CreateTokenForm dashboardUrl={cfg.dashboardUrl} />
      </Panel>
      <Panel title="Your tokens">
        {tokens.length ? (
          <div className="overflow-x-auto scroll-thin">
            <table className="w-full min-w-[640px] text-[13px]">
              <thead>
                <tr className="border-b border-line text-left text-[12px] text-muted">
                  <th className="px-4 py-2 font-medium">Name</th>
                  <th className="px-4 py-2 font-medium">Token</th>
                  <th className="px-4 py-2 font-medium">Created</th>
                  <th className="px-4 py-2 font-medium">Last used</th>
                  <th className="px-4 py-2" />
                </tr>
              </thead>
              <tbody className="divide-y divide-line">
                {tokens.map((t) => (
                  <tr key={t.id}>
                    <td className="px-4 py-2.5 font-medium text-ink">
                      <span className="flex items-center gap-2">
                        {Number(t.connected) > 0 ? <span className="live-dot" title="A client is connected with this token" /> : null}
                        {t.name}
                      </span>
                    </td>
                    <td className="px-4 py-2.5 font-mono text-[12.5px] text-ink-2">{t.token_prefix}…</td>
                    <td className="px-4 py-2.5 text-ink-2" title={formatDateTime(t.created_at)}>
                      {timeAgo(t.created_at)}
                    </td>
                    <td className="px-4 py-2.5 text-ink-2" title={t.last_used_at ? formatDateTime(t.last_used_at) : undefined}>
                      {Number(t.connected) > 0 ? "Connected now" : timeAgo(t.last_used_at)}
                    </td>
                    <td className="px-4 py-2.5 text-right">
                      <form action={revokeTokenAction}>
                        <input type="hidden" name="id" value={t.id} />
                        <ConfirmSubmit confirmText="Revoke and disconnect?">Revoke</ConfirmSubmit>
                      </form>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : (
          <EmptyState icon={<KeyRound size={22} />} title="No tokens yet">
            Create one above for each machine you want to run tunnels from.
          </EmptyState>
        )}
      </Panel>
    </>
  );
}

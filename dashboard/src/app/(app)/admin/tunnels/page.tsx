import type { Metadata } from "next";
import Link from "next/link";
import { Cable } from "lucide-react";
import { db } from "@/lib/db";
import { formatDateTime, formatNumber, osLabel, sessionLength } from "@/lib/format";
import { AuthBadge, EmptyState, Panel, ProtoBadge } from "@/components/ui";
import { PAGE_SIZE, Pager, pageOffset, pageParam } from "@/components/pager";
import { StopTunnelForm } from "../admin-forms";

export const metadata: Metadata = { title: "All tunnels" };

export default async function AdminTunnelsPage({ searchParams }: PageProps<"/admin/tunnels">) {
  const page = pageParam((await searchParams).page);
  const [[{ total }], rows] = await Promise.all([
    db()`select count(*)::int as total from tunnels where ended_at is null`,
    db()`
    select t.id, t.name, t.hostname, t.public_url, t.local_addr, t.auth_mode, t.started_at, t.proto,
      u.id as user_id, u.email, a.hostname as client_host, a.client_os, a.client_version, a.remote_addr,
      case when t.proto = 'http' then (select count(*)::int from requests r where r.tunnel_id = t.id)
           else (select count(*)::int from connections c where c.tunnel_id = t.id) end as requests
    from tunnels t join users u on u.id = t.user_id join agent_sessions a on a.id = t.agent_session_id
    where t.ended_at is null
    order by t.started_at desc
    limit ${PAGE_SIZE} offset ${pageOffset(page)}`,
  ]);
  return (
    <Panel
      title="Online tunnels"
      description={`${formatNumber(total)} across all accounts. Stopping one closes it on the client, which sees your reason.`}
    >
      {rows.length ? (
        <div className="overflow-x-auto scroll-thin">
          <table className="w-full min-w-[980px] text-[13px]">
            <thead>
              <tr className="border-b border-line text-left text-[12px] text-muted">
                <th className="px-4 py-2 font-medium">Hostname</th>
                <th className="px-4 py-2 font-medium">Owner</th>
                <th className="px-4 py-2 font-medium">Client</th>
                <th className="px-4 py-2 font-medium">Access</th>
                <th className="px-4 py-2 font-medium">Up</th>
                <th className="px-4 py-2 text-right font-medium" title="Requests for HTTP, connections for TCP/TLS">
                  Traffic
                </th>
                <th className="px-4 py-2" />
              </tr>
            </thead>
            <tbody className="divide-y divide-line">
              {rows.map((t) => (
                <tr key={t.id} className="align-top">
                  <td className="px-4 py-2.5">
                    {t.proto === "http" ? (
                      <a href={t.public_url} target="_blank" rel="noreferrer" className="font-mono text-[12.5px] text-ink hover:underline">
                        {t.hostname}
                      </a>
                    ) : (
                      <span className="font-mono text-[12.5px] text-ink">{t.public_url}</span>
                    )}
                    <span className="block font-mono text-[12px] text-muted">{t.local_addr}</span>
                  </td>
                  <td className="px-4 py-2.5">
                    <Link href={`/admin/users/${t.user_id}`} className="text-ink hover:underline">
                      {t.email}
                    </Link>
                  </td>
                  <td className="px-4 py-2.5 text-ink-2">
                    {t.client_host || "unknown"}
                    <span className="block text-[12px] text-muted">
                      {osLabel(t.client_os)} {t.client_version ? `v${t.client_version}` : ""}, {t.remote_addr}
                    </span>
                  </td>
                  <td className="px-4 py-2.5">
                    <span className="flex gap-1.5">
                      <ProtoBadge proto={t.proto} />
                      {t.proto === "http" ? <AuthBadge mode={t.auth_mode} /> : null}
                    </span>
                  </td>
                  <td className="px-4 py-2.5 text-ink-2" title={formatDateTime(t.started_at)}>
                    {sessionLength(t.started_at, null)}
                  </td>
                  <td className="px-4 py-2.5 text-right tabular text-ink">{formatNumber(t.requests)}</td>
                  <td className="px-4 py-2">
                    <StopTunnelForm id={t.id} />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <EmptyState icon={<Cable size={22} />} title="No tunnel is online" />
      )}
      <Pager path="/admin/tunnels" page={page} total={total as number} />
    </Panel>
  );
}

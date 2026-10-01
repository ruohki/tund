import type { Metadata } from "next";
import { Server } from "lucide-react";
import { db } from "@/lib/db";
import { formatBitRate, formatBytes, formatDateTime, formatNumber, sessionLength, timeAgo } from "@/lib/format";
import { Badge, EmptyState, Panel, cn } from "@/components/ui";
import { IntervalRefresh } from "@/components/interval-refresh";

export const metadata: Metadata = { title: "Nodes" };

type NodeRow = {
  name: string;
  role: string;
  region: string;
  version: string;
  public_ip: string;
  capacity_mbps: number;
  cpus: number;
  cpu_pct: number | null;
  load1: number | null;
  mem_total: number | null;
  mem_used: number | null;
  net_in_rate: number | null;
  net_out_rate: number | null;
  tunnel_rate: number | null;
  sessions: number;
  tunnels: number;
  started_at: Date;
  last_seen: Date;
  alive: boolean;
  fresh: boolean;
};

/** Share of a limit as a thin bar under the value; amber from 70 %, red from 90 %. */
function Meter({ ratio }: { ratio: number | null }) {
  if (ratio == null || !Number.isFinite(ratio)) return null;
  const pct = Math.min(100, Math.max(0, ratio * 100));
  return (
    <span className="mt-1 block h-1 w-24 overflow-hidden rounded-full bg-surface-3" aria-hidden>
      <span
        className={cn("block h-full rounded-full", pct >= 90 ? "bg-danger" : pct >= 70 ? "bg-sodium" : "bg-ok")}
        style={{ width: `${pct}%` }}
      />
    </span>
  );
}

export default async function AdminNodesPage() {
  const rows = (await db()`
    select name, role, region, version, public_ip, capacity_mbps, cpus, cpu_pct, load1, mem_total, mem_used,
      net_in_rate, net_out_rate, tunnel_rate, sessions, tunnels, started_at, last_seen,
      last_seen > now() - interval '45 seconds' as alive,
      coalesce(metrics_at > now() - interval '30 seconds', false) as fresh
    from nodes
    order by role = 'control' desc, name`) as unknown as NodeRow[];
  const online = rows.filter((n) => n.alive).length;

  return (
    <Panel
      title="Nodes"
      description={
        rows.length
          ? `${online} of ${rows.length} online. Each node reports its host metrics every 10 seconds.`
          : "Servers of this instance and how busy they are."
      }
    >
      <IntervalRefresh ms={10_000} />
      {rows.length ? (
        <div className="overflow-x-auto scroll-thin">
          <table className="w-full min-w-[1040px] text-[13px]">
            <thead>
              <tr className="border-b border-line text-left text-[12px] text-muted">
                <th className="px-4 py-2 font-medium">Node</th>
                <th className="px-4 py-2 font-medium">Status</th>
                <th className="px-4 py-2 font-medium">CPU</th>
                <th className="px-4 py-2 font-medium">Memory</th>
                <th className="px-4 py-2 font-medium" title="Traffic on the public interface">
                  Network in / out
                </th>
                <th className="px-4 py-2 text-right font-medium" title="Traffic through tunnels, both directions">
                  Tunnel traffic
                </th>
                <th className="px-4 py-2 text-right font-medium" title="Connected clients / online tunnels">
                  Clients / tunnels
                </th>
              </tr>
            </thead>
            <tbody className="divide-y divide-line">
              {rows.map((n) => {
                const live = n.alive && n.fresh;
                const capacity = n.capacity_mbps > 0 ? (n.capacity_mbps * 1e6) / 8 : 0;
                const netPeak = Math.max(n.net_in_rate ?? 0, n.net_out_rate ?? 0);
                return (
                  <tr key={n.name} className="align-top">
                    <td className="px-4 py-2.5">
                      <span className="flex items-center gap-1.5">
                        <span className="font-mono text-[12.5px] text-ink">{n.name}</span>
                        <Badge tone="outline">{n.role}</Badge>
                      </span>
                      <span className="block text-[12px] text-muted">
                        {[n.region, n.public_ip, n.version ? `v${n.version}` : ""].filter(Boolean).join(" · ")}
                      </span>
                    </td>
                    <td className="px-4 py-2.5">
                      {n.alive ? (
                        <Badge tone="ok" title={`Started ${formatDateTime(n.started_at)}`}>
                          Online
                        </Badge>
                      ) : (
                        <Badge tone="danger">Offline</Badge>
                      )}
                      <span className="block text-[12px] text-muted" title={formatDateTime(n.last_seen)}>
                        {n.alive ? `up ${sessionLength(n.started_at, null)}` : `seen ${timeAgo(n.last_seen)}`}
                      </span>
                    </td>
                    <td className="px-4 py-2.5 tabular text-ink">
                      {live && n.cpu_pct != null ? `${Math.round(n.cpu_pct)}%` : "—"}
                      <span className="block text-[12px] text-muted">
                        {n.cpus ? `${n.cpus} cores` : ""}
                        {live && n.load1 != null ? `, load ${n.load1.toFixed(2)}` : ""}
                      </span>
                      <Meter ratio={live && n.cpu_pct != null ? n.cpu_pct / 100 : null} />
                    </td>
                    <td className="px-4 py-2.5 tabular text-ink">
                      {live && n.mem_used != null && n.mem_total ? (
                        <>
                          {formatBytes(n.mem_used)}
                          <span className="block text-[12px] text-muted">of {formatBytes(n.mem_total)}</span>
                          <Meter ratio={n.mem_used / n.mem_total} />
                        </>
                      ) : (
                        "—"
                      )}
                    </td>
                    <td className="px-4 py-2.5 tabular text-ink">
                      {live ? (
                        <>
                          {formatBitRate(n.net_in_rate)} / {formatBitRate(n.net_out_rate)}
                          <span className="block text-[12px] text-muted">
                            {capacity ? `uplink ${formatNumber(n.capacity_mbps)} Mbit/s` : "uplink not set"}
                          </span>
                          <Meter ratio={capacity ? netPeak / capacity : null} />
                        </>
                      ) : (
                        "—"
                      )}
                    </td>
                    <td className="px-4 py-2.5 text-right tabular text-ink">{live ? formatBitRate(n.tunnel_rate) : "—"}</td>
                    <td className="px-4 py-2.5 text-right tabular text-ink">
                      {n.alive ? `${formatNumber(n.sessions)} / ${formatNumber(n.tunnels)}` : "—"}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      ) : (
        <EmptyState icon={<Server size={22} />} title="No node is reporting">
          Nodes appear here once they run as a cluster: set <code className="font-mono">TUND_RELAY_URL</code> on each
          server (see the edge node setup in the README).
        </EmptyState>
      )}
    </Panel>
  );
}

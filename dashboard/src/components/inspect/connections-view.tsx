"use client";

import { useEffect, useMemo, useState } from "react";
import { Cable, Trash2 } from "lucide-react";
import type { ConnectionSummary } from "@/lib/connections";
import { formatBytes, formatClock, formatDateTime, formatDuration } from "@/lib/format";
import { useDisplayTimeZone } from "@/lib/use-hydrated";
import { buttonClass, cn, Select } from "../ui";
import { useLiveEvents } from "../live";
import { clearConnectionsAction } from "@/app/actions/requests";


/** TCP/TLS connections: the inspector view for raw tunnels (docs/SPEC.md "Connection records"). */
export function ConnectionsView({
  hostnames,
  addresses,
  initialAddress,
  onSwitch,
}: {
  hostnames: string[];
  addresses: string[];
  initialAddress: string;
  onSwitch: (host: string) => void;
}) {
  const tz = useDisplayTimeZone();
  const [address, setAddress] = useState(initialAddress);
  const [items, setItems] = useState<ConnectionSummary[]>([]);
  const [hasMore, setHasMore] = useState(false);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [fresh, setFresh] = useState<Set<string>>(new Set());
  const [confirmClear, setConfirmClear] = useState(false);
  const known = useMemo(() => {
    const s = new Set(addresses);
    for (const i of items) s.add(i.address);
    if (address) s.add(address);
    return [...s];
  }, [addresses, items, address]);

  useEffect(() => {
    const qs = new URLSearchParams({ view: "connections", ...(address ? { host: address } : {}) });
    const next = `/inspect?${qs}`;
    if (next !== window.location.pathname + window.location.search) window.history.replaceState(null, "", next);
  }, [address]);

  useEffect(() => {
    const ctrl = new AbortController();
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setLoading(true);
    fetch(`/api/connections?${new URLSearchParams({ limit: "100", ...(address ? { host: address } : {}) })}`, { signal: ctrl.signal })
      .then(async (res) => {
        if (!res.ok) throw new Error((await res.json().catch(() => ({}))).error ?? `HTTP ${res.status}`);
        return res.json() as Promise<{ items: ConnectionSummary[]; hasMore: boolean }>;
      })
      .then((d) => {
        setItems(d.items);
        setHasMore(d.hasMore);
        setError(null);
      })
      .catch((err) => {
        if (err.name !== "AbortError") setError(err.message);
      })
      .finally(() => setLoading(false));
    return () => ctrl.abort();
  }, [address]);

  useLiveEvents((e) => {
    if (e.type !== "connection" || (address && e.data.address !== address)) return;
    const c = e.data;
    setItems((list) => (list.some((x) => x.id === c.id) ? list : [c, ...list].slice(0, 1000)));
    setFresh((s) => new Set(s).add(c.id));
    setTimeout(() => setFresh((s) => {
      const n = new Set(s);
      n.delete(c.id);
      return n;
    }), 1600);
  });

  const loadMore = async () => {
    const last = items[items.length - 1];
    if (!last) return;
    const qs = new URLSearchParams({ limit: "100", before: last.startedAt, beforeId: last.id, ...(address ? { host: address } : {}) });
    const res = await fetch(`/api/connections?${qs}`);
    if (!res.ok) return;
    const d = (await res.json()) as { items: ConnectionSummary[]; hasMore: boolean };
    setItems((list) => [...list, ...d.items.filter((x) => !list.some((y) => y.id === x.id))]);
    setHasMore(d.hasMore);
  };

  const clear = async () => {
    if (!confirmClear) {
      setConfirmClear(true);
      setTimeout(() => setConfirmClear(false), 3500);
      return;
    }
    setConfirmClear(false);
    await clearConnectionsAction(address);
    setItems((list) => list.filter((c) => c.teamSlug && c.address !== address));
  };

  return (
    <div className="flex h-[calc(100dvh-5.5rem)] min-h-[560px] flex-col overflow-hidden rounded-lg border border-line bg-surface max-lg:h-[calc(100dvh-8.5rem)]">
      <div className="flex flex-wrap items-center gap-2 border-b border-line px-3 py-2">
        <Select
          aria-label="Tunnel"
          value={`c:${address}`}
          onValueChange={(v) => {
            if (v.startsWith("c:")) setAddress(v.slice(2));
            else onSwitch(v);
          }}
          mono
          className="h-7.5 w-auto min-w-48 max-w-80 text-[12.5px]"
          options={[
            {
              label: "TCP / TLS connections",
              options: [{ value: "c:", label: "All connections" }, ...known.map((a) => ({ value: `c:${a}`, label: a }))],
            },
            { label: "HTTP", options: [{ value: "", label: "All HTTP requests" }, ...hostnames.map((h) => ({ value: h, label: h }))] },
          ]}
        />
        <span className="text-[12.5px] text-muted">One row per finished TCP or TLS connection.</span>
        <button
          type="button"
          onClick={clear}
          className={buttonClass(confirmClear ? "danger" : "ghost", "sm", "ml-auto")}
          title="Delete connections of your own tunnels (team traffic stays with its owners)"
        >
          <Trash2 size={13} />
          {confirmClear ? "Delete these?" : "Clear"}
        </button>
      </div>
      <div className={cn("min-h-0 flex-1 overflow-auto scroll-thin", loading && items.length > 0 && "opacity-60")}>
        {error ? (
          <p className="m-3 rounded-[5px] border border-danger/30 bg-danger-wash px-3 py-2 text-[13px] text-danger">
            Couldn&apos;t load connections: {error}
          </p>
        ) : null}
        {!loading && !items.length && !error ? (
          <div className="flex flex-col items-center px-6 py-16 text-center">
            <Cable size={22} className="mb-3 text-muted" />
            <p className="text-[15px] font-semibold text-ink">No connections yet</p>
            <p className="mt-1 max-w-[46ch] text-[13px] text-ink-2">
              Connect to a TCP or TLS tunnel and each finished connection shows up here with its bytes and duration.
            </p>
          </div>
        ) : null}
        {items.length ? (
          <table className="w-full min-w-[760px] text-[13px]">
            <thead className="sticky top-0 bg-surface">
              <tr className="border-b border-line text-left text-[12px] text-muted">
                <th className="px-3 py-2 font-medium">Time</th>
                <th className="px-3 py-2 font-medium">Address</th>
                <th className="px-3 py-2 font-medium">From</th>
                <th className="px-3 py-2 text-right font-medium">In</th>
                <th className="px-3 py-2 text-right font-medium">Out</th>
                <th className="px-3 py-2 text-right font-medium">Duration</th>
                <th className="px-3 py-2 font-medium">Result</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-line">
              {items.map((c) => (
                <tr key={c.id} className={cn(fresh.has(c.id) && "flash-new")}>
                  <td className="whitespace-nowrap px-3 py-2 text-ink-2 tabular" title={formatDateTime(c.startedAt, tz)}>
                    {formatClock(c.startedAt, tz)}
                  </td>
                  <td className="px-3 py-2">
                    <span className="font-mono text-[12.5px] text-ink">{c.address}</span>
                    <span className="ml-2 rounded-[3px] border border-line-strong px-1 text-[10.5px] uppercase text-ink-2">
                      {c.proto}
                    </span>
                    {c.teamSlug ? (
                      <span className="ml-1.5 rounded-[3px] border border-line-strong px-1 text-[10.5px] text-ink-2">
                        team {c.teamSlug}
                      </span>
                    ) : null}
                  </td>
                  <td className="px-3 py-2 font-mono text-[12px] text-ink-2">{c.remoteAddr || "unknown"}</td>
                  <td className="px-3 py-2 text-right tabular text-ink" title="Visitor to your service">
                    {formatBytes(c.bytesIn)}
                  </td>
                  <td className="px-3 py-2 text-right tabular text-ink" title="Your service to the visitor">
                    {formatBytes(c.bytesOut)}
                  </td>
                  <td className="px-3 py-2 text-right tabular text-ink-2">{formatDuration(c.durationMs)}</td>
                  <td className="px-3 py-2">
                    {c.error ? (
                      <span className="text-[12.5px] text-danger" title={c.error}>
                        {c.error.length > 60 ? `${c.error.slice(0, 60)}…` : c.error}
                      </span>
                    ) : (
                      <span className="text-[12.5px] text-muted">closed</span>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        ) : null}
        {hasMore ? (
          <div className="p-3 text-center">
            <button type="button" onClick={loadMore} className={buttonClass("secondary", "sm")}>
              Load older connections
            </button>
          </div>
        ) : null}
      </div>
    </div>
  );
}

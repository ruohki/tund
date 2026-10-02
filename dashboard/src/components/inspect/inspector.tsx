"use client";

import { useCallback, useEffect, useMemo, useRef, useState, useTransition } from "react";
import { Activity, Download, Pause, Play, Search, Trash2 } from "lucide-react";
import type { RequestDetail as Detail, RequestSummary } from "@/lib/requests";
import { formatBytes, formatClock, formatDuration, statusClass } from "@/lib/format";
import { buttonClass, cn, inputClass, Select, StatusCode } from "../ui";
import { Command } from "../client-ui";
import { useLiveEvents } from "../live";
import { useDisplayTimeZone } from "@/lib/use-hydrated";
import { RequestDetail } from "./request-detail";
import { CompareView } from "./compare-view";
import { clearRequestsAction, replayRequestAction } from "@/app/actions/requests";
import { useInfiniteScroll } from "@/components/use-infinite-scroll";

/** body: "1" searches q in request and response bodies too. */
type Filters = { host: string; method: string; status: string; q: string; body: string };

const METHODS = ["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"];
const STATUSES = [
  { id: "", label: "All" },
  { id: "2xx", label: "2xx" },
  { id: "3xx", label: "3xx" },
  { id: "4xx", label: "4xx" },
  { id: "5xx", label: "5xx" },
];

/** Whether a live request belongs in the filtered list (body searches can't tell; see below). */
function matches(r: RequestSummary, f: Filters) {
  if (f.host && r.hostname !== f.host) return false;
  if (f.method && r.method !== f.method) return false;
  if (f.status && statusClass(r.status) !== f.status) return false;
  if (f.q && !r.path.toLowerCase().includes(f.q.toLowerCase())) return false;
  return true;
}

// Filter controls size to their content (inputClass is full width by default).

function query(f: Filters, extra: Record<string, string> = {}) {
  const p = new URLSearchParams();
  for (const [k, v] of Object.entries({ ...f, ...extra })) if (v) p.set(k, v);
  return p.toString();
}

export function Inspector({
  hostnames,
  initialHost,
  initialId,
  initialCompare = "",
  addresses = [],
  onSwitch,
}: {
  hostnames: string[];
  initialHost: string;
  initialId: string;
  initialCompare?: string;
  /** TCP/TLS addresses; picking one switches to the connections view. */
  addresses?: string[];
  onSwitch?: (host: string) => void;
}) {
  const [filters, setFilters] = useState<Filters>({ host: initialHost, method: "", status: "", q: "", body: "" });
  const [reloads, setReloads] = useState(0);
  const reloadTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  /** Picking the second request of a comparison: the first one's id. */
  const [picking, setPicking] = useState<string | null>(null);
  const [compare, setCompare] = useState<{ a: string; b: string } | null>(
    initialId && initialCompare ? { a: initialId, b: initialCompare } : null,
  );
  const tz = useDisplayTimeZone();
  const [search, setSearch] = useState("");
  const [items, setItems] = useState<RequestSummary[]>([]);
  const [hasMore, setHasMore] = useState(false);
  const [loading, setLoading] = useState(true);
  const [listError, setListError] = useState<string | null>(null);
  const [selectedId, setSelectedId] = useState<string>(initialId);
  const [detail, setDetail] = useState<Detail | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);
  const [detailError, setDetailError] = useState<string | null>(null);
  const [paused, setPaused] = useState(false);
  const [buffered, setBuffered] = useState<RequestSummary[]>([]);
  const [fresh, setFresh] = useState<Set<string>>(new Set());
  const [replaying, startReplay] = useTransition();
  const [replayError, setReplayError] = useState<string | null>(null);
  const [confirmClear, setConfirmClear] = useState(false);
  const listRef = useRef<HTMLDivElement>(null);
  const knownHosts = useMemo(() => {
    const s = new Set(hostnames);
    for (const i of items) s.add(i.hostname);
    if (filters.host) s.add(filters.host);
    return [...s];
  }, [hostnames, items, filters.host]);

  // Debounce the path search.
  useEffect(() => {
    const t = setTimeout(() => setFilters((f) => (f.q === search ? f : { ...f, q: search })), 250);
    return () => clearTimeout(t);
  }, [search]);

  // Keep the URL shareable.
  useEffect(() => {
    const qs = new URLSearchParams();
    if (filters.host) qs.set("host", filters.host);
    if (compare) {
      qs.set("id", compare.a);
      qs.set("compare", compare.b);
    } else if (selectedId) qs.set("id", selectedId);
    const next = `/inspect${qs.size ? `?${qs}` : ""}`;
    if (next !== window.location.pathname + window.location.search) window.history.replaceState(null, "", next);
  }, [filters.host, selectedId, compare]);

  // Escape stops picking a request to compare with.
  useEffect(() => {
    if (!picking) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setPicking(null);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [picking]);
  useEffect(() => () => {
    if (reloadTimer.current) clearTimeout(reloadTimer.current);
  }, []);

  // Load the list whenever filters change.
  useEffect(() => {
    const ctrl = new AbortController();
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setLoading(true);
    fetch(`/api/requests?${query(filters, { limit: "100" })}`, { signal: ctrl.signal })
      .then(async (res) => {
        if (!res.ok) throw new Error((await res.json().catch(() => ({}))).error ?? `HTTP ${res.status}`);
        return res.json() as Promise<{ items: RequestSummary[]; hasMore: boolean }>;
      })
      .then((data) => {
        setItems(data.items);
        setHasMore(data.hasMore);
        setBuffered([]);
        setListError(null);
      })
      .catch((err) => {
        if (err.name !== "AbortError") setListError(err.message);
      })
      .finally(() => setLoading(false));
    return () => ctrl.abort();
  }, [filters, reloads]);

  // Load the selected request's details.
  useEffect(() => {
    if (!selectedId) {
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setDetail(null);
      return;
    }
    const ctrl = new AbortController();
    setDetailLoading(true);
    setDetailError(null);
    setReplayError(null);
    fetch(`/api/requests/${selectedId}`, { signal: ctrl.signal })
      .then(async (res) => {
        if (!res.ok) throw new Error((await res.json().catch(() => ({}))).error ?? `HTTP ${res.status}`);
        return res.json() as Promise<Detail>;
      })
      .then(setDetail)
      .catch((err) => {
        if (err.name !== "AbortError") {
          setDetail(null);
          setDetailError(err.message);
        }
      })
      .finally(() => setDetailLoading(false));
    return () => ctrl.abort();
  }, [selectedId]);

  const markFresh = useCallback((id: string) => {
    setFresh((s) => new Set(s).add(id));
    setTimeout(
      () =>
        setFresh((s) => {
          const n = new Set(s);
          n.delete(id);
          return n;
        }),
      1600,
    );
  }, []);

  const bodySearch = Boolean(filters.q && filters.body);
  useLiveEvents((e) => {
    if (e.type !== "request") return;
    if (bodySearch) {
      // Bodies aren't in live events: ask the server again, at most once a second.
      if (!paused && !reloadTimer.current) {
        reloadTimer.current = setTimeout(() => {
          reloadTimer.current = null;
          setReloads((n) => n + 1);
        }, 1000);
      }
      return;
    }
    if (!matches(e.data, filters)) return;
    if (paused) {
      setBuffered((b) => (b.some((x) => x.id === e.data.id) ? b : [e.data, ...b]));
      return;
    }
    setItems((list) => (list.some((x) => x.id === e.data.id) ? list : [e.data, ...list].slice(0, 1000)));
    markFresh(e.data.id);
  });

  const resume = () => {
    setPaused(false);
    if (buffered.length) {
      setItems((list) => {
        const ids = new Set(list.map((x) => x.id));
        return [...buffered.filter((b) => !ids.has(b.id)), ...list].slice(0, 1000);
      });
      setBuffered([]);
    }
  };

  const loadMore = async () => {
    const last = items[items.length - 1];
    if (!last) return;
    const res = await fetch(`/api/requests?${query(filters, { limit: "100", before: last.startedAt, beforeId: last.id })}`);
    if (!res.ok) return;
    const data = (await res.json()) as { items: RequestSummary[]; hasMore: boolean };
    setItems((list) => [...list, ...data.items.filter((d) => !list.some((x) => x.id === d.id))]);
    setHasMore(data.hasMore);
  };
  // Older requests load as the list scrolls; the button stays as a fallback.
  const moreRef = useInfiniteScroll(hasMore, items.length, loadMore);

  const replayed = (res: { summary: RequestSummary | null; requestId: string }) => {
    if (res.summary) {
      const s = res.summary;
      setItems((list) => (list.some((x) => x.id === s.id) ? list : [s, ...list]));
      markFresh(s.id);
    }
    if (res.requestId) setSelectedId(res.requestId);
  };

  const replay = () => {
    if (!detail) return;
    setReplayError(null);
    startReplay(async () => {
      const res = await replayRequestAction(detail.id);
      if (!res.ok) {
        setReplayError(res.error);
        return;
      }
      replayed(res);
    });
  };

  const select = (id: string) => {
    if (picking && id !== picking) {
      setCompare({ a: picking, b: id });
      setPicking(null);
    } else if (!picking) {
      setCompare(null);
    }
    setSelectedId(id);
  };
  const pickingSummary = picking ? (items.find((i) => i.id === picking) ?? (detail?.id === picking ? detail : null)) : null;

  const clear = async () => {
    if (!confirmClear) {
      setConfirmClear(true);
      setTimeout(() => setConfirmClear(false), 3500);
      return;
    }
    setConfirmClear(false);
    await clearRequestsAction({ ...filters, body: filters.body === "1" });
    setItems([]);
    setHasMore(false);
    setSelectedId("");
  };

  // j/k or arrow keys move through the list.
  const onListKey = (e: React.KeyboardEvent) => {
    if (!["ArrowDown", "ArrowUp", "j", "k"].includes(e.key)) return;
    e.preventDefault();
    const idx = items.findIndex((i) => i.id === selectedId);
    const next = e.key === "ArrowDown" || e.key === "j" ? Math.min(items.length - 1, idx + 1) : Math.max(0, idx - 1);
    const item = items[next];
    if (item && !picking) {
      setCompare(null);
      setSelectedId(item.id);
      listRef.current?.querySelector(`[data-id="${item.id}"]`)?.scrollIntoView({ block: "nearest" });
    }
  };

  const filtered = filters.host || filters.method || filters.status || filters.q;
  const showDetail = Boolean(selectedId || compare);

  return (
    <div className="flex h-[calc(100dvh-5.5rem)] min-h-[560px] flex-col overflow-hidden rounded-lg border border-line bg-surface max-lg:h-[calc(100dvh-8.5rem)]">
      {/* Filter row: scopes the list below it. */}
      <div className="flex flex-wrap items-center gap-2 border-b border-line px-3 py-2">
        <Select
          aria-label="Hostname"
          value={filters.host}
          onValueChange={(v) => {
            if (v.startsWith("c:") && onSwitch) onSwitch(v.slice(2));
            else setFilters((f) => ({ ...f, host: v }));
          }}
          mono
          className="h-7.5 w-auto min-w-48 max-w-72 text-[12.5px]"
          options={[
            { value: "", label: "All HTTP requests" },
            { label: "HTTP", options: knownHosts.map((h) => ({ value: h, label: h })) },
            ...(onSwitch
              ? [
                  {
                    label: "TCP / TLS connections",
                    options: [{ value: "c:", label: "All connections" }, ...addresses.map((a) => ({ value: `c:${a}`, label: a }))],
                  },
                ]
              : []),
          ]}
        />
        <Select
          aria-label="Method"
          value={filters.method}
          onValueChange={(v) => setFilters((f) => ({ ...f, method: v }))}
          mono
          className="h-7.5 w-auto min-w-32 text-[12.5px]"
          options={[{ value: "", label: "Any method" }, ...METHODS.map((m) => ({ value: m, label: m }))]}
        />
        <div role="radiogroup" aria-label="Status" className="inline-flex h-7.5 items-center rounded-[5px] border border-line-strong p-0.5">
          {STATUSES.map((s) => (
            <button
              key={s.id}
              type="button"
              role="radio"
              aria-checked={filters.status === s.id}
              onClick={() => setFilters((f) => ({ ...f, status: s.id }))}
              className={cn(
                "h-full rounded-[3px] px-2 font-mono text-[12px] transition-colors",
                filters.status === s.id ? "bg-surface-3 text-ink" : "text-muted hover:text-ink",
              )}
            >
              {s.label}
            </button>
          ))}
        </div>
        <div className="flex min-w-48 flex-1 items-center">
          <label className="relative flex-1">
            <span className="sr-only">{filters.body ? "Search paths and bodies" : "Filter by path"}</span>
            <Search size={14} className="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 text-muted" />
            <input
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder={filters.body ? "Search paths and bodies" : "Filter by path"}
              className={cn(inputClass, "h-7.5 rounded-r-none pl-8 font-mono text-[12.5px]")}
            />
          </label>
          <button
            type="button"
            aria-pressed={filters.body === "1"}
            onClick={() => setFilters((f) => ({ ...f, body: f.body ? "" : "1" }))}
            title="Also search request and response bodies (uncompressed ones; JSON, forms, text)"
            className={cn(
              "h-7.5 rounded-r-[5px] border border-l-0 border-line-strong px-2 text-[12px] transition-colors",
              filters.body ? "bg-surface-3 text-ink" : "text-muted hover:text-ink",
            )}
          >
            Bodies
          </button>
        </div>
        <div className="flex items-center gap-1">
          <a
            href={`/api/requests/har?${query(filters)}`}
            download
            className={buttonClass("ghost", "sm")}
            title="Download the newest 200 matching requests as a HAR file (HTTP Archive) for browser dev tools, Postman or k6"
          >
            <Download size={13} />
            HAR
          </a>
          <button
            type="button"
            onClick={() => (paused ? resume() : setPaused(true))}
            className={buttonClass("ghost", "sm")}
            title={paused ? "Resume live updates" : "Pause live updates"}
          >
            {paused ? <Play size={13} /> : <Pause size={13} />}
            {paused ? `Resume${buffered.length ? ` (${buffered.length} new)` : ""}` : "Pause"}
          </button>
          <button
            type="button"
            onClick={clear}
            className={buttonClass(confirmClear ? "danger" : "ghost", "sm")}
            title={
              filtered
                ? "Delete the requests from your own tunnels that match these filters"
                : "Delete all requests captured by your own tunnels (team traffic stays with its tunnel owners)"
            }
          >
            <Trash2 size={13} />
            {confirmClear ? (filtered ? "Delete matching?" : "Delete all?") : "Clear"}
          </button>
        </div>
      </div>

      <div className="flex min-h-0 flex-1">
        {/* List */}
        <div
          ref={listRef}
          tabIndex={0}
          onKeyDown={onListKey}
          aria-label="Captured requests"
          data-scroll-root
          className={cn(
            "min-h-0 overflow-y-auto scroll-thin outline-none lg:w-[44%] lg:min-w-[380px] lg:border-r lg:border-line",
            showDetail && !picking ? "hidden w-full lg:block" : "w-full",
          )}
        >
          {picking ? (
            <div className="sticky top-0 z-10 flex items-center justify-between gap-2 border-b border-line bg-sodium-wash px-3 py-2 text-[12.5px] text-ink">
              <span className="min-w-0 truncate">
                Pick a request to compare with{" "}
                <span className="font-mono">{pickingSummary ? `${pickingSummary.method} ${pickingSummary.path}` : "the selected one"}</span>
              </span>
              <button type="button" onClick={() => setPicking(null)} className={buttonClass("ghost", "sm")}>
                Cancel
              </button>
            </div>
          ) : null}
          {listError ? (
            <p className="m-3 rounded-[5px] border border-danger/30 bg-danger-wash px-3 py-2 text-[13px] text-danger">
              Couldn&apos;t load requests: {listError}
            </p>
          ) : null}
          {!loading && items.length === 0 && !listError ? (
            <div className="flex flex-col items-center px-6 py-16 text-center">
              <Activity size={22} className="mb-3 text-muted" />
              <p className="text-[15px] font-semibold text-ink">{filtered ? "No requests match these filters" : "Waiting for requests"}</p>
              <p className="mt-1 max-w-[42ch] text-[13px] text-ink-2">
                {filtered
                  ? "Change or clear the filters above. New matching requests appear here as they arrive."
                  : "Send a request to one of your tunnels and it shows up here instantly."}
              </p>
              {!filtered ? <Command className="mt-4 w-full max-w-sm text-left">tund http 3000</Command> : null}
            </div>
          ) : null}
          <ul className={cn(loading && items.length > 0 && "opacity-60")}>
            {items.map((r) => {
              const active = r.id === selectedId;
              return (
                <li key={r.id} data-id={r.id}>
                  <button
                    type="button"
                    onClick={() => select(r.id)}
                    className={cn(
                      "grid w-full grid-cols-[3.25rem_minmax(0,1fr)_auto] items-center gap-x-2 border-b border-line px-3 py-2 text-left",
                      active || (compare && (r.id === compare.a || r.id === compare.b)) || r.id === picking
                        ? "bg-surface-3"
                        : "hover:bg-surface-2",
                      fresh.has(r.id) && !active && "flash-new",
                    )}
                  >
                    <span className="font-mono text-[12px] font-medium text-ink-2">{r.method}</span>
                    <span className="min-w-0">
                      <span className="block truncate font-mono text-[12.5px] text-ink" title={r.path}>
                        {r.path}
                      </span>
                      <span className="block truncate text-[11.5px] text-muted">
                        {filters.host ? "" : `${r.hostname}  `}
                        {formatClock(r.startedAt, tz)}
                        {r.replayOf ? "  replay" : ""}
                        {r.teamSlug ? (
                          <span className="ml-2 rounded-[3px] border border-line-strong px-1 text-[10.5px] text-ink-2">
                            team {r.teamSlug}
                          </span>
                        ) : null}
                      </span>
                    </span>
                    <span className="flex flex-col items-end gap-0.5">
                      <StatusCode status={r.status} error={r.error} />
                      <span className="text-[11.5px] tabular text-muted">
                        {formatDuration(r.durationMs)}
                        {r.respSize ? `, ${formatBytes(r.respSize)}` : ""}
                      </span>
                    </span>
                  </button>
                </li>
              );
            })}
          </ul>
          {hasMore ? (
            <div ref={moreRef} className="p-3 text-center">
              <button type="button" onClick={loadMore} className={buttonClass("secondary", "sm")}>
                Load older requests
              </button>
            </div>
          ) : null}
        </div>

        {/* Detail */}
        <div className={cn("min-h-0 min-w-0 flex-1 flex-col", showDetail && !picking ? "flex" : "hidden lg:flex")}>
          {compare ? (
            <CompareView
              leftId={compare.a}
              rightId={compare.b}
              onSwap={() => setCompare({ a: compare.b, b: compare.a })}
              onClose={() => {
                setCompare(null);
                setSelectedId(compare.a);
              }}
            />
          ) : detail ? (
            <RequestDetail
              key={detail.id}
              detail={detail}
              loading={detailLoading}
              onReplay={replay}
              onReplayed={replayed}
              replaying={replaying}
              replayError={replayError}
              onCompare={() => setPicking(detail.id)}
              onClose={() => setSelectedId("")}
            />
          ) : detailError ? (
            <div className="m-auto max-w-sm px-6 text-center">
              <p className="text-[15px] font-semibold text-ink">Can&apos;t show this request</p>
              <p className="mt-1 text-[13px] text-ink-2">{detailError}. It may have been cleared or removed by retention.</p>
            </div>
          ) : (
            <div className="m-auto px-6 text-center text-[13px] text-muted">
              {detailLoading ? "Loading…" : "Select a request to see its headers, body and timing."}
              {!detailLoading ? (
                <p className="mt-2 text-[12px]">
                  Use <kbd className="rounded border border-line-strong px-1 font-mono">↑</kbd>{" "}
                  <kbd className="rounded border border-line-strong px-1 font-mono">↓</kbd> in the list to move between requests.
                </p>
              ) : null}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

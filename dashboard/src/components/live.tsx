"use client";

import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import type { RequestSummary } from "@/lib/requests";
import type { ConnectionSummary } from "@/lib/connections";

export type TunnelEvent = { id: string; hostname: string; event: "online" | "offline" };
export type LiveEvent =
  | { type: "request"; data: RequestSummary }
  | { type: "tunnel"; data: TunnelEvent }
  | { type: "connection"; data: ConnectionSummary };

type Listener = (e: LiveEvent) => void;

type LiveContextValue = {
  connected: boolean;
  /** False until the stream opened once, so the first attempt reads "Connecting". */
  everConnected: boolean;
  subscribe: (fn: Listener) => () => void;
};

const LiveContext = createContext<LiveContextValue | null>(null);

/** One EventSource per tab, shared by every live component on the page. */
export function LiveProvider({ children }: { children: ReactNode }) {
  const listeners = useRef(new Set<Listener>());
  const [connected, setConnected] = useState(false);
  const [everConnected, setEverConnected] = useState(false);

  useEffect(() => {
    let es: EventSource | null = null;
    let retry: ReturnType<typeof setTimeout> | null = null;
    let attempts = 0;
    let closed = false;

    const connect = () => {
      es = new EventSource("/api/stream");
      es.onopen = () => {
        attempts = 0;
        setConnected(true);
        setEverConnected(true);
      };
      const dispatch = (type: LiveEvent["type"]) => (msg: MessageEvent) => {
        try {
          const e = { type, data: JSON.parse(msg.data) } as LiveEvent;
          for (const fn of listeners.current) fn(e);
        } catch {
          /* ignore malformed event */
        }
      };
      es.addEventListener("request", dispatch("request"));
      es.addEventListener("tunnel", dispatch("tunnel"));
      es.addEventListener("connection", dispatch("connection"));
      es.onerror = () => {
        setConnected(false);
        es?.close();
        if (closed) return;
        attempts++;
        retry = setTimeout(connect, Math.min(15_000, 1000 * 2 ** Math.min(attempts, 4)));
      };
    };
    connect();
    return () => {
      closed = true;
      if (retry) clearTimeout(retry);
      es?.close();
    };
  }, []);

  const subscribe = useCallback((fn: Listener) => {
    const set = listeners.current;
    set.add(fn);
    return () => {
      set.delete(fn);
    };
  }, []);
  const value = useMemo(() => ({ connected, everConnected, subscribe }), [connected, everConnected, subscribe]);

  return <LiveContext.Provider value={value}>{children}</LiveContext.Provider>;
}

export function useLive(): LiveContextValue {
  const ctx = useContext(LiveContext);
  if (!ctx) throw new Error("useLive must be used inside <LiveProvider>");
  return ctx;
}

/** Subscribes to live events; the handler may change between renders. */
export function useLiveEvents(handler: Listener) {
  const { subscribe } = useLive();
  const ref = useRef(handler);
  useEffect(() => {
    ref.current = handler;
  });
  useEffect(() => subscribe((e) => ref.current(e)), [subscribe]);
}

export function LiveIndicator() {
  const { connected, everConnected } = useLive();
  return (
    <span
      className="inline-flex items-center gap-2 text-[12px] text-muted"
      title={connected ? "Receiving live updates" : everConnected ? "Reconnecting to the live stream…" : "Connecting to the live stream…"}
    >
      {connected ? <span className="live-dot h-1.5! w-1.5!" /> : <span className="h-1.5 w-1.5 rounded-full bg-line-strong" />}
      {connected ? "Live" : everConnected ? "Reconnecting" : "Connecting"}
    </span>
  );
}

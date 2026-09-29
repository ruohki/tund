"use client";

import { useRouter } from "next/navigation";
import { useEffect, useRef } from "react";
import { useLiveEvents } from "./live";

/**
 * Re-renders the current server page when live data changes. Tunnel events
 * refresh right away; request events are throttled.
 */
export function LiveRefresh({ onRequests = true, throttleMs = 5000 }: { onRequests?: boolean; throttleMs?: number }) {
  const router = useRouter();
  const last = useRef(0);
  const pending = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(
    () => () => {
      if (pending.current) clearTimeout(pending.current);
    },
    [],
  );

  useLiveEvents((e) => {
    if (e.type === "tunnel") {
      router.refresh();
      last.current = Date.now();
      return;
    }
    if (!onRequests || pending.current) return;
    const wait = Math.max(0, last.current + throttleMs - Date.now());
    pending.current = setTimeout(() => {
      pending.current = null;
      last.current = Date.now();
      router.refresh();
    }, wait);
  });
  return null;
}

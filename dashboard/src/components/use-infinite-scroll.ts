"use client";

import { useEffect, useRef } from "react";

/**
 * Infinite lists: calls `load` whenever the returned sentinel comes within
 * 400px of the visible part of its scroll container (the closest ancestor with
 * `data-scroll-root`, else the viewport), while `enabled`. `count` (the list
 * length) re-arms the observer after each load, so a page that leaves the
 * sentinel in view loads the next one too.
 */
export function useInfiniteScroll<T extends HTMLElement = HTMLDivElement>(enabled: boolean, count: number, load: () => Promise<void>) {
  const ref = useRef<T>(null);
  const loadRef = useRef(load);
  const busy = useRef(false);
  useEffect(() => {
    loadRef.current = load;
  });
  useEffect(() => {
    const el = ref.current;
    if (!enabled || !el || typeof IntersectionObserver === "undefined") return;
    const io = new IntersectionObserver(
      (entries) => {
        if (busy.current || !entries.some((e) => e.isIntersecting)) return;
        busy.current = true;
        loadRef.current().finally(() => {
          busy.current = false;
        });
      },
      { root: el.closest("[data-scroll-root]"), rootMargin: "0px 0px 400px 0px" },
    );
    io.observe(el);
    return () => io.disconnect();
  }, [enabled, count]);
  return ref;
}

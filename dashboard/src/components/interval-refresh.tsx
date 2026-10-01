"use client";

import { useRouter } from "next/navigation";
import { useEffect } from "react";

/** Re-renders the current server page every `ms` while the tab is visible. */
export function IntervalRefresh({ ms }: { ms: number }) {
  const router = useRouter();
  useEffect(() => {
    const id = setInterval(() => {
      if (document.visibilityState === "visible") router.refresh();
    }, ms);
    return () => clearInterval(id);
  }, [ms, router]);
  return null;
}

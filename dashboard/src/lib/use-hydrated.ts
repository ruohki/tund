import { useSyncExternalStore } from "react";

const subscribe = () => () => {};

/**
 * False during SSR and hydration, true afterwards. Components that format
 * dates use it to render UTC first (identical on server and client) and switch
 * to the viewer's local time zone right after hydration.
 */
export function useHydrated(): boolean {
  return useSyncExternalStore(
    subscribe,
    () => true,
    () => false,
  );
}

/** Time zone to format with: UTC until hydrated, then the browser's own. */
export function useDisplayTimeZone(): string | undefined {
  return useHydrated() ? undefined : "UTC";
}

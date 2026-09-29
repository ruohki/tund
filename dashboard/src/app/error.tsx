"use client";

import { ErrorView } from "@/components/error-view";

export default function RootError({ error, retry }: { error: Error & { digest?: string }; retry: () => void }) {
  return (
    <div className="min-h-dvh px-4">
      <ErrorView error={error} retry={retry} />
    </div>
  );
}

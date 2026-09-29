"use client";

import "./globals.css";
import { ErrorView } from "@/components/error-view";

const themeScript = `try{var t=localStorage.getItem("tund-theme");if(t==="light"||t==="dark")document.documentElement.dataset.theme=t}catch(e){}`;

// Replaces the root layout when it fails, so it brings its own document, styles and theme.
export default function GlobalError({ error, retry }: { error: Error & { digest?: string }; retry: () => void }) {
  return (
    <html lang="en" suppressHydrationWarning>
      <head>
        <title>Error · tund</title>
        <meta name="robots" content="noindex, nofollow" />
        <script dangerouslySetInnerHTML={{ __html: themeScript }} />
      </head>
      <body className="min-h-dvh" style={{ fontFamily: "ui-sans-serif, system-ui, sans-serif" }}>
        <ErrorView error={error} retry={retry} standalone />
      </body>
    </html>
  );
}

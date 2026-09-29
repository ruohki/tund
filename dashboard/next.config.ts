import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  output: "standalone",
  // Lets a verification build run next to a live `next start` without replacing its .next.
  distDir: process.env.NEXT_DIST_DIR || ".next",
  poweredByHeader: false,
  serverExternalPackages: ["postgres"],
  // Fonts read at runtime by the Open Graph image routes (src/lib/og.tsx).
  outputFileTracingIncludes: {
    "/opengraph-image": ["./assets/fonts/**"],
    "/twitter-image": ["./assets/fonts/**"],
  },
};

export default nextConfig;

import "server-only";
import { readFile } from "node:fs/promises";
import { join } from "node:path";
import { ImageResponse } from "next/og";
import { OG_ALT, OG_SIZE, siteInfo, TAGLINE } from "./seo";

// Shared renderer for opengraph-image and twitter-image. Fonts are bundled in
// assets/fonts (OFL) and traced into the standalone build (next.config.ts).

export { OG_ALT, OG_SIZE };

const fontDir = join(process.cwd(), "assets/fonts");
const fonts = Promise.all([
  readFile(join(fontDir, "barlow-latin-600-normal.woff")),
  readFile(join(fontDir, "barlow-latin-500-normal.woff")),
  readFile(join(fontDir, "jetbrains-mono-latin-500-normal.woff")),
]);

const C = {
  page: "#edeff2",
  surface: "#fbfcfc",
  ink: "#14171c",
  ink2: "#474e5a",
  muted: "#737b89",
  line: "#c3c9d2",
  sodium: "#f0a81c",
  term: "#16191e",
  termLine: "#262b33",
  termInk: "#e3e6ea",
  termMuted: "#858d99",
  ok: "#5fcf8e",
};

function Mark({ size }: { size: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 32 32">
      <path d="M5 27V15a11 11 0 0 1 22 0v12" fill="none" stroke={C.ink} strokeWidth="3" />
      <path d="M10.5 27V15.5a5.5 5.5 0 0 1 11 0V27" fill={C.sodium} />
    </svg>
  );
}

export async function renderOgImage() {
  const [[semi, medium, mono], site] = await Promise.all([fonts, siteInfo()]);
  const tunnel = `brave-otter-4821.${site.baseDomain}`;
  const row = (label: string, value: string, color = C.termInk) => (
    <div style={{ display: "flex", gap: 18 }}>
      <span style={{ width: 118, color: C.termMuted }}>{label}</span>
      <span style={{ color }}>{value}</span>
    </div>
  );
  return new ImageResponse(
    (
      <div
        style={{
          width: "100%",
          height: "100%",
          display: "flex",
          flexDirection: "column",
          background: C.page,
          fontFamily: "Barlow",
          color: C.ink,
          padding: "56px 64px 48px",
        }}
      >
        <div style={{ display: "flex", alignItems: "center", gap: 14 }}>
          <Mark size={44} />
          <span style={{ fontSize: 36, fontWeight: 600, letterSpacing: -1 }}>{site.name}</span>
        </div>

        <div style={{ display: "flex", marginTop: 36, gap: 44, flex: 1 }}>
          <div style={{ display: "flex", flexDirection: "column", width: 560 }}>
            <div style={{ fontSize: 62, fontWeight: 600, lineHeight: 1.04, letterSpacing: -2 }}>{`${TAGLINE}.`}</div>
            <div style={{ display: "flex", marginTop: 26, fontSize: 25, color: C.ink2, fontWeight: 500, lineHeight: 1.35 }}>
              One command. The same URL every run. Every request recorded.
            </div>
          </div>
          <div
            style={{
              display: "flex",
              flexDirection: "column",
              flex: 1,
              alignSelf: "flex-start",
              background: C.term,
              borderRadius: 14,
              border: `1px solid ${C.termLine}`,
              padding: "22px 26px",
              fontFamily: "JetBrains Mono",
              fontSize: 19,
              color: C.termInk,
              gap: 8,
            }}
          >
            <div style={{ display: "flex", gap: 8, marginBottom: 10 }}>
              <span style={{ width: 11, height: 11, borderRadius: 11, background: C.termLine }} />
              <span style={{ width: 11, height: 11, borderRadius: 11, background: C.termLine }} />
              <span style={{ width: 11, height: 11, borderRadius: 11, background: C.termLine }} />
            </div>
            <div style={{ display: "flex" }}>
              <span style={{ color: C.termMuted, marginRight: 12 }}>$</span>tund http 3000
            </div>
            <div style={{ display: "flex", gap: 18, alignItems: "center" }}>
              <span style={{ width: 118, color: C.termMuted }}>Session</span>
              <span style={{ width: 11, height: 11, borderRadius: 11, background: C.sodium, marginRight: -8 }} />
              <span style={{ color: C.sodium }}>online</span>
            </div>
            {row("Forwarding", "localhost:3000")}
            <div style={{ display: "flex", borderTop: `1px solid ${C.termLine}`, marginTop: 8, paddingTop: 12, gap: 16 }}>
              <span style={{ color: C.termMuted }}>GET</span>
              <span style={{ flex: 1 }}>/api/checkout</span>
              <span style={{ color: C.ok }}>200 OK</span>
            </div>
            <div style={{ display: "flex", gap: 16 }}>
              <span style={{ color: C.termMuted }}>POST</span>
              <span style={{ flex: 1 }}>/webhooks/stripe</span>
              <span style={{ color: C.ok }}>201</span>
            </div>
          </div>
        </div>

        <div style={{ display: "flex", alignItems: "center", gap: 18, fontFamily: "JetBrains Mono", fontSize: 21, marginTop: 34 }}>
          <span style={{ display: "flex", padding: "9px 16px", borderRadius: 9, border: `1.5px solid ${C.line}`, background: C.surface }}>
            {`https://${tunnel}`}
          </span>
          <div style={{ display: "flex", flex: 1, alignItems: "center" }}>
            <div style={{ display: "flex", flex: 1, height: 4, borderRadius: 4, background: C.sodium, boxShadow: `0 0 14px ${C.sodium}` }} />
            <div
              style={{
                display: "flex",
                width: 14,
                height: 14,
                borderTop: `4px solid ${C.sodium}`,
                borderRight: `4px solid ${C.sodium}`,
                transform: "rotate(45deg)",
                marginLeft: -12,
              }}
            />
          </div>
          <span style={{ display: "flex", padding: "9px 16px", borderRadius: 9, border: `1.5px solid ${C.line}`, background: C.surface }}>
            localhost:3000
          </span>
        </div>
        <div style={{ display: "flex", marginTop: 18, fontSize: 20, color: C.muted, fontWeight: 500 }}>{site.host}</div>
      </div>
    ),
    {
      ...OG_SIZE,
      fonts: [
        { name: "Barlow", data: semi, weight: 600, style: "normal" },
        { name: "Barlow", data: medium, weight: 500, style: "normal" },
        { name: "JetBrains Mono", data: mono, weight: 500, style: "normal" },
      ],
    },
  );
}

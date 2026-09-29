import { ImageResponse } from "next/og";

export const size = { width: 180, height: 180 };
export const contentType = "image/png";

// The tund mark on a dark tile, like icon.svg.
export default function AppleIcon() {
  return new ImageResponse(
    (
      <div style={{ width: "100%", height: "100%", display: "flex", alignItems: "center", justifyContent: "center", background: "#14171c" }}>
        <svg width="132" height="132" viewBox="0 0 32 32">
          <path d="M7 25V15a9 9 0 0 1 18 0v10" fill="none" stroke="#3a414d" strokeWidth="3" />
          <path d="M11.5 25v-9.5a4.5 4.5 0 0 1 9 0V25" fill="#f0a81c" />
        </svg>
      </div>
    ),
    size,
  );
}

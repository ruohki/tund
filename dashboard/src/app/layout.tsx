import type { Metadata, Viewport } from "next";
import { Barlow, JetBrains_Mono } from "next/font/google";
import "./globals.css";
import { DESCRIPTION, siteInfo, TAGLINE } from "@/lib/seo";

const barlow = Barlow({
  subsets: ["latin"],
  weight: ["400", "500", "600", "700"],
  variable: "--font-barlow",
  display: "swap",
});

const jetbrains = JetBrains_Mono({
  subsets: ["latin"],
  weight: ["400", "500"],
  variable: "--font-jetbrains",
  display: "swap",
});

// Every page reads the database or runtime configuration (TUND_* env), so
// nothing may be prerendered at build time.
export const dynamic = "force-dynamic";

export async function generateMetadata(): Promise<Metadata> {
  // Branding comes from instance settings; falls back to "tund" if the database isn't reachable yet.
  const { name, url } = await siteInfo();
  return {
    metadataBase: new URL(url),
    applicationName: name,
    title: { default: name, template: `%s · ${name}` },
    description: DESCRIPTION,
    // App pages are private. Public pages (landing, sign-in, sign-up) opt back in.
    robots: { index: false, follow: false },
    openGraph: { type: "website", siteName: name, title: `${name}: ${TAGLINE}`, description: DESCRIPTION, locale: "en_US" },
    twitter: { card: "summary_large_image", title: `${name}: ${TAGLINE}`, description: DESCRIPTION },
  };
}

export const viewport: Viewport = {
  themeColor: [
    { media: "(prefers-color-scheme: light)", color: "#edeff2" },
    { media: "(prefers-color-scheme: dark)", color: "#0f1216" },
  ],
};

// Applies the saved theme before first paint so there is no flash.
const themeScript = `try{var t=localStorage.getItem("tund-theme");if(t==="light"||t==="dark")document.documentElement.dataset.theme=t}catch(e){}`;

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en" className={`${barlow.variable} ${jetbrains.variable}`} suppressHydrationWarning>
      <head>
        <script dangerouslySetInnerHTML={{ __html: themeScript }} />
      </head>
      <body className="min-h-dvh antialiased">{children}</body>
    </html>
  );
}

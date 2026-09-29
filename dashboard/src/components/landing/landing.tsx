import Link from "next/link";
import type { ReactNode } from "react";
import { publicConfig } from "@/lib/config";
import { getSettings } from "@/lib/settings";
import { DESCRIPTION, siteInfo } from "@/lib/seo";
import { Wordmark } from "../brand";
import { buttonClass, StatusCode } from "../ui";
import { ThemeToggle } from "../theme-toggle";
import { LandingInstall } from "./install";

/** A still of the client's live view: what `tund http 3000` prints. */
function TerminalStill({ url, dashboardUrl }: { url: string; dashboardUrl: string }) {
  const row = (label: string, value: ReactNode) => (
    <div className="flex gap-3">
      <span className="w-[5.5rem] shrink-0 text-term-muted">{label}</span>
      <span className="min-w-0 truncate">{value}</span>
    </div>
  );
  const requests: [string, string, string, string, string][] = [
    ["14:02:11", "GET", "/", "200 OK", "12ms"],
    ["14:02:12", "GET", "/assets/app.js", "200 OK", "3.1ms"],
    ["14:02:19", "POST", "/api/checkout", "201 Created", "48ms"],
    ["14:02:24", "POST", "/webhooks/stripe", "200 OK", "31ms"],
  ];
  return (
    <figure
      aria-label="Terminal output of tund http 3000"
      className="overflow-hidden rounded-lg border border-term-line bg-term-bg font-mono text-[12.5px] leading-[1.7] text-term-ink shadow-pop"
    >
      <div className="flex items-center gap-1.5 border-b border-term-line px-4 py-2.5" aria-hidden>
        <span className="h-2.5 w-2.5 rounded-full bg-term-line" />
        <span className="h-2.5 w-2.5 rounded-full bg-term-line" />
        <span className="h-2.5 w-2.5 rounded-full bg-term-line" />
      </div>
      <div className="overflow-x-auto px-4 py-3.5 scroll-thin">
        <p>
          <span className="text-term-muted">$ </span>tund http 3000
        </p>
        <div className="mt-2">
          {row(
            "Session",
            <span className="inline-flex items-center gap-2">
              <span className="live-dot h-1.5! w-1.5!" aria-hidden />
              online
            </span>,
          )}
          {row("Account", "ada@example.com")}
          {row(
            "Forwarding",
            <>
              <span className="text-[color:var(--sodium)]">{url}</span>
              <span className="text-term-muted"> → </span>localhost:3000
            </>,
          )}
          {row("Inspector", `${dashboardUrl.replace(/^https?:\/\//, "")}/inspect`)}
        </div>
        <div className="mt-3 border-t border-term-line pt-2.5">
          {requests.map(([t, m, p, s, d]) => (
            <div key={t + p} className="flex gap-3 whitespace-nowrap">
              <span className="text-term-muted">{t}</span>
              <span className="w-10 font-medium">{m}</span>
              <span className="w-36 truncate">{p}</span>
              <span className={s.startsWith("2") ? "w-24 text-[#5fcf8e]" : "w-24"}>{s}</span>
              <span className="text-term-muted">{d}</span>
            </div>
          ))}
        </div>
      </div>
    </figure>
  );
}

function Feature({
  title,
  children,
  example,
  wide = false,
}: {
  title: string;
  children: ReactNode;
  example: ReactNode;
  /** Spans both columns (keeps the grid even when a feature is off). */
  wide?: boolean;
}) {
  return (
    <div className={`flex flex-col gap-4 bg-surface p-6${wide ? " md:col-span-2" : ""}`}>
      <div>
        <h3 className="text-[17px] font-semibold tracking-[-0.01em] text-ink">{title}</h3>
        <p className="mt-1.5 max-w-[46ch] text-[14px] text-ink-2">{children}</p>
      </div>
      <div className="mt-auto">{example}</div>
    </div>
  );
}

const exampleBox = "rounded-md border border-line bg-surface-2 px-3 py-2 font-mono text-[12.5px] text-ink";

export async function Landing() {
  const cfg = publicConfig();
  const site = await siteInfo();
  const { browser_warning: browserWarning, custom_domains: customDomains } = await getSettings();
  const url = `${cfg.scheme}://brave-otter-4821.${cfg.baseDomain}${cfg.portSuffix}`;
  const host = url.replace(/^https?:\/\//, "");

  const jsonLd = {
    "@context": "https://schema.org",
    "@type": "SoftwareApplication",
    name: site.name,
    applicationCategory: "DeveloperApplication",
    operatingSystem: "macOS, Linux, Windows",
    url: cfg.dashboardUrl,
    description: DESCRIPTION,
    downloadUrl: `${cfg.dashboardUrl}/install.sh`,
    softwareHelp: `${cfg.dashboardUrl}/get-started`,
    featureList: [
      "Public HTTPS URL for a local port",
      "Request inspector with replay",
      "Password and single sign-on protection",
      ...(customDomains ? ["Custom domains"] : []),
      "TCP and TLS tunnels",
    ],
  };
  return (
    <div className="min-h-dvh">
      <script
        type="application/ld+json"
        // Escape "<" so the JSON can't close the script element.
        dangerouslySetInnerHTML={{ __html: JSON.stringify(jsonLd).replace(/</g, "\\u003c") }}
      />
      <header className="mx-auto flex max-w-[1180px] items-center justify-between px-5 py-5 sm:px-8">
        <Link href="/" aria-label={`${site.name} home`}>
          <Wordmark name={site.name} />
        </Link>
        <nav className="flex items-center gap-2" aria-label="Account">
          <Link href="/login" className={buttonClass("ghost")}>
            Sign in
          </Link>
          <Link href="/signup" className={buttonClass("primary")}>
            Create account
          </Link>
        </nav>
      </header>

      <main>
        <section className="mx-auto grid max-w-[1180px] gap-12 px-5 pt-10 pb-16 sm:px-8 lg:grid-cols-[minmax(0,1fr)_minmax(0,1.05fr)] lg:items-center lg:pt-16">
          <div>
            <h1 className="text-[40px] font-semibold leading-[1.04] tracking-[-0.025em] text-ink sm:text-[54px]">
              Share localhost on a public HTTPS address.
            </h1>
            <p className="mt-5 max-w-[52ch] text-[17px] leading-relaxed text-ink-2">
              <code className="font-mono text-[15px] text-ink">tund http 3000</code> gives your local port an HTTPS URL
              that stays the same every run, and records each request so you can inspect it and replay it.
            </p>
            <div className="mt-8 flex flex-wrap gap-2">
              <Link href="/signup" className={buttonClass("primary", "md", "h-10 px-5 text-[15px]")}>
                Create an account
              </Link>
              <Link href="/login" className={buttonClass("secondary", "md", "h-10 px-5 text-[15px]")}>
                Sign in
              </Link>
            </div>
          </div>
          <TerminalStill url={url} dashboardUrl={cfg.dashboardUrl} />
        </section>

        <section aria-label="How a request travels" className="border-y border-line bg-surface">
          <div className="mx-auto flex max-w-[1180px] items-center gap-4 px-5 py-6 font-mono text-[12.5px] sm:px-8 sm:text-[13px]">
            <span className="min-w-0 truncate rounded-[5px] border border-line-strong bg-surface-2 px-2.5 py-1.5 text-ink">
              {host}
            </span>
            <span className="route-tube min-w-10 flex-1" data-lit="true" aria-hidden>
              <span className="route-pulse" style={{ animationIterationCount: "infinite", animationDuration: "2.6s" }} />
            </span>
            <span className="hidden text-muted sm:inline">encrypted tunnel</span>
            <span className="route-tube hidden min-w-10 flex-1 sm:block" data-lit="true" aria-hidden />
            <span className="shrink-0 rounded-[5px] border border-line-strong bg-surface-2 px-2.5 py-1.5 text-ink">
              localhost:3000
            </span>
          </div>
        </section>

        <section className="mx-auto grid max-w-[1180px] gap-10 px-5 py-16 sm:px-8 lg:grid-cols-[minmax(0,0.8fr)_minmax(0,1.2fr)]">
          <div>
            <h2 className="text-[28px] font-semibold leading-tight tracking-[-0.02em] text-ink">Two commands</h2>
            <p className="mt-3 max-w-[40ch] text-[15px] text-ink-2">
              The client connects out over HTTPS, so it works behind NAT, corporate firewalls and hotel Wi-Fi on macOS,
              Linux and Windows.
            </p>
          </div>
          <LandingInstall dashboardUrl={cfg.dashboardUrl} />
        </section>

        <section className="mx-auto max-w-[1180px] px-5 pb-16 sm:px-8">
          <h2 className="mb-6 text-[28px] font-semibold leading-tight tracking-[-0.02em] text-ink">
            Built for showing work and testing webhooks
          </h2>
          <div className="grid gap-px overflow-hidden rounded-lg border border-line bg-line md:grid-cols-2">
            <Feature
              title="A stable HTTPS address"
              example={<p className={exampleBox}>{url}</p>}
            >
              Certificates are handled for you. Your account keeps its address between runs, so webhook settings and
              shared links keep working. Pin more names, or pass <code className="font-mono text-[13px]">--random</code>{" "}
              for a throwaway one.
            </Feature>
            <Feature
              title="Every request, recorded"
              example={
                <ul className="divide-y divide-line overflow-hidden rounded-md border border-line bg-surface-2 text-[12.5px]">
                  {(
                    [
                      ["POST", "/webhooks/stripe", 200],
                      ["POST", "/api/checkout", 422],
                      ["GET", "/api/orders?page=2", 500],
                    ] as const
                  ).map(([m, p, s]) => (
                    <li key={p} className="grid grid-cols-[3.25rem_minmax(0,1fr)_auto] items-center gap-2 px-3 py-1.5">
                      <span className="font-mono text-[12px] font-medium text-ink-2">{m}</span>
                      <span className="truncate font-mono text-ink">{p}</span>
                      <StatusCode status={s} />
                    </li>
                  ))}
                </ul>
              }
            >
              Headers, bodies and timings appear in the inspector as they happen. Replay a request against your local
              server with one click, or copy it as cURL.
            </Feature>
            <Feature
              title="Private when it needs to be"
              wide={!customDomains}
              example={
                <div className="flex flex-col gap-1.5">
                  <p className={exampleBox}>tund http 3000 --password &apos;correct horse&apos;</p>
                  <p className={exampleBox}>tund http 3000 --oidc company --oidc-allow @company.com</p>
                </div>
              }
            >
              Ask visitors for a password, or make them sign in with Google, Entra ID, Keycloak or any OpenID Connect
              provider before a request reaches your machine.
              {browserWarning
                ? " Public links also get abuse protection: browsers see a one-time warning before an unprotected tunnel."
                : null}
            </Feature>
            {customDomains ? (
              <Feature
                title="Your own domains"
                example={<p className={exampleBox}>tund http 3000 --domain api.example.com</p>}
              >
                Point a domain, or a whole wildcard, at {site.name} and serve tunnels from it. Certificates are issued
                automatically once DNS is in place.
              </Feature>
            ) : null}
          </div>
        </section>

        <section className="mx-auto max-w-[1180px] px-5 pb-16 sm:px-8">
          <div className="grid gap-8 rounded-lg border border-line bg-surface p-6 sm:p-8 lg:grid-cols-[minmax(0,1fr)_minmax(0,1fr)] lg:items-center">
            <div>
              <h2 className="text-[24px] font-semibold leading-tight tracking-[-0.02em] text-ink">Built for AI agents</h2>
              <p className="mt-3 max-w-[48ch] text-[15px] text-ink-2">
                <code className="font-mono text-[13.5px] text-ink">tund mcp</code> lets Claude Code, Cursor, Codex and
                other coding agents start a tunnel, send you the URL and read the captured requests while they debug.
                You decide which ports they may share and whether every tunnel needs a password.
              </p>
              {/* Visitors here are signed out; go through /login explicitly so the #mcp anchor survives. */}
              <Link href={`/login?next=${encodeURIComponent("/get-started#mcp")}`} className={buttonClass("secondary", "md", "mt-5")}>
                Set up an agent
              </Link>
            </div>
            <div className="flex flex-col gap-2 font-mono text-[12.5px]">
              <p className={exampleBox}>claude mcp add tund -- tund mcp</p>
              <p className="mt-2 rounded-md border-l-2 border-sodium bg-surface-2 px-3 py-2 font-sans text-[14px] text-ink">
                Start the dev server and share it with me via tund.
              </p>
            </div>
          </div>
        </section>

        <section className="border-t border-line bg-surface">
          <div className="mx-auto grid max-w-[1180px] gap-10 px-5 py-16 sm:px-8 lg:grid-cols-[minmax(0,0.8fr)_minmax(0,1.2fr)]">
            <div>
              <h2 className="text-[28px] font-semibold leading-tight tracking-[-0.02em] text-ink">Self-host it</h2>
              <p className="mt-3 max-w-[44ch] text-[15px] text-ink-2">
                The same server, dashboard and inspector run on a machine you control: one Docker Compose file with
                Postgres and a wildcard DNS record. Clients downloaded from your server connect to it automatically.
              </p>
            </div>
            <div className="flex flex-col gap-2 font-mono text-[12.5px]">
              <p className="text-muted"># on your server, with *.tunnels.example.com pointing at it</p>
              <p className={exampleBox}>docker compose up -d</p>
              <p className="mt-3 text-muted"># a client installed from somewhere else</p>
              <p className={exampleBox}>tund config set-server https://dashboard.tunnels.example.com</p>
            </div>
          </div>
        </section>

        <section className="mx-auto flex max-w-[1180px] flex-wrap items-center justify-between gap-6 px-5 py-14 sm:px-8">
          <p className="text-[22px] font-semibold tracking-[-0.015em] text-ink">Get a URL for localhost:3000 now.</p>
          <div className="flex gap-2">
            <Link href="/signup" className={buttonClass("primary", "md", "h-10 px-5 text-[15px]")}>
              Create an account
            </Link>
            <Link href="/login" className={buttonClass("secondary", "md", "h-10 px-5 text-[15px]")}>
              Sign in
            </Link>
          </div>
        </section>
      </main>

      <footer className="border-t border-line">
        <div className="mx-auto flex max-w-[1180px] flex-wrap items-center justify-between gap-4 px-5 py-6 text-[12.5px] text-muted sm:px-8">
          <span className="flex flex-wrap items-center gap-4">
            <span>{cfg.dashboardHost}</span>
            <Link href="/terms" className="underline-offset-4 hover:text-ink hover:underline">
              Terms
            </Link>
            <Link href="/acceptable-use" className="underline-offset-4 hover:text-ink hover:underline">
              Acceptable use
            </Link>
            <Link href="/report" className="underline-offset-4 hover:text-ink hover:underline">
              Report abuse
            </Link>
          </span>
          <ThemeToggle />
        </div>
      </footer>
    </div>
  );
}

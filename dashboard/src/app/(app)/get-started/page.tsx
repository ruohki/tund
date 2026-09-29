import type { Metadata } from "next";
import Link from "next/link";
import { requireUser } from "@/lib/auth";
import { defaultStaticHostname } from "@/lib/static-hostnames";
import { config, publicConfig } from "@/lib/config";
import { getSettings } from "@/lib/settings";
import { tcpConfig } from "@/lib/tcp";
import { verificationRequired } from "@/lib/mail";
import { ButtonLink, PageHeader, Panel } from "@/components/ui";
import { Command } from "@/components/client-ui";
import { InstallSteps } from "@/components/install-steps";
import { IDENTITY_EXAMPLE, IdentityHeaderTable } from "@/components/identity-headers";
import { ApiSection, McpSection } from "./agents";

export const metadata: Metadata = { title: "Get started" };

const PLATFORMS = [
  { os: "linux", label: "Linux", arches: ["amd64", "arm64"] },
  { os: "darwin", label: "macOS", arches: ["arm64", "amd64"] },
  { os: "windows", label: "Windows", arches: ["amd64", "arm64"] },
];

const ARCH_LABEL: Record<string, string> = { amd64: "x86-64", arm64: "ARM64" };

export default async function GetStartedPage() {
  const user = await requireUser();
  const cfg = publicConfig();
  const [defaultHost, tcp, verifyRequired] = await Promise.all([
    defaultStaticHostname(user.id),
    tcpConfig(),
    verificationRequired(),
  ]);
  const mustVerify = !user.emailVerified && verifyRequired;
  const staticUrl = defaultHost ? config().publicUrl(defaultHost) : null;

  const flags: [string, string][] = [
    ["--subdomain <name>", `Use <name>.${cfg.baseDomain} instead of your default static hostname.`],
    ["--pin", "Keep the hostname of this run as one of your static hostnames."],
    ["--random", "Use a throwaway hostname for this run only."],
    ["--allow-ip <list>", "Only accept visitors from these IPs or CIDRs, e.g. 203.0.113.7,10.0.0.0/8. Works for HTTP, TCP and TLS."],
    ["--domain <host>", "Use a custom domain you verified under Domains."],
    ["--name <name>", "Label shown in the dashboard. Defaults to http-<port>."],
    ["--host-header rewrite", "Send Host: localhost:<port> upstream, for dev servers that reject unknown hosts."],
    ["--password <secret>", "Ask visitors for a password before they reach your service."],
    ["--oidc <slug>", "Make visitors sign in with an identity provider from Access control."],
    ["--oidc-allow <list>", "Only let these emails or @domains through, comma separated."],
    ["--log", "Print plain log lines instead of the live view (for CI and services)."],
  ];

  const yaml = `tunnels:
  web:
    addr: 3000
    subdomain: my-app
  api:
    addr: 8080
    host_header: rewrite
    auth:
      password: correct-horse
  admin:
    addr: https://localhost:8443
    domain: admin.example.com
    auth:
      oidc: company
      allow: ["@example.com"]`;

  return (
    <>
      <PageHeader
        title="Get started"
        description="Install the client on the machine that runs your service. It connects out over HTTPS, so it works behind NAT and firewalls without admin rights."
        actions={
          <>
            <ButtonLink href="#mcp" variant="ghost" size="sm">
              AI agents (MCP)
            </ButtonLink>
            <ButtonLink href="#api" variant="ghost" size="sm">
              HTTP API
            </ButtonLink>
          </>
        }
      />

      <Panel title="Set up the client" className="mb-6" bodyClassName="p-5">
        {mustVerify ? (
          <p className="mb-5 rounded-md border border-sodium/60 bg-sodium-wash px-3.5 py-3 text-[13.5px] text-ink">
            <span className="font-semibold">Confirm your email address first.</span> Open the link we sent to {user.email}{" "}
            (or use “Send a new link” above). Until then the client can&apos;t log in and tunnels won&apos;t start.
          </p>
        ) : null}
        <InstallSteps dashboardUrl={cfg.dashboardUrl} staticUrl={staticUrl} />
      </Panel>

      <McpSection />

      <div className="mb-6 grid grid-cols-1 gap-6 xl:grid-cols-[minmax(0,1.2fr)_minmax(0,1fr)]">
        <Panel title="Useful flags" description="tund http <port | host:port | url> [flags]">
          <dl className="divide-y divide-line">
            {flags.map(([flag, desc]) => (
              <div key={flag} className="grid gap-1 px-4 py-2.5 sm:grid-cols-[13rem_minmax(0,1fr)] sm:gap-4">
                <dt className="font-mono text-[12.5px] text-ink">{flag}</dt>
                <dd className="text-[13px] text-ink-2">{desc}</dd>
              </div>
            ))}
          </dl>
          <div className="flex flex-col gap-2 border-t border-line p-4">
            <p className="text-[13px] text-ink-2">Other targets work too:</p>
            <Command>tund http 192.168.1.20:8080</Command>
            <Command>tund http https://localhost:8443</Command>
          </div>
        </Panel>

        <Panel title="Several tunnels at once" description="Describe them in the config file and start them together.">
          <div className="flex flex-col gap-3 p-4">
            <pre className="overflow-x-auto scroll-thin rounded-md border border-line bg-surface-2 p-3 font-mono text-[12px] leading-5 text-ink">
              {yaml}
            </pre>
            <Command>tund start --all</Command>
            <p className="text-[12.5px] text-muted">
              Or start some of them by name: <code className="font-mono text-ink-2">tund start web api</code>. Find the
              file with <code className="font-mono text-ink-2">tund config path</code>.
            </p>
          </div>
        </Panel>
      </div>

      <div className="mb-6 grid grid-cols-1 gap-6 xl:grid-cols-2">
        <Panel
          id="tcp"
          title="TCP tunnels"
          description="For SSH, databases, game servers and anything else that isn't HTTP."
          bodyClassName="flex flex-col gap-3 p-4 text-[13px] text-ink-2"
        >
          {tcp ? (
            <>
              <p>Expose a local port on a public TCP port of {tcp.host}:</p>
              <Command>tund tcp 22</Command>
              <p>
                tund prints an address like <code className="font-mono text-[12.5px] text-ink">tcp://{tcp.host}:{tcp.from}</code>.
                Connect with the usual tools:
              </p>
              <Command>{`ssh -p ${tcp.from} you@${tcp.host}`}</Command>
              <Command>tund tcp 5432 --pin</Command>
              <Command>{`psql "host=${tcp.host} port=<the port tund printed> user=postgres"`}</Command>
              <p>
                <code className="font-mono text-[12.5px] text-ink">--pin</code> keeps the port for next time (it becomes a
                static TCP port under Domains); <code className="font-mono text-[12.5px] text-ink">--remote-port N</code> asks
                for a specific one in {tcp.from}–{tcp.to}. Password and single sign-on don&apos;t apply to raw TCP, so use{" "}
                <code className="font-mono text-[12.5px] text-ink">--allow-ip</code> to limit who can connect.
              </p>
            </>
          ) : (
            <p>TCP tunnels aren&apos;t enabled on this server. An administrator can turn them on with TUND_TCP_PORTS.</p>
          )}
        </Panel>
        <Panel
          id="tls"
          title="TLS passthrough"
          description="The edge routes by hostname and never decrypts the traffic."
          bodyClassName="flex flex-col gap-3 p-4 text-[13px] text-ink-2"
        >
          <p>Forward TLS connections to a local service that has its own certificate:</p>
          <Command>tund tls 8443 --domain secure.example.com</Command>
          <p>
            Visitors see your service&apos;s certificate, so this fits best on a verified custom domain with a certificate
            you own. On a {cfg.baseDomain} hostname, let the client terminate TLS instead:
          </p>
          <Command>tund tls 8080 --terminate-cert cert.pem --terminate-key key.pem</Command>
          <p>
            The client decrypts with that certificate and forwards plain traffic to your local port. Hostname flags work as
            for HTTP (<code className="font-mono text-[12.5px] text-ink">--subdomain</code>,{" "}
            <code className="font-mono text-[12.5px] text-ink">--pin</code>,{" "}
            <code className="font-mono text-[12.5px] text-ink">--random</code>), and{" "}
            <code className="font-mono text-[12.5px] text-ink">--allow-ip</code> limits who can connect. Connections show
            up under Inspect.
          </p>
        </Panel>
      </div>

      <div className="mb-6 grid grid-cols-1 gap-6 xl:grid-cols-[minmax(0,1fr)_minmax(0,1.2fr)]">
        <Panel
          id="teams"
          title="Teams"
          description="Share identity providers and domains with other accounts."
          bodyClassName="flex flex-col gap-3 p-4 text-[13px] text-ink-2"
        >
          <p>
            Every member of a{" "}
            <Link href="/teams" className="font-medium text-ink underline underline-offset-4">
              team
            </Link>{" "}
            can run tunnels on the team&apos;s static hostnames and custom domains, protect them with the team&apos;s
            identity providers, and see and replay the traffic on them in the inspector. Owners and admins manage
            members, invites, providers and domains.
          </p>
          <Command>tund http 3000 --subdomain acme-staging --oidc acme/google</Command>
          <p>
            <code className="font-mono text-[12.5px] text-ink">--oidc acme/google</code> picks the provider{" "}
            <span className="font-mono text-[12.5px]">google</span> of the team{" "}
            <span className="font-mono text-[12.5px]">acme</span>. A bare slug first looks at your own providers, then at
            your teams&apos; when only one matches.
          </p>
        </Panel>
        <Panel
          id="identity-headers"
          title="Identity headers"
          description="After a visitor passes single sign-on (or a password), tund tells your app who it is."
          bodyClassName="flex flex-col gap-3 p-4 text-[13px] text-ink-2"
        >
          <IdentityHeaderTable />
          <pre className="overflow-x-auto scroll-thin rounded-md border border-line bg-surface-2 p-3 font-mono text-[12px] leading-5 text-ink">
            {IDENTITY_EXAMPLE}
          </pre>
          <p>
            tund removes every <code className="font-mono text-[12.5px] text-ink">X-Tund-*</code> header a visitor sends,
            on every tunnel, so these can&apos;t be spoofed on requests that arrive through tund. Claims the provider
            doesn&apos;t send are left out; request the <code className="font-mono text-[12.5px] text-ink">profile</code>{" "}
            scope for names and usually a <code className="font-mono text-[12.5px] text-ink">groups</code> scope or claim
            mapping for groups.
          </p>
        </Panel>
      </div>

      <ApiSection dashboardUrl={cfg.dashboardUrl} />

      {(await getSettings()).browser_warning ? (
        <Panel
          id="browser-warning"
          title="Browser warning"
          description={
            <>
              To protect visitors from phishing, people opening a tunnel on{" "}
              <span className="font-mono text-[12.5px] text-ink-2">*.{cfg.baseDomain}</span> in a browser see a one-time
              warning page first.
            </>
          }
          className="mb-6"
          bodyClassName="flex flex-col gap-3 p-4 text-[13px] text-ink-2"
        >
          <p>For fetch(), API clients and automated tests, skip it with a request header:</p>
          <Command>{`curl -H "Tund-Skip-Browser-Warning: 1" ${cfg.scheme}://my-app.${cfg.baseDomain}${cfg.portSuffix}/`}</Command>
          <p>
            Custom domains, tunnels protected with a password or single sign-on, and trusted accounts never show it.
          </p>
        </Panel>
      ) : null}

      <Panel title="Download manually" description="Single static binaries. Put it anywhere on your PATH.">
        <div className="grid gap-px bg-line sm:grid-cols-3">
          {PLATFORMS.map((p) => (
            <div key={p.os} className="bg-surface p-4">
              <p className="mb-2 text-[14px] font-medium text-ink">{p.label}</p>
              <ul className="flex flex-col gap-1">
                {p.arches.map((a) => {
                  const file = `tund-${p.os}-${a}${p.os === "windows" ? ".exe" : ""}`;
                  return (
                    <li key={a}>
                      <a
                        href={`${cfg.dashboardUrl}/_tund/downloads/${file}`}
                        className="inline-flex items-baseline gap-2 text-[13px] text-ink hover:underline"
                        download
                      >
                        <span className="font-mono text-[12.5px]">{file}</span>
                        <span className="text-muted">{ARCH_LABEL[a]}</span>
                      </a>
                    </li>
                  );
                })}
              </ul>
            </div>
          ))}
        </div>
        <div className="border-t border-line p-4">
          <p className="mb-2 text-[13px] text-ink-2">
            Binaries downloaded here already connect to {cfg.dashboardHost}. Only if you self-host and use a client from
            somewhere else, point it at your server once:
          </p>
          <Command>{`tund config set-server ${cfg.dashboardUrl}`}</Command>
        </div>
      </Panel>
    </>
  );
}

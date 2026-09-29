import Link from "next/link";
import { Panel } from "@/components/ui";
import { Command, CopyButton } from "@/components/client-ui";

// Tool names are stable API (docs/SPEC.md "tund mcp"); agents and prompts refer to them.
const TOOLS: [string, string][] = [
  ["whoami", "Shows the server, the account, its static hostnames and limits."],
  ["login", "Starts the browser login and gives you the code to confirm, if the CLI isn't logged in yet."],
  ["start_tunnel", "Exposes a local port or URL and returns the public URL and the inspector link."],
  ["list_tunnels", "Lists the tunnels this session started, or all online tunnels of the account."],
  ["stop_tunnel", "Stops a tunnel by id or URL."],
  ["list_requests", "Lists captured requests, newest first, filtered by tunnel, method, status or path."],
  ["get_request", "Returns one request with headers and decoded bodies."],
  ["replay_request", "Sends a captured request again, optionally changed, and returns the new capture."],
];

function CodeBlock({ children }: { children: string }) {
  return (
    <div className="relative rounded-md border border-line bg-surface-2">
      <pre className="overflow-x-auto scroll-thin p-3 pr-10 font-mono text-[12.5px] leading-5 text-ink">{children}</pre>
      <div className="absolute right-1.5 top-1.5">
        <CopyButton value={children} />
      </div>
    </div>
  );
}

export function McpSection() {
  const json = `{
  "mcpServers": {
    "tund": { "command": "tund", "args": ["mcp"] }
  }
}`;
  return (
    <Panel
      id="mcp"
      title="AI agents (MCP)"
      description="tund mcp lets coding agents such as Claude Code, Cursor or Codex share what they build: they start a tunnel, hand you the URL and read the captured requests while they debug."
      className="mb-6"
    >
      <div className="grid grid-cols-1 gap-6 p-5 xl:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]">
        <div className="flex flex-col gap-5">
          <div>
            <p className="mb-2 text-[14px] font-medium text-ink">Claude Code</p>
            <Command>claude mcp add tund -- tund mcp</Command>
          </div>
          <div>
            <p className="mb-2 text-[14px] font-medium text-ink">Other MCP clients</p>
            <p className="mb-2 text-[12.5px] text-muted">
              Cursor, Windsurf, VS Code, Codex and most other clients take a server entry of this shape in their MCP
              settings:
            </p>
            <CodeBlock>{json}</CodeBlock>
          </div>
          <p className="text-[12.5px] text-muted">
            The MCP server uses the same login as your terminal. If you haven&apos;t run tund yet, the agent&apos;s{" "}
            <code className="font-mono text-ink-2">login</code> tool shows you a code to confirm in the browser. Tunnels
            it starts stop when the agent session ends.
          </p>
          <div>
            <p className="mb-2 text-[14px] font-medium text-ink">Keep it on a leash</p>
            <dl className="flex flex-col gap-2 text-[13px]">
              <div>
                <dt className="font-mono text-[12.5px] text-ink">--allow-ports 3000,5173</dt>
                <dd className="text-ink-2">Only these local ports may be exposed.</dd>
              </div>
              <div>
                <dt className="font-mono text-[12.5px] text-ink">--require-password</dt>
                <dd className="text-ink-2">Every tunnel the agent starts gets a random password unless it sets one.</dd>
              </div>
              <div>
                <dt className="font-mono text-[12.5px] text-ink">--no-browser</dt>
                <dd className="text-ink-2">Don&apos;t open the browser for login; the code is only returned to the agent.</dd>
              </div>
            </dl>
            <Command className="mt-3">claude mcp add tund -- tund mcp --allow-ports 3000,5173 --require-password</Command>
          </div>
        </div>

        <div className="flex flex-col gap-5">
          <div>
            <p className="mb-2 text-[14px] font-medium text-ink">Try it</p>
            <blockquote className="rounded-md border-l-2 border-sodium bg-surface-2 px-3.5 py-2.5 text-[14px] text-ink">
              Start the dev server and share it with me via tund.
            </blockquote>
            <p className="mt-2 text-[12.5px] text-muted">
              The agent starts your app, calls <code className="font-mono text-ink-2">start_tunnel</code> and replies with
              the public URL. Ask it to check the requests afterwards and it reads them from the inspector.
            </p>
          </div>
          <div>
            <p className="mb-2 text-[14px] font-medium text-ink">Tools</p>
            <dl className="divide-y divide-line rounded-md border border-line">
              {TOOLS.map(([name, desc]) => (
                <div key={name} className="grid gap-0.5 px-3 py-2 sm:grid-cols-[9rem_minmax(0,1fr)] sm:gap-3">
                  <dt className="font-mono text-[12.5px] text-ink">{name}</dt>
                  <dd className="text-[13px] text-ink-2">{desc}</dd>
                </div>
              ))}
            </dl>
          </div>
        </div>
      </div>
    </Panel>
  );
}

const ENDPOINTS: [string, string, string][] = [
  ["GET", "/me", "Account, server, limits and static hostnames."],
  ["GET", "/tunnels", "Online tunnels of the account."],
  ["POST", "/tunnels/{id}/stop", "Stops a tunnel."],
  ["GET", "/requests", "Captured requests, newest first. Filters: hostname, tunnel_id, method, status, path, limit, before."],
  ["GET", "/requests/{id}", "One request with headers and decoded bodies."],
  ["POST", "/requests/{id}/replay", "Replays a request, optionally with a changed method, path, headers or body."],
];

export function ApiSection({ dashboardUrl }: { dashboardUrl: string }) {
  const base = `${dashboardUrl}/_tund/api/v1`;
  return (
    <Panel
      id="api"
      title="HTTP API"
      description={
        <>
          Script against your tunnels with an{" "}
          <Link href="/authtokens" className="font-medium text-ink underline underline-offset-4">
            auth token
          </Link>
          , sent as <code className="font-mono text-[12.5px] text-ink-2">Authorization: Bearer &lt;token&gt;</code>. Everything
          is scoped to the token&apos;s account; errors come back as JSON with an error field.
        </>
      }
      className="mb-6"
    >
      <div className="border-b border-line px-4 py-2.5 font-mono text-[12.5px] text-ink-2">
        <span className="text-muted">Base URL </span>
        <span className="break-all text-ink">{base}</span>
      </div>
      <dl className="divide-y divide-line">
        {ENDPOINTS.map(([method, path, desc]) => (
          <div key={method + path} className="grid gap-1 px-4 py-2.5 sm:grid-cols-[16rem_minmax(0,1fr)] sm:gap-4">
            <dt className="font-mono text-[12.5px] text-ink">
              <span className="inline-block w-11 text-ink-2">{method}</span>
              {path}
            </dt>
            <dd className="text-[13px] text-ink-2">{desc}</dd>
          </div>
        ))}
      </dl>
      <div className="flex flex-col gap-2 border-t border-line p-4">
        <p className="text-[13px] text-ink-2">The five most recent failed requests:</p>
        <Command>{`curl -H "Authorization: Bearer $TUND_AUTHTOKEN" "${base}/requests?status=5xx&limit=5"`}</Command>
      </div>
    </Panel>
  );
}

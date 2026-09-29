// Dev-only: fills the database with fake sessions, tunnels and requests so the
// dashboard can be developed without a running tund-server.
//   node scripts/seed-dev.mjs <user-email>
import postgres from "postgres";
import zlib from "node:zlib";

const url = process.env.TUND_DATABASE_URL ?? "postgres://tund:tund@localhost:55432/tund";
const email = process.argv[2];
if (!email) {
  console.error("usage: node scripts/seed-dev.mjs <user-email>");
  process.exit(1);
}
const sql = postgres(url);
const base = process.env.TUND_BASE_DOMAIN ?? "localtest.me";
const [user] = await sql`select id from users where email = ${email}`;
if (!user) throw new Error(`no user ${email}`);
const uid = user.id;

const [token] = await sql`select id from authtokens where user_id = ${uid} limit 1`;
const [s1] = await sql`
  insert into agent_sessions (user_id, authtoken_id, client_version, client_os, hostname, remote_addr, connected_at)
  values (${uid}, ${token?.id ?? null}, '0.1.0', 'darwin/arm64', 'tills-macbook', '93.184.216.34:51234', now() - interval '2 hours 13 minutes')
  returning id`;
const [s2] = await sql`
  insert into agent_sessions (user_id, authtoken_id, client_version, client_os, hostname, remote_addr, connected_at, disconnected_at)
  values (${uid}, ${token?.id ?? null}, '0.1.0', 'linux/amd64', 'build-runner-02', '10.0.4.12:40022', now() - interval '9 hours', now() - interval '3 hours')
  returning id`;

const mk = async (session, name, hostname, local, auth, started, ended) => {
  const [t] = await sql`
    insert into tunnels (agent_session_id, user_id, name, hostname, public_url, local_addr, auth_mode, started_at, ended_at)
    values (${session}, ${uid}, ${name}, ${hostname}, ${"https://" + hostname}, ${local}, ${auth}, ${started}, ${ended})
    returning id`;
  return t.id;
};
const now = Date.now();
const t1 = await mk(s1.id, "web", `my-app.${base}`, "http://localhost:3000", "password", new Date(now - 2.2 * 3600e3), null);
const t2 = await mk(s1.id, "http-8080", `brave-otter-4821.${base}`, "http://localhost:8080", "none", new Date(now - 2.1 * 3600e3), null);
const t3 = await mk(s2.id, "webhooks", `quiet-heron-1123.${base}`, "http://127.0.0.1:4000", "oidc", new Date(now - 9 * 3600e3), new Date(now - 3 * 3600e3));

const json = (o) => Buffer.from(JSON.stringify(o));
const hdr = (o) => Object.fromEntries(Object.entries(o).map(([k, v]) => [k, [v]]));
const png = Buffer.from(
  "iVBORw0KGgoAAAANSUhEUgAAABAAAAAQCAYAAAAf8/9hAAAAPElEQVR4nGNgGAWjYBSMglEwCkbBKBgFo2AUDEYAAAQQAAG7yv4hAAAAAElFTkSuQmCC",
  "base64",
);

const paths = [
  ["GET", "/", 200, "text/html; charset=utf-8"],
  ["GET", "/api/users?page=2&limit=20", 200, "application/json"],
  ["POST", "/api/users", 201, "application/json"],
  ["GET", "/assets/logo.png", 200, "image/png"],
  ["POST", "/webhooks/stripe", 200, "application/json"],
  ["GET", "/favicon.ico", 404, "text/plain"],
  ["PATCH", "/api/users/42", 422, "application/json"],
  ["GET", "/api/orders", 500, "application/json"],
  ["GET", "/login", 302, "text/html"],
  ["DELETE", "/api/session", 204, ""],
  ["POST", "/login", 303, "text/html"],
];

const rows = [];
const hosts = [
  [t1, `my-app.${base}`],
  [t2, `brave-otter-4821.${base}`],
  [t3, `quiet-heron-1123.${base}`],
];
for (let i = 0; i < 420; i++) {
  const ageH = Math.pow(Math.random(), 1.6) * 23.9;
  const [tid, host] = hosts[i % 7 === 0 ? 2 : i % 3 === 0 ? 1 : 0];
  const [method, path, status0, ct] = paths[Math.floor(Math.random() * paths.length)];
  const status = Math.random() < 0.015 ? 0 : status0;
  const duration = Math.max(0.4, Math.exp(Math.random() * 5.2) * (status === 500 ? 4 : 1));
  let respBody = null;
  let respHeaders = { "Content-Type": ct, Date: new Date().toUTCString() };
  if (ct.includes("json")) {
    const payload =
      status >= 400
        ? { error: status === 422 ? "validation_failed" : "internal_error", fields: status === 422 ? { email: "must be unique" } : undefined }
        : { data: Array.from({ length: 3 }, (_, k) => ({ id: 40 + k, email: `user${k}@example.com`, active: k % 2 === 0, score: 3.14 * k })), page: 2, next: null };
    respBody = zlib.gzipSync(json(payload));
    respHeaders["Content-Encoding"] = "gzip";
  } else if (ct.startsWith("image/")) {
    respBody = png;
  } else if (ct.startsWith("text/html")) {
    respBody = Buffer.from(`<!doctype html><html><head><title>My app</title></head><body><h1>Hello</h1></body></html>`);
  } else if (ct.startsWith("text/plain")) {
    respBody = Buffer.from("404 page not found\n");
  }
  if (status === 302 || status === 303) respHeaders.Location = "/dashboard";
  let reqBody = null;
  const reqHeaders = {
    Accept: "*/*",
    "User-Agent": i % 2 ? "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/140.0 Safari/537.36" : "curl/8.7.1",
    "X-Forwarded-For": "203.0.113.7",
  };
  if (method === "POST" && path === "/login") {
    reqBody = Buffer.from("email=ada%40example.com&password=hunter2&remember=on");
    reqHeaders["Content-Type"] = "application/x-www-form-urlencoded";
  } else if (method === "POST" || method === "PATCH") {
    reqBody = json({ email: "ada@example.com", name: "Ada Lovelace", roles: ["admin", "ops"], meta: { source: "signup-form", beta: true } });
    reqHeaders["Content-Type"] = "application/json";
    reqHeaders.Authorization = "Bearer eyJhbGciOiJIUzI1NiJ9.demo";
  }
  rows.push({
    tunnel_id: tid,
    user_id: uid,
    hostname: host,
    method,
    path,
    proto: "HTTP/1.1",
    remote_addr: `203.0.113.${(i % 40) + 2}`,
    req_headers: reqHeaders,
    req_body: reqBody,
    req_body_size: reqBody?.length ?? 0,
    req_body_truncated: false,
    status,
    resp_headers: status === 0 ? {} : hdr(respHeaders),
    resp_body: status === 0 || status === 204 ? null : respBody,
    resp_body_size: status === 0 || status === 204 ? 0 : (respBody?.length ?? 0),
    resp_body_truncated: false,
    ttfb_ms: duration * 0.7,
    duration_ms: duration,
    error: status === 0 ? "dial tcp 127.0.0.1:3000: connect: connection refused" : "",
    started_at: new Date(now - ageH * 3600e3),
  });
}
// A few special cases right now.
const big = Buffer.alloc(262144, "x");
rows.push({
  ...rows[0], tunnel_id: t2, hostname: `brave-otter-4821.${base}`, method: "PUT", path: "/upload/big.bin",
  req_headers: hdr({ "Content-Type": "application/octet-stream" }), req_body: Buffer.concat([Buffer.from([0, 1, 2, 3, 255, 254]), big.subarray(0, 4000)]),
  req_body_size: 5_242_880, req_body_truncated: true, status: 200, resp_headers: hdr({ "Content-Type": "application/json" }),
  resp_body: json({ ok: true, stored: "big.bin" }), resp_body_size: 29, started_at: new Date(now - 60e3),
});
for (const r of rows) {
  r.req_headers = Object.fromEntries(Object.entries(r.req_headers).map(([k, v]) => [k, Array.isArray(v) ? v : [v]]));
}
for (let i = 0; i < rows.length; i += 100) {
  await sql`insert into requests ${sql(rows.slice(i, i + 100), Object.keys(rows[0]))}`;
}
console.log(`seeded ${rows.length} requests, tunnels ${t1} ${t2} ${t3}`);
await sql.end();

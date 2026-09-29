-- tund initial schema. Owned by tund-server (applied on startup); the dashboard
-- reads and writes the same tables but never migrates.

create table users (
  id            uuid primary key default gen_random_uuid(),
  email         text not null unique,            -- always lowercase
  name          text not null default '',
  password_hash text not null,                   -- scrypt$N$r$p$salt$hash (see docs/SPEC.md)
  is_admin      boolean not null default false,
  created_at    timestamptz not null default now()
);

-- Dashboard login sessions. id = sha256 hex of the cookie token.
create table sessions (
  id         text primary key,
  user_id    uuid not null references users(id) on delete cascade,
  expires_at timestamptz not null,
  user_agent text not null default '',
  ip         text not null default '',
  created_at timestamptz not null default now()
);
create index sessions_user_idx on sessions(user_id);

-- Tokens used by the tund client. Only the sha256 hash is stored.
create table authtokens (
  id           uuid primary key default gen_random_uuid(),
  user_id      uuid not null references users(id) on delete cascade,
  name         text not null,
  token_hash   text not null unique,
  token_prefix text not null,                    -- e.g. "tund_3f9a1c2b" for display
  created_at   timestamptz not null default now(),
  last_used_at timestamptz
);
create index authtokens_user_idx on authtokens(user_id);

-- OIDC identity providers a user can protect endpoints with.
create table oidc_providers (
  id            uuid primary key default gen_random_uuid(),
  user_id       uuid not null references users(id) on delete cascade,
  name          text not null,
  slug          text not null,                   -- referenced from the client: --oidc <slug>
  issuer        text not null,
  client_id     text not null,
  client_secret text not null default '',
  scopes        text not null default 'openid email profile',
  created_at    timestamptz not null default now(),
  unique (user_id, slug)
);

-- Reserved hostnames: subdomains of the base domain or custom domains
-- (optionally wildcard, stored as "*.dev.example.com").
create table domains (
  id                   uuid primary key default gen_random_uuid(),
  user_id              uuid not null references users(id) on delete cascade,
  hostname             text not null unique,     -- lowercase FQDN
  kind                 text not null check (kind in ('subdomain', 'custom')),
  verification_token   text not null default '',
  verified_at          timestamptz,              -- subdomains are verified on creation
  auth_mode            text not null default 'none' check (auth_mode in ('none', 'password', 'oidc')),
  auth_password_hash   text,
  auth_oidc_provider_id uuid references oidc_providers(id) on delete set null,
  auth_oidc_allow      text[] not null default '{}',  -- "a@b.com" or "@b.com"; empty = anyone who can log in
  created_at           timestamptz not null default now()
);
create index domains_user_idx on domains(user_id);

-- One row per connected client process.
create table agent_sessions (
  id              uuid primary key default gen_random_uuid(),
  user_id         uuid not null references users(id) on delete cascade,
  authtoken_id    uuid references authtokens(id) on delete set null,
  client_version  text not null default '',
  client_os       text not null default '',
  hostname        text not null default '',     -- machine hostname reported by the client
  remote_addr     text not null default '',
  connected_at    timestamptz not null default now(),
  disconnected_at timestamptz
);
create index agent_sessions_user_idx on agent_sessions(user_id, connected_at desc);

-- One row per bound tunnel (hostname -> local address) for the lifetime of a session.
create table tunnels (
  id               uuid primary key default gen_random_uuid(),
  agent_session_id uuid not null references agent_sessions(id) on delete cascade,
  user_id          uuid not null references users(id) on delete cascade,
  name             text not null,
  hostname         text not null,
  public_url       text not null,
  local_addr       text not null,
  auth_mode        text not null default 'none',
  started_at       timestamptz not null default now(),
  ended_at         timestamptz
);
create index tunnels_user_idx on tunnels(user_id, started_at desc);
create index tunnels_hostname_idx on tunnels(hostname, started_at desc);
create unique index tunnels_online_hostname_idx on tunnels(hostname) where ended_at is null;

-- Captured HTTP exchanges.
create table requests (
  id                  uuid primary key default gen_random_uuid(),
  tunnel_id           uuid references tunnels(id) on delete set null,
  user_id             uuid not null references users(id) on delete cascade,
  hostname            text not null,
  method              text not null,
  path                text not null,             -- path + raw query
  proto               text not null default '',
  remote_addr         text not null default '',
  req_headers         jsonb not null default '{}',   -- {"Content-Type": ["application/json"]}
  req_body            bytea,
  req_body_size       bigint not null default 0,
  req_body_truncated  boolean not null default false,
  status              integer not null default 0,    -- 0 = no response (see error)
  resp_headers        jsonb not null default '{}',
  resp_body           bytea,
  resp_body_size      bigint not null default 0,
  resp_body_truncated boolean not null default false,
  ttfb_ms             double precision not null default 0,
  duration_ms         double precision not null default 0,
  error               text not null default '',
  replay_of           uuid,
  started_at          timestamptz not null
);
create index requests_user_idx on requests(user_id, started_at desc);
create index requests_hostname_idx on requests(hostname, started_at desc);
create index requests_tunnel_idx on requests(tunnel_id, started_at desc);

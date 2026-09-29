-- TCP and TLS-passthrough tunnels, IP allow lists and connection logs.

alter table tunnels add column proto text not null default 'http' check (proto in ('http', 'tcp', 'tls'));
alter table tunnels add column remote_port integer;

-- Static TCP ports ("static addresses"), personal or team-owned.
create table tcp_reservations (
  port       integer primary key,
  user_id    uuid not null references users(id) on delete cascade,
  team_id    uuid references teams(id) on delete cascade,
  created_at timestamptz not null default now()
);
create index tcp_reservations_user_idx on tcp_reservations(user_id);

-- One row per finished TCP/TLS connection (the "requests" of raw tunnels).
create table connections (
  id          uuid primary key default gen_random_uuid(),
  tunnel_id   uuid references tunnels(id) on delete set null,
  user_id     uuid not null references users(id) on delete cascade,
  proto       text not null check (proto in ('tcp', 'tls')),
  address     text not null,                 -- "tund.io:20417" or the TLS hostname
  remote_addr text not null default '',
  bytes_in    bigint not null default 0,     -- visitor -> local service
  bytes_out   bigint not null default 0,     -- local service -> visitor
  duration_ms double precision not null default 0,
  error       text not null default '',
  started_at  timestamptz not null
);
create index connections_user_idx on connections(user_id, started_at desc);
create index connections_tunnel_idx on connections(tunnel_id, started_at desc);
create index connections_address_idx on connections(address, started_at desc);

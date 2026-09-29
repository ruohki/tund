-- Edge nodes: several tund-server processes sharing this database.
create table nodes (
  name              text primary key,
  role              text not null default 'control' check (role in ('control', 'edge')),
  region            text not null default '',
  relay_url         text not null default '',   -- host:port other nodes dial
  relay_cert_sha256 text not null default '',   -- pinned by callers
  version           text not null default '',
  started_at        timestamptz not null default now(),
  last_seen         timestamptz not null default now()
);
alter table tunnels add column node text not null default '';
alter table agent_sessions add column node text not null default '';
create unique index tunnels_online_tcp_port_idx on tunnels(remote_port) where ended_at is null and proto = 'tcp';

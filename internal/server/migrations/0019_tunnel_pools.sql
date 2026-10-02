-- Load-balanced tunnels: several online rows may share a hostname when they
-- are pool members of one account with the same pool_key (a keyed hash of
-- the visitor-facing settings). Single tunnels keep pool_key = ''.
alter table tunnels add column pool_key text not null default '';
drop index tunnels_online_hostname_idx;
create unique index tunnels_online_hostname_idx on tunnels(hostname) where ended_at is null and pool_key = '';
create index tunnels_online_pool_idx on tunnels(hostname) where ended_at is null and pool_key <> '';

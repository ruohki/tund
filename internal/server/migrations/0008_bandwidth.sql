-- Bandwidth: per-account throughput caps and monthly transfer quotas.

-- Per-user overrides of the instance defaults (settings limit_bandwidth_kbps /
-- limit_transfer_gb). NULL = use the default; 0 = unlimited.
alter table users add column bandwidth_kbps integer check (bandwidth_kbps >= 0);
alter table users add column transfer_quota_gb integer check (transfer_quota_gb >= 0);

-- Transfer usage per account and UTC day, written by every node (upserts).
create table usage_daily (
  user_id     uuid not null references users(id) on delete cascade,
  day         date not null,
  bytes_in    bigint not null default 0,   -- visitor -> local service
  bytes_out   bigint not null default 0,   -- local service -> visitor
  requests    bigint not null default 0,
  connections bigint not null default 0,
  primary key (user_id, day)
);

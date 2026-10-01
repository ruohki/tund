-- Host metrics every node reports with its heartbeat (admin overview, DNS
-- balancing). Rates are bytes per second; NULL = not measured (non-Linux
-- host, or the first heartbeat after start).
alter table nodes
  add column public_ip     text not null default '',
  add column capacity_mbps integer not null default 0,  -- uplink, 0 = unknown
  add column cpus          integer not null default 0,
  add column cpu_pct       real,
  add column load1         real,
  add column mem_total     bigint,
  add column mem_used      bigint,
  add column net_in_rate   bigint,
  add column net_out_rate  bigint,
  add column tunnel_rate   bigint,                       -- tunnel traffic, both directions
  add column sessions      integer not null default 0,
  add column tunnels       integer not null default 0,
  add column metrics_at    timestamptz;

-- Certificate storage shared by all nodes (TUND_CERT_STORAGE=postgres):
-- certificates, keys, ACME accounts and challenge data, plus locks.
create table certmagic_data (
  key      text primary key,
  value    bytea not null,
  modified timestamptz not null default now()
);
create table certmagic_locks (
  name       text primary key,
  owner      text not null,
  expires_at timestamptz not null
);

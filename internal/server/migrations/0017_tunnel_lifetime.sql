-- Maximum tunnel lifetime per account (minutes): NULL = the instance setting
-- limit_tunnel_lifetime (admins: unlimited), 0 = unlimited.
alter table users add column tunnel_lifetime_minutes integer;

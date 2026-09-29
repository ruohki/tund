-- Trusted accounts skip the browser warning page on the base domain.
alter table users add column trusted boolean not null default false;

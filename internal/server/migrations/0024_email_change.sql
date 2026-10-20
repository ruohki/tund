-- Email links remember the address they went to (docs/SPEC.md "Email flows"):
-- confirmation and reset links stop working once the account's address
-- changes, a reset vouches only for the address it was sent to, and a new
-- address takes effect when it opens its link (kind 'email_change'). Older
-- links keep '' and vouch for nothing.
alter table email_tokens add column email text not null default '';
alter table email_tokens drop constraint email_tokens_kind_check;
alter table email_tokens add constraint email_tokens_kind_check check (kind in ('verify', 'reset', 'email_change'));

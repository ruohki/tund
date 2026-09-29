-- `tund login` with a loopback callback: the CLI listens on 127.0.0.1 and the
-- dashboard hands the approval back to it by redirecting the browser there, so
-- the user doesn't have to compare codes. Approving such a request only marks
-- it 'authorized' and stores the hash of a one-time callback code; the token is
-- created when the CLI presents that code. A login link sent to someone else
-- therefore can't be approved on the sender's behalf: the code lands on the
-- approver's own machine.
alter table device_codes
  add column callback_port      integer check (callback_port between 1024 and 65535),
  add column callback_state     text,
  add column callback_code_hash text;
alter table device_codes drop constraint device_codes_status_check;
alter table device_codes add constraint device_codes_status_check
  check (status in ('pending', 'authorized', 'approved', 'denied'));

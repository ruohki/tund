---
name: tund-share
description: Share something from this machine through a protected public HTTPS link using the tund CLI. A website or app you are building (dev server, preview build, any localhost port) goes through `tund http`; a file or folder (design files, exports, build artifacts, a PDF) goes through `tund serve`. Use this whenever the user wants to share, send, show or expose local work to someone else, asks for a link, preview URL or tunnel, or wants to open it on another device, even if they never mention tund. Every share is put behind a password or single sign-on, and the link plus how to get in are reported back to the user.
---

# Share local work with tund

tund puts something on this machine behind a public HTTPS URL for as long as a `tund` process is running. Use it to hand the user, or whoever they name, a link to what you built. It is a way to show work in progress, not a deployment.

## Pick the command

| The visitor should… | Command |
| --- | --- |
| use a running web app: a dev server, a preview server, anything listening on a local port | `tund http <port>` |
| browse and download files: design files, an export, a build artifact, a single PDF | `tund serve <file or folder>` |

`tund serve` is a file browser, not a web server. Visitors open folders, preview images, PDFs, audio, video and text, and download; HTML files download instead of rendering. So a site that only exists as files (`dist/`, `out/`, a folder of HTML mockups to click through) needs a local web server in front of it and then `tund http`. Bind that server to localhost, for example `python3 -m http.server 8000 --bind 127.0.0.1 -d dist`: many static servers listen on every network interface by default, which would put the unprotected site on the local network. It is a second background process to stop afterwards.

## Check the CLI first

```sh
tund config show
```

* Command not found: tund is not installed. Say so and ask before installing it (`curl -fsSL https://tund.io/install.sh | sh`; a self-hosted instance serves the same path on its own dashboard host).
* `Authtoken (not set …)`: not logged in. Logging in is approved in the user's browser, so ask them to run `tund login` in their terminal, then continue.
* `Tunnels`: tunnels the user has defined, with how each one is protected. The next step uses this.

Use `tund config show` rather than reading the config file: the file holds the account's authtoken, and `show` masks it.

## Decide the protection before you start

The URL is reachable from the whole internet the moment the tunnel is online, and what you are sharing is usually unreleased work. So every share gets a password or single sign-on (OIDC), chosen before starting, in this order:

1. **The user said how.** A password to use, a sign-in provider, who may see it, or an explicit "make it public": do that. If they say their hostname is already protected in the dashboard, run `tund http` without `--password` or `--oidc` (the flags would replace the stored policy) and confirm the result in the next step.
2. **Single sign-on is already set up for this.**
   * `tund config show` lists a tunnel for the same port with `oidc …` or `password`: run `tund start <name> --log`. It uses the hostname and protection the user configured.
   * A provider is named by the user, by the project's agent instructions, or by other tunnels in the config: pass `--oidc <provider> --oidc-allow <who>`. `<provider>` is `<name>` or `<team>/<name>`; `<who>` is emails (`ana@acme.com`), email domains (`@acme.com`) or groups (`group:design`), comma separated. The allow list is what decides who gets in. If you don't know it, ask instead of guessing: with a public provider such as Google, an open list admits anyone who has an account there. Providers are created in the dashboard and no command lists them, so don't invent a name.
3. **Otherwise use a password.** Generate it from a random source; a password you make up yourself is guessable.

   ```sh
   openssl rand -hex 8
   ```

## Start it and wait for the link

tund runs until it is stopped, and the share has to outlive your turn. Start it as a background process that keeps running after you reply: your harness's background-command feature if its processes survive, otherwise `nohup … > <log file> 2>&1 &`. Note the PID. Send the output to a log file of its own in a scratch or temp directory, outside the repository and outside the folder being shared. `--log` prints plain lines that are easy to read back:

```sh
tund http 3000 --password <password> --log              # the app on localhost:3000, on the account's static URL
tund http 3000 --random --password <password> --log     # the same on a one-off URL
tund serve ./designs --password <password> --log        # a folder; a single file works the same way
tund http 3000 --oidc google --oidc-allow @acme.com --log
tund serve ./designs --oidc acme/okta --oidc-allow group:design --log
```

`tund http` uses the account's static URL, the same one every run, which suits a link that should keep working across sessions. Pass `--random` when the user wants a throwaway link. `tund serve` always gets a hostname of its own and keeps it for that path.

Usually within a second or two the log shows the tunnel coming online, or why it did not:

```
2026-10-14T00:44:00+02:00 session online account=ana@acme.com server_version=0.8.0
2026-10-14T00:44:01+02:00 tunnel online name=serve-designs url=https://quiet-heron-2817.tund.io local=~/work/designs auth=password inspect="https://tund.io/inspect?host=quiet-heron-2817.tund.io"

2026-10-14T00:44:01+02:00 tunnel failed name=http-3000 local=http://localhost:3000 error="…"
tund: …                      (the process exited; the message says what to fix)
```

If neither shows up after about 20 seconds, treat it as failed, stop the process and read the log.

* `url=` is the link.
* `auth=` is how the link is really protected: `password` or `oidc`. Report from this field, not from the flags you passed. The user's team can enforce single sign-on, which replaces a password, and a `warning=` field then says so. **No `auth=` means the link is public**: stop the process right away unless the user asked for a public link.
* `expires_at=` appears when the server closes tunnels after a maximum lifetime. Mention the time, or start with `--restart-on-expiry`.

A password share can be checked from the shell; OIDC links only work in a browser:

```sh
curl -s -o /dev/null -w '%{http_code}\n' -u ':<password>' <url>/    # 200 with the password, 401 without
```

Things that come up:

* **Nothing listening.** `tund http` comes online even when the app is not running, and visitors then get an error page. Check `curl -sI http://localhost:<port>` first.
* **"Invalid Host header", "host is not allowed", a blank page.** Some dev servers (Vite, webpack) reject the public hostname. Restart the tunnel with `--host-header rewrite` so the app sees `Host: localhost:<port>`.
* **"… is already in use by another tunnel".** Another tunnel of the user's holds the static URL. Add `--random`.
* **What is in the folder.** List it (`ls -A`) before sharing. tund leaves dotfiles and the secrets it recognizes (`.env`, keys, credentials) out of what visitors see, so a stray `.env` is no reason to copy the folder: share it as it is and say what stays hidden. Everything else in it is visible. If it holds more than the user means to show, share a narrower folder or ask. A cleaned-up copy also works, but it is a snapshot that no longer follows the original, so say that.
* **A folder is refused.** `tund serve` won't share the home folder, the disk root or system folders. Pick the folder that holds just the files.
* **Uploads.** Visitors can only look and download. `--upload` (folders only) lets them add files; pass it only when the user asked to receive files.

## Tell the user how to get in

Once the tunnel is online, report:

* the link and what is behind it (which port or folder);
* how to get in: the password itself, since the user has to pass it on and it is the only way in, or the sign-in provider and who is allowed. Password visitors get a page that asks for the password; there is no user name;
* that the link works only while the share is running and this machine is awake and online, and how it gets stopped.

For example:

> Your design files are shared at https://quiet-heron-2817.tund.io
> Password: `3f9c2a7be01d4c56`
> Visitors can browse and download, not change anything. The link works while this machine is on and until the share is stopped: tell me when you're done, or run `kill 48213`.

If the tunnel came from `tund start` with a password stored in the config, you don't know that password: say the link is behind the password from their tund config.

## While it is up, and afterwards

* Leave the process running after you report; it is the share. Keep working as usual: with `tund http`, visitors see the local app as it changes, and with `tund serve` they see the folder as it is now.
* The log gets a `request method=GET path=/ status=200 …` line for every request that got past the password or sign-in, so it shows whether someone opened the link and what failed for them. Rejected attempts are not listed. The `inspect=` URL is the dashboard page with the full requests and responses.
* If you have to restart a share, reuse its password so the link the user already passed on keeps working.
* Stop it with `kill <pid>` when the user is done or asks, together with any local web server you started for it. The URL stops working immediately. `tund status` lists everything the account still has online, on every machine.
* Don't write the password into the repository, and use a new one for the next share.

## With the tund MCP server

If this session has tund's MCP tools (`start_tunnel`, `list_requests`, …), `start_tunnel` can replace `tund http`: it waits for the tunnel, returns the URL and takes the same `password` and `oidc` options, so everything above about protection and reporting still applies. Files and folders always go through `tund serve`.

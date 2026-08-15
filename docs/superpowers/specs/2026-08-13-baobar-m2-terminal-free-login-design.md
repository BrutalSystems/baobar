# Baobar M2 — terminal-free login and autostart

**Status:** implemented and unit-tested on branch `m2-terminal-free-login`; not yet
exercised against a live server or a real browser (see the README's Status section for
exactly what that does and doesn't cover).
**Date:** 2026-08-13
**Supersedes:** the M2/M3 split in `2026-08-13-baobar-design.md`. That document ordered
userpass+TOTP before OIDC; this one does both in one milestone and reverses the emphasis.

M1 shipped an indicator whose login menu items shell out to a terminal. This milestone
removes the terminal entirely and adds "Start at login".

---

## Why

Two problems, one of which is embarrassing for a GUI app.

**A terminal window appears when you click Login.** The shell prototype had to do this — a
SwiftBar plugin cannot host a long-running interactive process, so it handed off to
Terminal.app. That mechanism was ported into a Go app that has no such limitation and was
never re-examined. Clicking a menu item in a tray app should not spawn a console.

**M1 login requires the `bao` CLI.** The spec's Decision 2 argues Baobar exists because
Windows developers are the least likely to have it installed — then M1's login shells out
to it anyway. M1 therefore gives a Windows user a correct indicator and a login button
that cannot work. This milestone closes that gap: after M2 Baobar has **no dependency on
the `bao` binary at all**, for status or for login.

## Decision: implement the flows in-process

The auth flows are implemented against OpenBao's HTTP API. Baobar opens a browser and
runs a short-lived local listener; the `bao` CLI is never invoked.

**The rejected alternative, recorded because it is genuinely simpler.** `bao login` does
not need a terminal — that was the bug, not a constraint. Running
`exec.Command("bao", "login", "-method=oidc", ...)` with no console wrapper works: the CLI
opens the browser itself and runs its own listener on :8250. For userpass, `password=-`
reads from stdin, so credentials can be piped rather than passed in argv. That version is
perhaps a tenth of the code in this document.

It was rejected on one criterion: **it does not work for a user without the `bao` CLI**,
which is the population this whole product exists for. Choosing it would mean M2 fixes the
terminal popup for people who already have working logins, and does nothing for the people
Decision 2 names. If CLI-less machines ever stop mattering, this decision should be
revisited — the interface below would not change, only its internals.

## Scope

1. In-process OIDC login.
2. In-process userpass + TOTP login.
3. "Start at login" as a checkable menu item.
4. Deletion of the terminal shell-out and everything that supported it.

Not in scope: token renewal, multiple servers, autostart on Linux distributions without
XDG autostart, refined icon artwork.

---

## Architecture

```
internal/authflow/          NEW — everything about acquiring a token
  session.go                 one-shot loopback listener: bind, serve, shut down, time out
  oidc.go                    OIDC: auth_url -> browser -> callback -> code exchange
  userpass.go                userpass login -> two-phase MFA validate
  browser.go                 serves the userpass form and drives UserpassBrowser end to end
  login.html                 the embedded HTML form (go:embed, no external assets)
internal/autostart/         NEW — one interface, per-platform implementations
  autostart.go              Enabled/Enable/Disable + the shared file-backed implementation
  render.go                  renders the plist / .desktop file contents
  autostart_darwin.go       ~/Library/LaunchAgents/<id>.plist
  autostart_windows.go      HKCU\Software\Microsoft\Windows\CurrentVersion\Run
  autostart_linux.go        ~/.config/autostart/baobar.desktop
internal/login/             DELETED (see "What gets deleted")
```

`internal/authflow` has no dependency on `internal/bao` or `internal/tray` — it returns the
token string and lets the caller decide what to do with it. `cmd/baobar` is what wires
`authflow.OIDC`/`authflow.UserpassBrowser` to `bao.WriteToken` and to `tray.Options`'
function values, exactly as it already wires logout.

### The shared one-shot server

Both flows are the same shape: open a browser, wait for the browser to come back, write a
token. That shape is the package's core and is written once.

- Binds **127.0.0.1 only**. Never `0.0.0.0`.
- Serves exactly one flow. A second attempt while one is live is refused, and the menu
  item is disabled for the duration.
- Shuts down on success, on failure, or after a **5-minute timeout**, whichever comes
  first. The timeout is not optional: a listener left running is a standing local endpoint.
- Every response carries `Cache-Control: no-store`.
- On completion it serves a plain "you can close this tab" page and signals the caller.

### OIDC flow

Mirrors what the CLI does internally, so an existing `allowed_redirect_uris` on the role
keeps working unchanged.

1. Generate a `client_nonce` (32 bytes, crypto/rand, hex).
2. `POST /v1/auth/<oidc_mount>/oidc/auth_url` with
   `{"role": <oidc_role>, "redirect_uri": "http://127.0.0.1:<callback_port>/oidc/callback",
   "client_nonce": <nonce>}` → `data.auth_url`. The redirect URI uses the **IP address**
   `127.0.0.1`, not the hostname `localhost`: the listener below binds IPv4 loopback only,
   and `localhost` can resolve to `::1` first on a machine with IPv6 enabled, which would
   send the callback to a different (and possibly unbound, or worse, someone else's)
   listener instead of this one. A role whose allowed redirect URIs list only the
   `localhost` form needs the `127.0.0.1` variant added.
3. Start the listener on `callback_port` (default **8250**) with a `/oidc/callback` route.
4. Open the system browser at `auth_url`.
5. The provider redirects back with `state` and `code`.
6. `GET /v1/auth/<oidc_mount>/oidc/callback?state=…&code=…&client_nonce=…` →
   `auth.client_token`.
7. Write the token, shut down, force a poll.

`callback_port` cannot be randomised: it must match what the role permits. If the port is
already in use, fail with a message that says so plainly rather than silently picking
another — a different port would just fail the redirect check later, further from the
cause.

`oidc_role` may be empty when the mount has a `default_role` configured; send the field
only when set.

### Userpass + TOTP flow

**This is two-phase.** Verified against the OpenBao API docs, not assumed.

1. Start the listener on a random free port with a single-use nonce in the path:
   `/login/<nonce>`.
2. Open the browser there. The page is a form: username (prefilled from `username` config
   if set), password, TOTP code.
3. On POST: `POST /v1/auth/<userpass_mount>/login/<username>` with `{"password": …}`.
   - **If no MFA is configured**, the response already contains `auth.client_token`. Done.
     This case must work — not every user has MFA enforced.
   - **If MFA is required**, the response contains `auth.mfa_requirement` with an
     `mfa_request_id` and `mfa_constraints`. The constraints are a map of enforcement name
     → `{"any": [{"type": "totp", "id": "<uuid>", "uses_passcode": true, "name": …}]}`.
4. Pick the first constraint entry with `uses_passcode: true`, take its `id`, and
   `POST /v1/sys/mfa/validate` with
   `{"mfa_request_id": …, "mfa_payload": {"<id>": ["<passcode>"]}}` → `auth.client_token`.
5. Write the token, shut down, force a poll.

The method identifier **must be read from the response**, never configured. The API accepts
either the method's UUID or its name as the payload key; prefer the UUID from the response
and fall back to the name if the UUID is absent.

A rejected password or a wrong passcode returns 403. That must re-render the form with an
error, on the same page, with the fields cleared — not a dead end that forces the user to
start the flow again from the menu.

### Autostart

```go
type Autostart interface {
    Enabled() (bool, error)
    Enable() error
    Disable() error
}
```

| Platform | Mechanism |
|---|---|
| macOS | `~/Library/LaunchAgents/com.brutalsystems.baobar.plist` with `RunAtLoad`. Works for an unbundled binary; `SMAppService` would require an app bundle. |
| Windows | A `HKCU\Software\Microsoft\Windows\CurrentVersion\Run` value pointing at the executable. |
| Linux | `~/.config/autostart/baobar.desktop` per the XDG autostart spec. |

Each records the **absolute path of the running executable** (`os.Executable`), so moving
the binary and re-toggling fixes the entry.

The checkbox reflects real on-disk state, read at startup and re-read after every toggle —
never remembered in memory alone. **If enabling fails, the checkbox stays unchecked and an
alert says why.** Showing "on" for something that did not happen is the same class of lie
as M1's logout reporting success while the token stayed valid server-side; that bug is
already fixed and must not be reintroduced in a new place.

---

## Security

This milestone handles a password, so the rules are explicit.

- The listener binds **loopback only** and dies on completion or after 5 minutes.
- The userpass form lives behind a **single-use crypto-random nonce** in the path, so no
  other local process can drive the flow by guessing a URL.
- **No credential is ever logged, cached, or placed in a URL or process argument.** Only
  the resulting token is persisted, to `~/.vault-token`, unchanged from M1.
- The form sets `autocomplete="off"` and the page is served `no-store`.
- The OIDC `client_nonce` binds the auth_url request to the callback exchange.
- The success page contains no token, no identity, and no server address.

## Configuration additions

| Setting | Env | Default | Notes |
|---|---|---|---|
| `oidc_mount` | `BAOBAR_OIDC_MOUNT` | `oidc` | mount path of the OIDC auth method |
| `oidc_role` | `BAOBAR_OIDC_ROLE` | *(empty)* | omitted from the request when empty |
| `userpass_mount` | `BAOBAR_USERPASS_MOUNT` | `userpass` | |
| `callback_port` | `BAOBAR_CALLBACK_PORT` | `8250` | must match the role's allowed redirect URI |
| `username` | `BAOBAR_USERNAME` | *(empty)* | prefills the form only; never a credential |

## Tray changes

- "Login with password + TOTP" and "Login with SSO" keep their labels but no longer open a
  terminal. Both are disabled while a flow is in progress.
- A new checkable "Start at login" item.
- The CLI-missing branch and its explanatory labels are removed — there is no longer a CLI
  to miss.
- Flow failures surface through the existing `Alert` mechanism.

## Testing

Both flows are testable end to end without a GUI: point the client at an `httptest` fake
OpenBao and drive Baobar's own listener with an HTTP client instead of a browser. The
browser-opening function is injected so tests capture the URL rather than launching
anything.

Cases that must exist:

- OIDC happy path; callback with a mismatched `state`; callback carrying a provider error;
  the 5-minute timeout firing.
- Userpass **with no MFA configured** (token straight from phase one).
- Userpass with TOTP: constraint parsed by UUID, and by name when the UUID is absent.
- Wrong passcode → 403 → form re-renders with an error and cleared fields.
- Port 8250 already bound → a clear failure naming the port.
- A second flow attempted while one is live → refused.
- Autostart: enable, verify the on-disk artifact, `Enabled()` reports true, disable, verify
  removal — per platform, against a temporary HOME.
- Autostart enable failure → `Enable()` returns an error and `Enabled()` still reports
  false.

## What gets deleted

`internal/login` in its entirety: `Command`, `Launch`, `CLIAvailable`, `cliAvailable`, the
`MethodUserpass`/`MethodOIDC` constants, and their tests. In the tray: the `CLIAvailable`
option, the disabled-items branch, and the "Login needs the bao CLI" labels. In the docs:
every statement that Baobar shells out to a terminal or needs the CLI.

`config.ValidateAddr` **stays**. It is no longer guarding a shell command, but it still
prevents a malformed address from being used to construct request URLs, and it is the
single validator both packages share.

## Open questions

1. **The OIDC role name.** `bao login -method=oidc role=developer` suggests `developer`,
   but the mount may have a `default_role` making it optional. Determined at first run,
   not now.
2. **Whether the userpass enforcement returns the TOTP method by UUID or by name.** The
   API permits both as the payload key; the implementation handles both, so this resolves
   itself.
3. **Prefilling the username.** Defaulting to the OS username is convenient and usually
   right, but wrong often enough to be annoying. M2 prefills only from explicit config.

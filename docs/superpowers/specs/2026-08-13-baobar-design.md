# Baobar — design

**Status:** M1 implemented on branch `m1-indicator` — all packages unit-tested, and the app
has been run once on macOS against a live server (the indicator, the countdown, and the
audit-log throttle were confirmed working; the remaining manual checks are outstanding).
The milestones below split userpass+TOTP (M2) and OIDC/SSO (M3) into two phases; that split
was later reordered and merged into a single milestone, also called M2, covering both plus
start-at-login — see
[`2026-08-13-baobar-m2-terminal-free-login-design.md`](2026-08-13-baobar-m2-terminal-free-login-design.md).
That milestone is implemented and unit-tested, but not yet exercised against a live server.
See the README for exactly what is and is not verified.
**Date:** 2026-08-13

A cross-platform menu bar / system tray app that shows whether you are signed in to
OpenBao, how long your token has left, and gets you signed back in without opening a
terminal.

---

## Why this exists

A shell prototype already does exactly this: a SwiftBar/xbar/Argos plugin, kept in a
private ops repo (see `docs/local/verification.md` for the path). Read it before writing
any code — it is the working reference, and this design is largely a port of its behavior
plus the one thing it cannot do.

That script works on macOS and Linux because both have a plugin host that renders a
script's stdout into the menu bar. **Windows has no equivalent.** The documented
workarounds — a PowerShell prompt indicator, a scheduled toast — are all worse: the prompt
indicator only knows whether `~/.vault-token` exists, not whether it has expired.

Baobar closes that gap by *being* the host rather than depending on one. That is the whole
product thesis. Every scope decision below defers to it.

Downstream consequence worth stating plainly: SOPS in `st/aws` reads `~/.vault-token`
directly, so "am I signed in to OpenBao" is really "can I decrypt this repo right now."
That is why the countdown matters enough to occupy screen space.

## Name

`baobar` = bao (the OpenBao dumpling logo) + menu bar. Availability checked 2026-08-13:

| Where | Status |
|---|---|
| npm, PyPI, Homebrew formula, Homebrew cask, crates.io | free (404) |
| GitHub | 9 name matches, all 0-star and unrelated (a Milanese bao restaurant, etc.) |
| `baobar.dev` / `.io` / `.app` / `.sh` | no NS record — likely available |
| `baobar.com` | taken, parked on Sedo |

The name reads slightly oddly on Windows, which has a "tray" and no "bar." Accepted. If
the project ever grows past OpenBao into a general credential indicator, the better name
is **Doorman**, which isn't tied to a backend — see *Scope* below.

---

## Decisions

Three choices were made deliberately. Changing any of them is a real redesign, not a
refactor, so each records its reasoning.

### 1. Go + systray

`fyne.io/systray` (or `energye/systray` if richer menus are needed) wraps Cocoa
`NSStatusItem`, Win32 `Shell_NotifyIcon`, and DBus/StatusNotifierItem behind one Go API.

Chosen because: one static binary per platform, no runtime for the user to install,
cross-compiles from a Mac with `GOOS=windows go build`, ~8MB.

The cost: **systray can only draw a menu.** It cannot draw a text input. That constraint
shapes milestones M2/M3 below.

Rejected: Tauri v2 (real webview for settings/TOTP windows, but a Rust + WebView2
toolchain for a thing whose entire UI is a dropdown) and Python + pystray (fastest
prototype, but shipping means PyInstaller, ~40MB artifacts, and notarization pain).

### 2. Talk to the HTTP API, not the `bao` CLI

The shell script shells out to `bao token lookup` and degrades to "⚠️ no bao" when the CLI
is missing. Baobar instead calls `GET /v1/auth/token/lookup-self` with the token read from
`~/.vault-token`.

Chosen because the entire reason Baobar exists is Windows, and a Windows developer is the
person least likely to have the OpenBao CLI installed. Requiring the CLI would reintroduce
the gap the app was built to close.

Interop is preserved in the other direction: **Baobar keeps reading and writing
`~/.vault-token` in the standard location**, so `sops -d`, the `bao` CLI, and anything else
that expects it keep working with whatever session Baobar established. Do not move this
file or invent a private token store.

### 3. OpenBao only, no backend abstraction

No `Backend` interface, no plugin system, no AWS SSO / kubeconfig / 1Password support. The
token model (`~/.vault-token`, absolute `expire_time`, policy list) is hardcoded.

If a second backend ever genuinely appears, that is the moment to abstract — and probably
also the moment to rename to Doorman. Not before.

---

## Architecture

```
baobar/
  cmd/baobar/main.go        wiring only: config -> poller -> tray
  internal/bao/             token state machine, API client, cache      (no UI, no CLI)
  internal/tray/            icon + menu rendering, click wiring
  internal/authflow/        auth flows; writes ~/.vault-token (renamed from internal/login/ during M2 — see the M2 design doc)
  internal/config/          VAULT_ADDR, intervals, thresholds
  internal/notify/          desktop notifications (e.g. gen2brain/beeep)
```

The boundary that matters: `internal/bao` knows nothing about menus and never shells out,
so the entire interesting logic — expiry math, cache freshness, state transitions — is
unit-testable with no OS integration. `internal/tray` stays thin enough that hand-testing
it on three operating systems is tolerable.

### State machine

`internal/bao` exposes a single `State` with exactly these values. Keep them distinct in
the UI; conflating them is how the script's successor would regress.

| State | Meaning | Indicator |
|---|---|---|
| `SignedIn` | valid token, >30m left | green icon + `6h19m` |
| `Expiring` | valid token, ≤30m left | amber icon + `22m` |
| `SignedOut` | no token file, or expired, or 403 from the server | red icon + `login` |
| `Degraded` | server unreachable; counting down from cache | dimmed icon + countdown |

`Degraded` is not a cosmetic nicety. A network blip must never render as "logged out" —
the token is still valid and the user must not be nagged into a pointless re-login.

### Data flow

```
tick (every 1s, local)         -> recompute countdown from cached expires_at -> redraw
tick (every RECHECK, default 300s) -> GET /v1/auth/token/lookup-self
                                       200 -> update cache {checked_at, expires_at, name, policies}
                                       403 -> clear cache, SignedOut
                                       net error -> Degraded, keep local countdown
crossing 30m / 5m remaining    -> desktop notification (once per threshold per token)
```

### The audit-log invariant

**Read this before changing any interval.**

Every `lookup-self` is an authenticated request that lands in the OpenBao audit log.
Polling every 30 seconds would add roughly **2,900 audited requests per user per day**,
burying the decrypt events the audit log exists to surface.

The token's `expire_time` is absolute, so the countdown is computed **locally** and the
server is consulted at most once per `RECHECK` window (default 300s). A revoked token is
still noticed within five minutes, at about 1% of the audit noise. Measured in the shell
prototype: ~0.18s when it calls the server, ~0.05s from cache.

Redrawing the countdown once a second is free — it touches no network. **Do not "fix" the
perceived staleness by removing the cache or shortening `RECHECK`.** If a future
requirement genuinely needs faster revocation detection, the right answer is a server-side
push, not polling.

### Platform truths that shape the design

Both were found while writing the M1 plan, after this spec was first approved, and both
were verified against the systray source:

- **`SetTitle` renders no text on Windows.** The Windows tray has an icon and a tooltip and
  nothing else. The countdown therefore reaches Windows users through `SetTooltip` plus a
  color-coded icon, and **every state must be distinguishable by icon alone** — which
  promotes icon work from open question 4 to an M1 requirement.
- **~~Linux cannot be cross-compiled from macOS.~~ CORRECTED 2026-08-13 during M1.** This
  claim was wrong. It came from the `getlantern/systray` README, which does require
  `CGO_ENABLED=1` plus `gcc`, `libgtk-3-dev`, and `libayatana-appindicator3-dev`. The fork
  actually used here, `fyne.io/systray` v1.12.2, implements Linux in **pure Go over D-Bus**
  (`StatusNotifierItem`, via `godbus/dbus/v5`); only the darwin files use cgo. Verified
  against the module source: `GOOS=linux go build` succeeds from macOS with no cgo and no
  system headers. Windows likewise cross-builds (pure-Go syscalls, `CGO_ENABLED=0`) and
  wants `-ldflags "-H=windowsgui"` to suppress a console window.

  What remains true is narrower and worth keeping: a Linux binary *building* is not the
  same as the tray *appearing*. The D-Bus path needs a session bus and a
  StatusNotifierItem-capable desktop, so a real Linux desktop run is still unverified. If a
  future systray version reintroduces a cgo path, the `apt-get` line above becomes relevant
  again.

### Two consequences for the core

- **`Status` carries the absolute `ExpiresAt`**, not just a remaining duration. The tray
  needs it to identify the current token when tracking notification thresholds, and to keep
  the countdown ticking while a poll is in flight. Reconstructing it as `now + Remaining` at
  the call site is the fragile version of this.
- **The cache records the token file's mtime and size.** A cache keyed only by *when* it
  was written cannot tell that the token underneath it changed — log in again from a
  terminal and Baobar would show the previous token's expiry for the rest of the recheck
  window. File metadata is not token material, so the no-secrets rule holds.

### Startup and responsiveness

Two rules that are easy to get wrong and unpleasant to live with:

- **Configuration errors belong in the tray, not on stderr.** Baobar is normally launched
  by double-click, where there is no terminal. Exiting with a log message is
  indistinguishable from the app failing to start — and "no `VAULT_ADDR` yet" is the most
  likely state a new user is in. A misconfigured Baobar shows a ⚠️ icon whose menu names
  the problem and the config path.
- **The status poll runs on its own goroutine.** A lookup can block for the full HTTP
  timeout; if that happens on the goroutine servicing menu clicks, the menu freezes every
  recheck. The UI reads the last known `Status` and recomputes the countdown locally.

### Files and locations

| Path | Purpose |
|---|---|
| `~/.vault-token` (`%USERPROFILE%\.vault-token`) | the live token — shared with the CLI and SOPS |
| `~/.cache/baobar/status.json` (OS-appropriate: `os.UserCacheDir()`) | cached `expires_at`, display name, policies |
| `~/.config/baobar/config.toml` (`os.UserConfigDir()`) | `VAULT_ADDR`, recheck, thresholds |

The cache holds **no token material** — only an expiry timestamp, display name, and policy
names. Deleting it must be harmless: it just forces a fresh server check.

`VAULT_ADDR` comes from config file, then the `VAULT_ADDR` env var, then nothing. Because
this repo is public, **do not hardcode `https://bao.example.com`** anywhere outside
of examples in documentation. First run with no config should say so plainly rather than
failing cryptically.

---

## Milestones

### M1 — the indicator (ship this first)

Status, countdown, signed-in-as, policy list, link to the web UI, and logout
(`POST /v1/auth/token/revoke-self`, then delete the token file and cache). Login menu
items still shell out to a terminal exactly as the shell script does today
(`osascript` on macOS, `powershell` on Windows, `gnome-terminal`/`$TERMINAL` on Linux).

M1 is a *partial* Windows win, and it is worth being precise about the limit. A Windows
developer gets a live, expiry-aware indicator that exists in no form today — genuinely new,
genuinely worth shipping first. But M1's login still shells out to `bao login`, and
Decision 2 above rests on Windows users being the least likely to have that CLI. So on a
bare Windows machine the login items are a dead end until M2.

M1 must therefore detect a missing `bao` CLI, disable the login items, say why, and leave
the web UI link as the way through. Claiming M1 "solves Windows" would be overselling it:
it solves *knowing*, not *acting*. M2 solves acting.

> **Superseded.** M2 shipped browser-based login for both flows, on every platform. The
> terminal shell-out, the `bao`-CLI dependency, and the "dead end on a bare Windows
> machine" limitation described above no longer exist in the code. See
> [`2026-08-13-baobar-m2-terminal-free-login-design.md`](2026-08-13-baobar-m2-terminal-free-login-design.md).
> The reasoning above is kept for the record.

Menu layout, ported from the prototype:

```
[green icon] 6h19m
---
https://bao.example.com          -> opens /ui
Signed in as userpass-dev
Policies: admin, deploy
Expires in 6h19m
---
Log out (revoke token)
---
Login with password + TOTP
Login with SSO
---
Refresh now
Quit
```

### M2 — userpass + TOTP in-app

`POST /v1/auth/userpass/login/<username>` with the TOTP supplied as the MFA payload, then
write the returned `client_token` to `~/.vault-token`. Removes the terminal from the
common path.

Needs a two-field prompt, which systray cannot draw. **Use a native prompt spawned as a
subprocess** — `osascript -e 'display dialog'` on macOS, a PowerShell/WinForms
`InputBox` on Windows, `zenity --entry` (with a Qt/`kdialog` fallback) on Linux — rather
than adopting a GUI toolkit.

### M3 — OIDC / SSO in-app

Open the system browser to the authorize URL and listen on `localhost:8250/oidc/callback`
— the same flow `bao login -method=oidc` performs internally. Exchange the code, write the
token file.

> **Superseded.** The M2/M3 split below (userpass first, OIDC second) was reordered and
> merged into a single milestone. See
> [`2026-08-13-baobar-m2-terminal-free-login-design.md`](2026-08-13-baobar-m2-terminal-free-login-design.md),
> which also removes the terminal shell-out entirely rather than keeping it as M1's
> stopgap. The reasoning below is kept for the record.

### The revisit trigger

If the native-prompt approach in M2 turns out fragile across the three platforms — quoting
bugs, focus-stealing, missing `zenity` — **that is the honest signal to reconsider Tauri**,
which gets a real window for free. Make that call explicitly at the end of M2 rather than
accumulating shell-escaping workarounds.

---

## Testing

TDD applies to `internal/bao`, which is where all the logic that can be wrong lives:

- expiry parsing from fixture `lookup-self` JSON, including the `ttl`-only fallback when
  `expire_time` is absent
- cache freshness: fresh cache is reused, stale cache triggers a fetch, expired-but-fresh
  cache still reports `SignedOut`
- the state machine's transitions, especially `Degraded` on network error vs `SignedOut`
  on 403 — the failure most likely to regress
- clock skew and negative remaining time
- notification thresholds fire once per token, not once per tick

The API client tests against `httptest.Server`. `internal/tray` is verified by hand on
macOS, Windows, and Linux — accept that; keeping the package thin is what makes it cheap.

---

## Distribution

Public GitHub repo, MIT. `goreleaser` → GitHub Releases with binaries for
darwin/arm64, darwin/amd64, windows/amd64, linux/amd64.

Homebrew cask for macOS. **Signing and notarization is the first real chore** — an
unsigned tray app is a bad first impression on macOS, and it needs an Apple Developer
account plus notarization in CI. Budget for it rather than discovering it at release.

winget and scoop for Windows, and a `.deb`, come later.

---

## Open questions

Not blockers for M1. Decide when the milestone that needs them arrives.

1. **Autostart.** Login items on macOS, Run key on Windows, `.desktop` on Linux. Probably a
   menu toggle, but not in M1.
2. **Multiple OpenBao servers.** Someone with a work and a personal instance. The token
   file is singular, so this is genuinely awkward — punt until asked.
3. **Renew vs re-login.** `POST /v1/auth/token/renew-self` could extend an expiring token
   in one click, if the token is renewable and within its max TTL. Attractive for the
   `Expiring` state; needs a check of what the `st/aws` policies actually permit.
4. **Icon design.** RESOLVED for accessibility, still open for craft. M1 ships five
   generated shapes — filled circle, diamond, ring, hollow square, triangle — so every
   state is identifiable in greyscale rather than by hue alone, which matters because the
   icon is the whole state signal on Windows and red/green is the worst possible pair for
   colourblind users. A test compares silhouettes so this cannot regress. What remains is
   craft, not accessibility: a bao bun rendered as a macOS template image so it adapts to
   dark/light mode, keeping the per-state shape distinction.

---

## Starting a new session on this

Read, in order:

1. this document
2. `docs/superpowers/plans/2026-08-13-baobar-m1-indicator.md` — the M1 implementation plan
3. `docs/local/verification.md` (git-ignored, local only) — the shell prototype's location,
   the real server address for manual testing, and the audit-log check

Then start with M1: `go mod init`, `internal/bao` under TDD, and a tray that renders the
menu above.

**This repo is public.** No internal hostnames, usernames, policy names, cluster
topology, or absolute paths from a private machine belong in a committed file. Environment
specifics live in `docs/local/`, which is git-ignored. Public docs and tests use
`bao.example.com`, `userpass-dev`, and placeholder policy names.

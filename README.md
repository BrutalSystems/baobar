# Baobar

[![CI](https://github.com/BrutalSystems/baobar/actions/workflows/ci.yml/badge.svg)](https://github.com/BrutalSystems/baobar/actions/workflows/ci.yml)

A menu bar / system tray indicator for [OpenBao](https://openbao.org): shows whether you
are signed in and how long your token has left, everywhere a shell-script plugin can't
(Windows has no menu-bar-script host). macOS, Windows, and Linux.

```
[green]  6h19m     signed in, 6h19m remaining
[amber]  22m       under 30 minutes left (expiry warning)
[red]    login     not signed in
[grey]   ~6h19m    server unreachable, counting down from the last known session
```

Logging back in opens your **browser**, not a terminal. Baobar drives both the SSO (OIDC)
flow and a password+TOTP form itself, over a short-lived loopback listener, and never
shells out to the `bao` CLI or any terminal for anything — see [Status](#status) and
[State table](#state-table) below for exactly what that means and what has (and hasn't)
been exercised against a live server.

Baobar contacts the server (`lookup-self`) at most once per `BAOBAR_RECHECK` window,
throttled by wall-clock time rather than by whether a cache happens to exist — an expired
token, an unwritable cache, or an unreachable server all stay inside that same one-request
budget instead of retrying every poll tick. The countdown itself is recomputed locally
every second in between, with no network call. Clicking "Refresh now" is the one
deliberate exception: it forces the very next check past the throttle. See
[The audit-log invariant](#the-audit-log-invariant) before touching `BAOBAR_RECHECK`.

## Status

M1 and M2 are code-complete: all internal packages (`bao`, `config`, `notify`, `tray`,
`authflow`, `autostart`) are implemented and unit-tested. All Go unit tests pass under
`-race`; `go build`, `go vet`, and `gofmt` are clean; macOS, Windows, and Linux binaries all
cross-compile.

**Verified by running it on macOS against a live OpenBao server** (one M1 session, before
M2's login work began): the tray item appeared, the `Expiring` state rendered correctly
with its countdown, the menu showed identity/policies/expiry, the signed-out state
rendered, and the audit-log throttle held — over 100 seconds of runtime the cached
`checked_at` never moved while the countdown kept ticking, i.e. one server request, not one
per second.

**Verified on Windows Server 2022 against the same live server:** the tray icon renders;
SSO login runs end to end in a real browser; the token Baobar writes is picked up by the
`bao` CLI with no terminal login; autostart survives a real sign-out and sign-in; the
"Start at login" guards behave (a deleted binary reports unchecked, a temporary path is
refused); and the misconfiguration path names both the problem and the config file.

That pass found one bug that made the app invisible on Windows — the tray icon was PNG,
which `systray` accepts only on macOS and Linux — fixed in v0.1.1.

**Not verified:**

- The **Linux binary has never been run.** It cross-compiles and its tests pass in CI, but
  whether the tray item actually appears over D-Bus is unknown.
- **`windows_arm64`** is published but has never been run on Windows-on-ARM hardware. AWS
  offers no Windows-on-ARM instances, so the Windows pass above could not cover it.
- The macOS **"Start at login" checkbox has never been clicked in the tray.** The bug behind
  it was found and fixed against a real stale LaunchAgent, but not through the UI.
- **Windows and Linux binaries are unsigned.** macOS is signed and notarized as of v0.1.2;
  the other two platforms are not, so Windows SmartScreen may warn on first run.

See [Build matrix](#build-matrix) for what has and has not been exercised per platform.
Build, run, and test are three different claims, and this section tries to keep them apart.

## State table

| State | Icon | Menu bar label | Meaning |
|---|---|---|---|
| Signed out | red **ring** (hollow) | `login` | no usable token on disk, or the server rejected it |
| Signed in | green **filled circle** | `<countdown>` | token valid, more than the warning window left |
| Expiring | amber **diamond** | `<countdown>` | inside the warning window (`BAOBAR_WARN`, default 30m); fires one desktop notification per threshold |
| Degraded | grey **hollow square** | `~<countdown>` or `?` | server unreachable; counting down from the cached expiry rather than declaring you signed out |

Every state has its own **shape**, not just its own colour. On Windows the icon is the
entire state signal, and distinguishing signed-out from signed-in by red-versus-green alone
would be unreadable for the ~8% of men with red/green colour blindness. A test decodes the
icons and compares their silhouettes, so a change that reduces two states to a difference
in hue fails the build.

On Windows the menu bar carries no text (`SetTitle` is a no-op there — see
[Platform notes](#platform-notes)), so the tooltip is the primary readout: it spells out
the same state in a full sentence, e.g. "OpenBao: signed in as userpass-dev, 6h19m left
[developer]".

The tray menu itself always shows who you're signed in as, your policies (`default`
filtered out), the expiry, a "Log out (revoke token)" item, two login items ("Login with
password + TOTP", "Login with SSO" — each opens a browser tab; see [Login](#login) below),
a "Start at login" checkbox (see [Start at login](#start-at-login)), a manual "Refresh
now", and Quit.

## Login

Both login items open your default browser instead of a terminal. Baobar never invokes the
`bao` CLI, and does not require it to be installed, for login or anything else.

- **Login with SSO** drives an OIDC flow: Baobar asks OpenBao for an authorization URL,
  opens it in your browser, and catches the redirect on a short-lived loopback listener
  bound to both `127.0.0.1` and `[::1]`.
  See [callback_port](#configuration) below — this is the single most likely thing to trip
  up a first run.
- **Login with password + TOTP** opens a small form, served from that same loopback
  listener behind an unguessable, single-use path (a random nonce), in your browser.
  Credentials live only in the POST request that carries them to OpenBao — Baobar never
  writes a password or passcode to disk or to a log. A wrong password or passcode
  re-renders the same page with an error and asks again; the form does not navigate away or
  lose the username you already typed.

Either flow writes the resulting token to `~/.vault-token` on success, exactly as a
successful `bao login` would, and the tray picks it up on its next refresh.

**Why the OIDC callback route has no nonce, unlike the userpass form.** The userpass
form's path is unguessable on purpose (a random nonce), but the OIDC callback path is
`/oidc/callback` — fixed and effectively public, because the redirect URI registered with
the identity provider has to name a specific path and port. That is a deliberate,
bounded gap, not an oversight: another local process that guesses the callback path and
sends a request to it can only ever abort an in-progress login (by racing OpenBao's real
redirect and getting rejected, or by tripping `guard`'s Host check) — it cannot obtain a
token. Completing the flow requires `client_nonce`, which is generated in-process, sent to
OpenBao over the authenticated `auth_url` request, and never written anywhere a local
attacker could read it before the legitimate callback arrives; OpenBao binds `state` to
that nonce server-side, so a forged callback with a guessed or replayed `state` fails the
exchange rather than returning a token.

## Install

### Homebrew

```bash
brew tap brutalsystems/tap
brew trust brutalsystems/tap    # Homebrew refuses casks from untrusted third-party taps
brew install --cask baobar
```

macOS binaries are signed with a Developer ID and notarized by Apple as of v0.1.2, so this
just works — no Gatekeeper dialog and no `xattr` incantation. Verify it yourself if you
like:

```bash
codesign --test-requirement="=notarized" -vv "$(brew --prefix)/bin/baobar"
```

If you are on v0.1.1 or earlier, upgrade — those binaries were unsigned, and Gatekeeper
offered to move them to the Trash.

#### Why the signature says Springthrough

Verifying a macOS build shows a name that is not BrutalSystems:

```
$ codesign -dv --verbose=2 "$(brew --prefix)/bin/baobar"
Authority=Developer ID Application: Springthrough Consulting Inc. (7CQD3Q2Y8Z)
TeamIdentifier=7CQD3Q2Y8Z
```

**This is expected, not a compromised build.** Baobar is published by BrutalSystems, but is
currently signed with Springthrough Consulting's Apple Developer identity — the two share an
owner, and a separate BrutalSystems Apple team does not exist yet. Team ID `7CQD3Q2Y8Z` is
the value to expect; treat anything else as suspect.

This is temporary. When BrutalSystems has its own Apple team the Team ID will change, and
that change will be called out in the release notes — a signing identity that changes
without explanation is exactly the thing you should not accept quietly.

### Download a release

Binaries for macOS (universal), Linux and Windows:
<https://github.com/BrutalSystems/baobar/releases>

### Build from source

The path with no signing caveats:

```bash
git clone https://github.com/BrutalSystems/baobar
cd baobar
go build -o baobar ./cmd/baobar
```

Run it with `VAULT_ADDR` set, or a `config.toml` in place (see below):

```bash
VAULT_ADDR=https://bao.example.com ./baobar
```

If neither is set, Baobar still starts — the tray shows a ⚠️ icon whose menu names the
problem and the config path it wants, plus a working Quit. It never exits silently or logs
to a terminal that may not exist: Baobar is normally launched by double-click, where there
is no terminal to read.

## Configuration

**You do not need to write this file by hand.** On first run, if no address is configured,
Baobar opens a browser tab asking for your OpenBao server address, saves it, and carries on
starting up. Everything below is for changing settings afterwards, or for setting the ones
the first-run prompt deliberately does not ask about.

Settings resolve from a config file first, then the environment, then a built-in default.
The file lives at the OS-appropriate config directory (`os.UserConfigDir()`):

- macOS: `~/Library/Application Support/baobar/config.toml`
- Linux: `~/.config/baobar/config.toml`
- Windows: `%AppData%\baobar\config.toml`

```toml
addr = "https://bao.example.com"
recheck = "5m"
warn = "30m"
oidc_mount = "oidc"
oidc_role = ""
userpass_mount = "userpass"
callback_port = 8250
username = ""
```

| Setting | Env var | Default | Notes |
|---|---|---|---|
| OpenBao address | `VAULT_ADDR` | — (required) | must be a bare `http(s)://host[:port][/path]`, no userinfo or query string |
| Recheck interval | `BAOBAR_RECHECK` | `5m` | how often the server is actually asked; hard floor of 60s |
| Warning threshold | `BAOBAR_WARN` | `30m` | remaining time at which the icon turns amber and a notification fires |
| OIDC mount | `BAOBAR_OIDC_MOUNT` | `oidc` | auth mount path used for SSO login |
| OIDC role | `BAOBAR_OIDC_ROLE` | *(empty)* | role name passed to the OIDC auth URL request; the `role` field is omitted from the request entirely when this is empty |
| Userpass mount | `BAOBAR_USERPASS_MOUNT` | `userpass` | auth mount path used for password + TOTP login |
| Callback port | `BAOBAR_CALLBACK_PORT` | `8250` | port Baobar listens on for the OIDC redirect — see below, this must match the role |
| Username | `BAOBAR_USERNAME` | *(empty)* | prefills the username field on the password + TOTP form; it is never treated as a credential and never sent anywhere by itself |

**`callback_port` must match the OIDC role's allowed redirect URI.** Baobar sends
`http://localhost:<callback_port>/oidc/callback` — the same form the `bao` CLI uses, so a
role and an identity-provider app registration that already work with the CLI need no
change.

That hostname is deliberate, and so is what backs it. `localhost` can resolve to either
`127.0.0.1` or `::1`, and a listener bound to only one of them would leave the other free
for any local process to occupy and receive your authorization code. Baobar therefore
**binds both** `127.0.0.1:<port>` and `[::1]:<port>` for the seconds a login is in flight,
so whichever address the browser picks, Baobar is the one listening. If either address is
already in use, the login fails with a message saying so rather than proceeding with half
the pair.

An earlier design sent the `127.0.0.1` form instead. It was abandoned because the redirect
URI must be allow-listed in *two* systems — the OpenBao role and the identity provider's
app registration — and requiring a change in both, one of which is often outside your
control, is a worse trade than simply holding both addresses.

Baobar also reads and writes `~/.vault-token` (`%USERPROFILE%\.vault-token` on Windows) —
the same file the `bao` CLI and SOPS use, for compatibility — and caches the last known
expiry, display name, and policies (never the token itself) under `os.UserCacheDir()`.
Baobar does not require the `bao` CLI to be installed and never invokes it; the shared file
format is the only connection between them.

### Start at login

The "Start at login" tray checkbox reflects and controls a real, platform-native autostart
entry — not a remembered setting, so the checkbox always shows what's actually on disk:

| Platform | Mechanism | Location |
|---|---|---|
| macOS | LaunchAgent plist | `~/Library/LaunchAgents/com.brutalsystems.baobar.plist` |
| Windows | per-user Run key (no elevation needed) | `HKCU\Software\Microsoft\Windows\CurrentVersion\Run` |
| Linux | XDG autostart entry | `~/.config/autostart/baobar.desktop` |

Checking the box writes the entry, pointing at the current executable's path; unchecking it
removes the entry. Removing an already-absent entry is not an error.

### The audit-log invariant

**Read this before lowering `BAOBAR_RECHECK`.**

Every `lookup-self` is an authenticated request that lands in the OpenBao audit log.
Polling every 30 seconds would add roughly 2,900 audited requests per user per day, burying
the decrypt events the audit log exists to surface. That is also why `BAOBAR_RECHECK` has a
hard floor of 60 seconds — a lower value is rejected at startup rather than silently
clamped, and shows up as a ⚠️ config error in the tray rather than a log line.

The token's expiry is absolute, so the on-screen countdown is computed locally every
second and the server is consulted at most once per `BAOBAR_RECHECK` window (5 minutes by
default). That budget holds unconditionally — an expired token, a cache the app couldn't
write, or a server that's simply down all still cost at most one request per window, not
one per poll tick — because the throttle is timed from the last attempt, not gated on a
cache happening to exist and be fresh. A revoked token is still noticed within that
window. The one deliberate exception is clicking "Refresh now" (or a login/logout attempt
completing), which forces the very next check past the throttle on purpose. Redrawing the
countdown is free — it touches no network — so do not "fix" the perceived staleness by
shortening `BAOBAR_RECHECK` or removing the cache; that just multiplies audit noise for the
same freshness you already have.

## Build matrix

| Platform | Command | Notes |
|---|---|---|
| macOS | `go build -o baobar ./cmd/baobar` | native build only; not cross-compiled from Windows or Linux |
| Windows | `CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags "-H=windowsgui" -o dist/baobar.exe ./cmd/baobar` | cross-compiles cleanly from macOS: pure-Go syscalls, no cgo. `-H=windowsgui` suppresses the console window that would otherwise flash on launch |
| Linux | `GOOS=linux GOARCH=amd64 go build -o dist/baobar-linux ./cmd/baobar` | also cross-compiles cleanly from macOS as of `fyne.io/systray` v1.12.2, which talks to the tray over D-Bus (`StatusNotifierItem`) in pure Go rather than linking GTK/appindicator via cgo. No `gcc`/`libgtk-3-dev`/`libayatana-appindicator3-dev` were needed to produce the binary used here. That build has not yet been *run* on a Linux desktop, so treat "the tray actually appears" as unverified rather than "impossible without cgo" — if a future systray version reintroduces a cgo path, `apt-get install gcc libgtk-3-dev libayatana-appindicator3-dev` is the fallback for building on/for Linux |

The **Windows `amd64`** binary has now been run on real hardware (Windows Server 2022): the
tray icon, SSO login in a browser, the token being picked up by the `bao` CLI, registry
autostart across a sign-out and sign-in, and the misconfiguration path all check out. That
pass found the PNG-versus-ICO tray icon bug, which no amount of cross-compiling would have
surfaced. **`windows_arm64` has still never been run** — AWS has no Windows-on-ARM
instances, so it was not covered.

The **Linux** binary still has **not been run** on a Linux desktop. Compiling is not
verifying: whether the tray item appears at all depends on a session bus and a
StatusNotifierItem-capable desktop, and that remains an open item
([#5](https://github.com/BrutalSystems/baobar/issues/5)).

## Platform notes

- **`SetTitle` renders no text on Windows.** The countdown reaches Windows users only
  through the tooltip and the color-coded icon — every state is distinguishable by icon
  alone for that reason.
- **The status poll runs on its own goroutine**, separate from the one servicing menu
  clicks. A `lookup-self` can block for the full HTTP timeout; if that ever happened on the
  UI goroutine the menu would freeze on every recheck. The tray reads the last known status
  and recomputes the countdown locally instead.

## Design

Full rationale, alternatives considered, and the M1 plan:
[`docs/superpowers/specs/2026-08-13-baobar-design.md`](docs/superpowers/specs/2026-08-13-baobar-design.md)
· [`docs/superpowers/plans/2026-08-13-baobar-m1-indicator.md`](docs/superpowers/plans/2026-08-13-baobar-m1-indicator.md)

Browser-based login and start-at-login (M2), including the loopback listener, the nonce
scheme, and the redirect-URI subtlety documented above:
[`docs/superpowers/specs/2026-08-13-baobar-m2-terminal-free-login-design.md`](docs/superpowers/specs/2026-08-13-baobar-m2-terminal-free-login-design.md)
· [`docs/superpowers/plans/2026-08-13-baobar-m2-terminal-free-login.md`](docs/superpowers/plans/2026-08-13-baobar-m2-terminal-free-login.md)

Not yet built, by design: token renewal, refined artwork, goreleaser, a Homebrew cask,
signing and notarization.

## Contributing

Contributions are welcome — see [CONTRIBUTING.md](CONTRIBUTING.md). It is mostly a list of
the invariants that are easy to break by accident, each of which has a real failure behind
it. [`docs/NEXT.md`](docs/NEXT.md) has the current outstanding work; the platform-verification
items are good places to start.

### Who publishes this

Baobar is owned and published by **BrutalSystems**, and the MIT licence is held by
BrutalSystems. Two things currently point elsewhere, and both are deliberate rather than
oversights:

- **macOS builds are signed with Springthrough Consulting's Apple Developer identity**
  (Team ID `7CQD3Q2Y8Z`) — see [above](#why-the-signature-says-springthrough). The two
  companies share an owner; a BrutalSystems Apple team does not exist yet.
- **Commits are authored from a personal account** rather than an organisation address.

Neither affects the licence, which is MIT: you can use, modify and redistribute this
regardless of who signed a given build. If the signing identity changes, it will be stated
in the release notes rather than left for you to notice.

## License

MIT — see [LICENSE](LICENSE).

Baobar's binaries bundle third-party open-source components, all permissively
licensed (Apache-2.0, MIT, ISC, BSD-2/3). Their notices are reproduced in
[THIRD_PARTY_LICENSES.md](THIRD_PARTY_LICENSES.md), which ships inside every
release archive; regenerate it with `tools/gen-third-party-licenses.sh`.

Security issues: see [SECURITY.md](SECURITY.md).

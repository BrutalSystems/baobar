# Baobar — what's done, what's outstanding

**Written:** 2026-08-15. **State:** `main` at 72 commits, clean tree, nothing pushed (no
remote configured). M1 and M2 are both merged.

Read this first if you are picking the project up cold. It is the honest status, not a
summary of intentions.

---

## What exists and is verified

A Go system-tray indicator for OpenBao — macOS, Windows, Linux — showing login state and a
live token countdown, with logout and both login methods driven from the menu.

**Verified against a live OpenBao server on macOS:**

- The indicator: signed-in, expiring, and signed-out states; countdown; identity and
  policies in the menu; the web-UI link.
- **Userpass + TOTP login**, including OpenBao's two-phase MFA exchange, in a browser form
  Baobar serves on loopback. No terminal.
- **SSO/OIDC login** end to end: browser → identity provider → loopback callback → token,
  picked up by the tray within a second.
- **Logout**, confirmed by a real `revoke-self` and a revoked lease in the audit log.
- **The audit-log invariant**, twice: ~2 requests per 10 minutes of runtime while the
  countdown ticked every second, and — importantly — 2 requests in 5 minutes with an
  *expired* token sitting on disk, which is the exact case that once produced one request
  per second.

**Verified by build and test only:**

- `gofmt`, `go build`, `go vet`, `go test -race` all clean across 7 packages.
- macOS, Windows, and Linux binaries all cross-compile; `GOOS=linux` and `GOOS=windows`
  vet are clean.

**Also verified since:** the Windows `amd64` binary on Windows Server 2022 (tray icon, SSO
login in a real browser, the token picked up by the `bao` CLI, autostart across a real
sign-out) and the Linux `amd64` binary on Ubuntu 24.04 under XFCE and GNOME.

**Packaging, added 2026-08-18** — an app icon on all three platforms, Windows exe icon and
VERSIONINFO, a Chocolatey Start Menu shortcut, Linux `.deb`/`.rpm` with a desktop entry and
icon theme files, and a signed, notarized `Baobar.app` for macOS. The macOS chain was run
end to end on a Mac: `notarytool` returned Accepted and `spctl` reports
`accepted, source=Notarized Developer ID`, including against a quarantined copy.

**Never run:** `windows_arm64`, `linux_arm64`, and the macOS "Start at login" checkbox via
the tray UI. The Windows icon and Start Menu shortcut are verified to be *present* but have
not been seen rendered on Windows.

---

## Recommended next steps, in priority order

### 1. Finish the Windows pass — highest expected yield

Windows is the platform the product exists for (the thesis is that Windows has no
SwiftBar-style menu-bar-script host), and it is still the least exercised. A base pass has
been done on Windows Server 2022; what follows is what that pass did not cover, plus two
things that were each wrong at some point in development:

An earlier version of this line also claimed Windows has no `bao` CLI. That was wrong.
OpenBao publishes `windows_amd64` and `windows_arm64` binaries, and there is a Chocolatey
package (`choco install openbao`, lagging upstream at the time of writing). The product
rationale is unaffected — it rests on the absence of a menu-bar-script host, which is
still true — but the claim was false and this file is meant to be the honest status.

- **Browser launch.** `browserCommand` uses `rundll32 url.dll,FileProtocolHandler`. The
  original `cmd /c start` truncated OIDC URLs at the first `&`, because `cmd.exe` re-parses
  its command line. The fix is unit-tested but has never opened a real browser.
- **Autostart.** The registry implementation (`HKCU\…\Run`) has never executed. It cannot
  be tested from macOS; the tests cover the file-based platforms only.
- **Start Menu shortcut.** After `choco install baobar`, confirm a "Baobar" entry exists in
  the Start Menu and launches the tray app; after `choco uninstall baobar`, confirm it is
  gone. The install script is syntax-checked on macOS with `pwsh`, but Chocolatey's cmdlets
  do not exist there, so the behaviour is untested until this runs on Windows.
- **Exe icon and publisher.** Confirm the exe shows the bun icon rather than the generic Go
  binary icon in Explorer and the taskbar, and that Properties → Details names BrutalSystems
  as the publisher. The resource is verified to be *in* the binary from macOS; that it is
  *drawn* can only be checked here.

Also confirm the tray icon and tooltip render at all: `SetTitle` is a no-op on Windows, so
the icon plus tooltip is the entire signal there. If the icon does not appear, that is a
real bug, not a cosmetic one.

```bash
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags "-H=windowsgui" -o dist/baobar.exe ./cmd/baobar
```

### 2. Install the Linux package

The binary itself has been run on Ubuntu 24.04 under XFCE and GNOME, so the D-Bus
StatusNotifierItem path is confirmed. What is unverified is the packaging added since:
`dpkg -i` the `.deb`, then confirm Baobar appears in the applications grid with its icon,
and that the autostart entry shows an icon in the startup-apps UI. The `.deb` payload has
been inspected and contains the binary, the desktop entry and all seven icon sizes — but
inspecting an archive is not the same as installing it.

### 3. Finish the M1 manual checks

Four of thirteen are done. The remaining ones each take a few minutes and each has a
history of finding something:

- Offline/degraded: kill the network and confirm the icon dims and the countdown keeps
  running, rather than claiming you are signed out.
- The expiry notification at 30m and 5m (force it with `BAOBAR_WARN=8h`).
- The misconfiguration path: `env -u VAULT_ADDR ./baobar` must show a ⚠️ tray item naming
  the problem, never exit silently.
- Menu responsiveness while a server check hangs: point at a black hole
  (`VAULT_ADDR=https://10.255.255.1`) and confirm clicks stay instant.
- Re-login pickup: log in from a terminal while Baobar runs; it should notice within a
  second, not wait out the recheck window.

### 4. A tray template image

**The application icon is done** — a bao bun on a cream tile, in `assets/icon/`, shipped on
all three platforms. This item is now only about the *tray* glyphs, which are a different
asset with a different job.

Accessibility there is already handled: five states with distinct **shapes** (filled circle,
diamond, ring, hollow square, triangle), enforced by a test comparing silhouettes so a
change cannot regress to hue-only. What remains is craft — rendering the bun as a macOS
template image so it adapts to light and dark menu bars, while keeping the per-state shape
distinction that the test enforces.

Do not derive these from the app icon: at 22px the app icon's ground and pleat detail turn
to mush, and the state shapes are the point. Regenerate with `go run ./tools/genicons`.

### 5. Publishing — mostly done, with three loose ends

Releases publish to GitHub, macOS is signed and notarized, the Homebrew tap and winget
manifests are wired, and the Chocolatey package is submitted. What is left:

- **Windows and Linux binaries are unsigned** ([#11](https://github.com/BrutalSystems/baobar/issues/11)).
  SmartScreen may warn on first run.
- **The `nfpms` maintainer address** is `BrutalSystems <noreply@brutalsystems.com>`, which
  may not be a real mailbox. Every `.deb` and `.rpm` carries it publicly and permanently —
  decide before the next release.
- **Notification attribution.** `beeep` shells out to `osascript` on macOS, so expiry
  notifications are attributed to Script Editor rather than to Baobar. The `.app` bundle
  now supplies the bundle identifier that fixing this requires, but the fix itself needs a
  different notification path.

Standing rule: never screenshot the menu for a README without redacting. It shows your
server hostname and your email-derived display name.

---

## Known-and-accepted, with reasons

None of these block anything. They are recorded so you do not rediscover them as surprises.

| Item | Why it is acceptable |
|---|---|
| `Human()` never rolls into days — a 32-day token renders `768h00m` | Ugly, not wrong; root tokens show `∞` instead |
| `Classify` ignores `expiryKnown` when the server is reachable | A successful lookup implies a known expiry; unenforced assumption only |
| `SaveCache`/`DeleteCache` errors are swallowed | Fails safe: one extra server call next tick |
| Cache file mode 0600 is untested | The cache holds no token material by design, asserted by a test |
| No test for malformed TOML, bad durations, or a bad `expire_time` | Behavior verified by inspection: wrapped error, no panic |
| The address allowlist accepts ports above 65535 | The regexp bounds digit count, not value; a bad port fails to dial, loudly |
| A non-GET/POST request to the login form renders the credential error | Nothing leaves the process; the empty username is rejected locally |
| The OIDC callback route has no nonce | Impossible — the redirect URI is registered, so path and port are public. Bounded: a local page can abort a login but cannot obtain a token, because the `client_nonce` never leaves the process and OpenBao binds `state` to it |
| One commit (`645892a`) does not build in isolation | References an icon function defined in the next commit; only affects `git bisect`, and a squash-merge would erase it |

---

## Things worth knowing before you change anything

These are the traps this codebase has already fallen into. Each cost a review cycle.

**The audit-log invariant is load-bearing.** Every `lookup-self` lands in OpenBao's audit
log. The server is contacted at most once per `BAOBAR_RECHECK` window (default 5m, hard
floor 60s enforced in code); the countdown is recomputed locally every second. The throttle
is **time-based and unconditional** — an earlier version keyed it on cache presence, which
meant an expired token on disk polled once per second, roughly 28,000 audit entries
overnight. Do not "fix" a stale-looking countdown by shortening the interval.

**A network failure is not a logout.** `ErrForbidden` (the server rejected the token) means
signed out. Any other error means degraded: keep the cached countdown running, dim the
icon. Conflating them tells users with valid tokens that they are logged out.

**Credentials leak through error strings in three different ways**, all found the hard way:
Go's `*url.Error` embeds the full request URL (hence `sanitize()`); an unescaped username
interpolated into a request path can redirect a password to another endpoint (hence
`PathEscape` plus outright rejection of `/ \ ? #` and control characters); and following an
HTTP redirect re-sends the body, the token header, and a `Referer` carrying the auth code
(hence `CheckRedirect: http.ErrUseLastResponse` on all three clients). Any new HTTP call
needs all three.

**Fail closed on platform errors.** The IPv6 bind check allowlists the errnos that mean "no
IPv6 here" and treats everything else as fatal, because `syscall.EADDRINUSE` is a
fabricated placeholder on Windows — the real Winsock error is `10048`. A blocklist would
fail open there, handing the callback to whoever holds `[::1]`.

**The Host guard is not CSRF protection.** A browser always sends the correct Host for the
connection it opened, so the guard defends against DNS rebinding only. The password form
protects itself with an unguessable nonce path plus a `Sec-Fetch-Site` check. Do not add a
blanket cross-site rejection: the OIDC provider's redirect is legitimately cross-site.

**A test that reimplements the code it tests protects nothing.** This happened twice — an
escaping test that validated its own copy of the escaping, and a Windows browser test that
passed with the bug restored. When you add a test for a behavior, verify it fails when you
break that behavior.

**Never claim something is verified when it is not.** A README once claimed the Windows
tray had been "eyeballed on a Windows machine"; nobody had. Build, run, and test are three
different claims.

---

## Layout

```
cmd/baobar/          wiring, browserCommand, runLogin
internal/config/     settings, address allowlist, the 60s recheck floor
internal/bao/        state machine, token file, API client, cache, poller
internal/authflow/   loopback session, OIDC flow, two-phase MFA, served form
internal/autostart/  Enabled/Enable/Disable + per-platform artifacts
internal/tray/       formatting, icons, systray wiring
internal/notify/     expiry thresholds, desktop notifications

assets/icon/         the app icon: SVG source, PNG masters, generated .icns/.ico/hicolor
packaging/chocolatey/  Windows package: nuspec + install/uninstall scripts
packaging/homebrew/  the macOS cask, hand-maintained (see Releasing below)
packaging/linux/     the applications-menu .desktop entry
packaging/windows/   versioninfo.json, the exe's VERSIONINFO block

tools/genicons/      regenerates the five 22px tray STATE icons
tools/genappicon/    derives .icns/.ico/hicolor from the app icon master
tools/mkmaster.py    renders the app icon PNG masters from the SVG (needs Chrome)
tools/macapp.sh      builds, signs, notarizes and staples Baobar.app
docs/superpowers/    specs and plans, including the 2026-08-18 desktop integration work
docs/local/          git-ignored: real server address, the internal prototype's path
.superpowers/sdd/    git-ignored: per-task ledgers, reports, and review packages
```

`docs/local/verification.md` holds the environment-specific values the public docs
deliberately omit. Start there if a command in the docs needs a real hostname.

## Releasing

### macOS: the Homebrew cask is hand-maintained

GoReleaser no longer generates the cask. An app-based cask needs the `app:` stanza, which
is GoReleaser Pro only and additionally forces DMG output, which is also Pro. The cask
therefore lives at `packaging/homebrew/baobar.rb` and is copied to the tap by hand.

After a release publishes:

1. Take the SHA-256 that `tools/macapp.sh notarize` printed — it is in the release job log,
   on the line after "notarized and stapled".
2. Update `version` and `sha256` in `packaging/homebrew/baobar.rb`.
3. Copy it to `BrutalSystems/homebrew-tap` as `Casks/baobar.rb` and push.

Two fields per release. This is the automation a Pro licence would have bought, and it is
worth revisiting that trade if Windows `.msi` installers ever land on the roadmap, since
those are Pro-only too.

### Testing the cask locally

Homebrew refuses casks that are not in a tap, so a bare file path will not install. Use a
scratch tap, and test with an *isolated* copy rather than the real cask — installing a
second cask named `baobar` collides with the installed one, and the real uninstall runs
`launchctl: com.brutalsystems.baobar`, which would tear down your own working LaunchAgent.
The procedure is in `docs/superpowers/plans/2026-08-18-macos-app-bundle.md`, Task 4.

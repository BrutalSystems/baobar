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

**Never run:** the Windows binary, the Linux binary, and the "Start at login" toggle on any
platform. See the top of the recommendations below.

---

## Recommended next steps, in priority order

### 1. Run the Windows binary — highest expected yield

Windows is the platform the product exists for (the whole thesis is that Windows has no
SwiftBar-style host and no `bao` CLI), and it is the platform least exercised. Two things
there are untested at runtime and were both wrong at some point in development:

- **Browser launch.** `browserCommand` uses `rundll32 url.dll,FileProtocolHandler`. The
  original `cmd /c start` truncated OIDC URLs at the first `&`, because `cmd.exe` re-parses
  its command line. The fix is unit-tested but has never opened a real browser.
- **Autostart.** The registry implementation (`HKCU\…\Run`) has never executed. It cannot
  be tested from macOS; the tests cover the file-based platforms only.

Also confirm the tray icon and tooltip render at all: `SetTitle` is a no-op on Windows, so
the icon plus tooltip is the entire signal there. If the icon does not appear, that is a
real bug, not a cosmetic one.

```bash
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags "-H=windowsgui" -o dist/baobar.exe ./cmd/baobar
```

### 2. Run the Linux binary

Cross-builds cleanly (`fyne.io/systray` v1.12.2 talks to the tray over D-Bus in pure Go, so
no cgo or GTK headers are needed — an earlier claim to the contrary was wrong and is
corrected in the design doc). What is unverified is whether the tray item actually appears:
the D-Bus path needs a session bus and a StatusNotifierItem-capable desktop.

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

### 4. Real icon artwork

Accessibility is already handled — five states with distinct **shapes** (filled circle,
diamond, ring, hollow square, triangle), enforced by a test that compares silhouettes so a
change cannot regress to hue-only. What remains is craft: a bao-bun rendered as a macOS
template image so it adapts to light/dark, keeping the per-state shape distinction.

Regenerate with `go run ./tools/genicons`.

### 5. Publishing, if you want it

- LICENSE is MIT, copyright "Mike Williams" taken from git authorship — **change it if the
  intended holder is an entity.**
- No GitHub remote exists yet. The module path is `github.com/BrutalSystems/baobar`; if the
  org differs, `go mod edit -module` before the first push.
- goreleaser → GitHub Releases, then a Homebrew cask. **macOS signing and notarization is
  the first real chore** — an unsigned tray app is a bad first impression and it needs an
  Apple Developer account plus notarization in CI.
- Before publishing: never screenshot the menu for a README without redacting. It shows
  your server hostname and your email-derived display name.

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
tools/genicons/      regenerates the five state icons
docs/superpowers/    specs and plans for M1 and M2
docs/local/          git-ignored: real server address, the internal prototype's path
.superpowers/sdd/    git-ignored: per-task ledgers, reports, and review packages
```

`docs/local/verification.md` holds the environment-specific values the public docs
deliberately omit. Start there if a command in the docs needs a real hostname.

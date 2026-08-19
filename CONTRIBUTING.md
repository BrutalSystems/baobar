# Contributing to Baobar

Contributions are welcome. This document is short on ceremony and long on the handful of
invariants that are easy to break by accident — every one of them was broken at least once
during development and caught in review.

## Getting set up

```bash
go build ./...
go vet ./...
go test -race ./...
```

Go 1.26.3, pinned in `.tool-versions`. There is nothing else to install.

To run it you need an OpenBao server:

```bash
VAULT_ADDR=https://bao.example.com go run ./cmd/baobar
```

With no address configured it still starts and shows a ⚠️ tray item explaining what it
wants — that is deliberate, see below.

## What state the project is in

Honest answer, because it affects what your PR needs to prove: the app has been exercised
against a live OpenBao server **on macOS only**. Windows and Linux binaries compile and
their unit tests run in CI, but nobody has run the application on either. If you are on one
of those platforms, running it and reporting what happens is one of the most useful things
you can do. See [`docs/NEXT.md`](docs/NEXT.md) for the current outstanding list.

## The invariants

Break these and the review will bounce the PR, so they are worth reading first. None are
arbitrary; each has a failure behind it.

### The server is contacted at most once per recheck window

Every `lookup-self` lands in OpenBao's audit log. Baobar therefore calls the server at most
once per `BAOBAR_RECHECK` window (default 5m, hard floor of 60s enforced in code) and
recomputes the countdown **locally** every second in between.

The throttle is **time-based and unconditional**. An earlier version keyed it on whether a
cache entry existed, which meant an expired token sitting on disk polled once per second —
roughly 28,000 audit entries overnight, per user, burying the events the audit log exists to
surface. If the countdown looks stale to you, it is not: it is recomputed every second. Do
not "fix" it by shortening the interval or removing the cache.

### A network failure is not a logout

`ErrForbidden` — the server actively rejected the token — means signed out. **Any other
error** means degraded: keep counting down from the cached expiry and dim the icon. These
are distinct states and must stay distinct. Conflating them tells someone with a perfectly
valid token that they have been logged out, every time their network hiccups.

### Credentials must not reach an error, a log, a URL, or a process argument

Three separate leaks of this class were found during development, each through a different
channel. If you add an HTTP call, you need all three defences:

- **Wrapped transport errors.** Go's `*url.Error` embeds the full request URL in its
  message, and our URLs carry authorization codes and usernames. Wrap every `client.Do`
  and `http.NewRequestWithContext` error through `sanitize()`.
- **Path interpolation.** A username goes into a request path. Unescaped, a value like
  `../../sys/mfa/validate` redirects a password to a different endpoint. It is both
  `url.PathEscape`d and rejected outright if it contains `/ \ ? #` or control characters.
- **Redirects.** A 307 re-sends the POST body — the password — to whatever host the
  redirect names, and a 302 sends a `Referer` carrying the authorization code. Every HTTP
  client sets `CheckRedirect: http.ErrUseLastResponse`.

Response bodies are not rendered to the user either, with one deliberate exception:
`ConfigProblem` carries the strings from OpenBao's `errors` array for the two failures a
user can actually fix (a rejected `auth_url`, an occupied callback port). Everything else
stays generic.

### Fail closed on platform errors

The IPv6 bind check allowlists the errnos that genuinely mean "this host has no IPv6" and
treats **everything else**, recognised or not, as fatal. This is not paranoia:
`syscall.EADDRINUSE` is a fabricated placeholder on Windows — the real Winsock error is
`10048` — so a blocklist would fail *open* there and hand the login callback to whatever
process was squatting `[::1]`.

### The Host guard is not CSRF protection

The loopback listener refuses requests whose `Host` is not its own address. That defends
against DNS rebinding **only** — a browser always sends the correct Host for the connection
it opened, so a cross-origin POST sails through it. The password form protects itself with
an unguessable nonce in its path plus a `Sec-Fetch-Site` check. Do not add a blanket
cross-site rejection: the OIDC provider's redirect is legitimately cross-site and would
break.

### Nothing writes to stderr and exits

Baobar is normally launched by double-click, where there is no terminal. A configuration
failure that logs and exits is indistinguishable from the app not starting. Failures render
in the tray. `cmd/baobar/main.go` has no `log` import and no `os.Exit`, and it should stay
that way.

### The UI never claims a state it did not reach

The "Start at login" checkbox reflects real on-disk state, re-read after every toggle — not
a remembered flag. Logout reports it if the server could not be reached, because the token
stays valid there until it expires. An early version reported success either way, which is
the specific kind of lie this project treats as a defect.

## Tests

The project is written test-first, and reviews check test *quality*, not just presence.

**A test that reimplements the code it tests protects nothing.** This happened twice here:
an escaping test that asserted against its own inline copy of the escaping, and a test for
the Windows browser bug that passed with the bug restored. Both were only caught because
someone broke the production code and watched the test still pass.

So: when you add a test for a behaviour, **break that behaviour and confirm the test
fails.** Then restore it. If the test still passes, it is not testing what you think.

A caution learned the hard way — if you do that, `git stash` your work or commit first.
`git checkout -- <file>` restores from HEAD and will silently discard uncommitted changes
along with your mutation.

## Style

Follow the surrounding code. `gofmt` is enforced in CI. Comments explain *why*, not what —
particularly for anything that looks arbitrary, because in this codebase it usually is not.

## Icons and packaging

### There are two icon families, and they are not the same asset

Confusing them is the easiest mistake to make here.

**Tray state icons** — `internal/tray/assets/*.png|.ico`, 22px, five of them, one per state.
Each has a distinct *shape* as well as a distinct colour, because on Windows the icon is the
entire state signal and roughly 8% of men cannot separate red from green. A test decodes
them and compares silhouettes, so a change that reduces two states to a difference in hue
fails the build. Regenerate with:

```bash
go run ./tools/genicons
```

**The application icon** — `assets/icon/`, one bao bun on a cream tile. This is what Finder,
Explorer, the Dock, Launchpad and the Linux applications grid show. It carries no state.
Never derive one family from the other: at 22px the app icon's ground and pleat detail turn
to mush, and the state shapes are the whole point.

### Changing the application icon

The design source is `assets/icon/baobar.svg`. Rendering it is a separate, manual step from
deriving the packaged formats, because rasterising an SVG needs a real renderer:

```bash
python3 tools/mkmaster.py     # SVG  -> baobar-1024.png, baobar-32.png, baobar-16.png
go run ./tools/genappicon     # PNGs -> baobar.icns, baobar.ico, hicolor/*
```

`mkmaster.py` needs Google Chrome and Pillow — neither is a project dependency, which is
exactly why its output is committed rather than built. Chrome does the rasterising because
the SVG carries its fills in a CSS `<style>` block, which the pure-Go rasterisers ignore;
they would render an unfilled shape and it would look like a problem with the artwork.

Three masters, not one: the mark is inset from the tile edge, and the 80% span that reads
well at 1024 wastes pixels at 16, where the bun ends up small inside its own tile. So 16 and
32 are composed separately with the mark spanning more of the tile, and `genappicon` prefers
a committed `baobar-<n>.png` over a downscale. An override whose dimensions do not match its
filename is ignored rather than trusted.

**Commit the generated output.** Nothing in the Go build references `assets/`, so a stale or
truncated icon would otherwise be discovered by `goversioninfo`, in CI, during a release. A
guard test in `tools/genappicon` decodes the committed `.ico` to move that failure earlier.

### The macOS bundle

`tools/macapp.sh` owns the whole chain and is deliberately outside `.goreleaser.yaml`: every
GoReleaser feature that would help — `app_bundles`, `dmg`, the cask `app:` stanza, native
notarization — is Pro-only, and hooks that run only inside a release cannot be tested. The
same commands run on a laptop and in CI.

```bash
go build -o dist/baobar-local ./cmd/baobar
./tools/macapp.sh bundle dist/baobar-local 0.1.6 dist
./tools/macapp.sh sign     dist/Baobar.app
./tools/macapp.sh notarize dist/Baobar.app 0.1.6 dist   # needs the Apple key
./tools/macapp.sh verify   dist/Baobar.app
```

`sign` needs the Developer ID in your keychain; `notarize` additionally needs
`APPLE_NOTARY_KEY_FILE`, `APPLE_NOTARY_KEY_ID` and `APPLE_NOTARY_ISSUER_ID`. The key is
`D99P3DSQVU` and lives in the Apple secrets vault, not on disk.

`verify` is the one that matters: before notarization `spctl` reports
`rejected / Unnotarized Developer ID`, and after stapling it must report
`accepted / Notarized Developer ID`. Check that flip rather than trusting the exit codes —
getting the staple ordering wrong produces an archive that passes notarization and still
warns users who are offline.

See `docs/NEXT.md` under "Releasing" for the per-release cask step, and
`docs/superpowers/plans/2026-08-18-macos-app-bundle.md` for why each piece is shaped the way
it is.

## Commits and PRs

Explain the reasoning, not just the change. If you are fixing something subtle, say what
would go wrong without the fix — the commit history here is one of the more useful pieces
of documentation the project has, and `git blame` should lead somewhere informative.

## Security

If you find a vulnerability, please report it privately through GitHub's security advisory
form rather than opening a public issue.

# Working on Baobar

A menu bar / system tray indicator for OpenBao: login state and a live token
countdown, on macOS, Windows and Linux. Go, no cgo except on macOS.

`CONTRIBUTING.md` is the authority on how this code works and why. This file
exists to name the constraints that are non-obvious enough to be broken by
accident, and to point at where the real explanation lives.

## Build, test, lint

```bash
go build ./... && go vet ./... && gofmt -l . && go test ./... -race
```

`gofmt -l` printing anything is a failure. CI runs all four on macOS, Ubuntu and
Windows, plus a cross-build job.

## Invariants that are easy to break

Each of these has a full explanation under "The invariants" in `CONTRIBUTING.md`.
Read that section before changing anything they touch.

- **The server is contacted at most once per recheck window.** Every
  `lookup-self` lands in the audit log. Polling faster buries the decrypt events
  the audit log exists to surface, which is why `BAOBAR_RECHECK` has a hard 60s
  floor. The countdown is recomputed locally every second and costs nothing — do
  not "fix" perceived staleness by shortening the window or removing the cache.
- **A network failure is not a logout.** An unreachable server means degraded,
  not signed out. Telling someone with a valid token that they are logged out is
  the worst thing this app can do.
- **Credentials must not reach an error, a log, a URL, or a process argument.**
- **Nothing writes to stderr and exits.** Baobar is normally launched by
  double-click, where there is no terminal to read. Configuration problems go to
  the tray, which names the problem and the config path.
- **The UI never claims a state it did not reach.** The "Start at login"
  checkbox reflects a real on-disk entry, not a remembered setting.

## The two icon families are different assets

- `internal/tray/assets/` — five 22px **state** glyphs. Each has a distinct
  *shape*, not just a distinct colour, because on Windows the icon is the entire
  state signal and red/green alone is unreadable for many people. A test compares
  silhouettes, so a change that reduces two states to a difference in hue fails
  the build. Regenerate: `go run ./tools/genicons`.
- `assets/icon/` — the **application** icon. Carries no state. Regenerate:
  `python3 tools/mkmaster.py` then `go run ./tools/genappicon`.

Never derive one from the other. Generated assets are committed; see "Icons and
packaging" in `CONTRIBUTING.md` for why, and for the macOS bundle chain in
`tools/macapp.sh`.

## Claims about verification

This project distinguishes *built*, *run* and *tested*, and says which is which —
see `docs/NEXT.md`, which tracks what has actually been exercised on real
hardware versus what only compiles. Keep that distinction when you add to it. If
something has not been run, say so rather than implying coverage.

## Where things are

`docs/NEXT.md` — current state, what is outstanding, repo layout, release steps.
`docs/superpowers/specs/` and `plans/` — the reasoning behind larger changes.
`docs/local/` — git-ignored, holds environment-specific values the public docs omit.

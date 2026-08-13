# Baobar

A menu bar / system tray indicator for [OpenBao](https://openbao.org): shows whether you
are signed in, how long your token has left, and gets you signed back in without opening a
terminal. macOS, Windows, and Linux.

```
🔓 6h19m          signed in, 6h19m remaining
🟠 22m            under 30 minutes left
🔒 login          not signed in
```

**Nothing is built yet.** This repo currently holds the design only.

- **Design:** [`docs/superpowers/specs/2026-08-13-baobar-design.md`](docs/superpowers/specs/2026-08-13-baobar-design.md)
- **M1 plan:** [`docs/superpowers/plans/2026-08-13-baobar-m1-indicator.md`](docs/superpowers/plans/2026-08-13-baobar-m1-indicator.md)
  — 10 tasks, TDD, start here
- **Working prototype it replaces:** `~/Source/st/aws/openbao/tools/bao-status.30s.sh` — a
  SwiftBar/xbar/Argos plugin that already does this on macOS and Linux. Baobar exists
  because Windows has no plugin host, so the app has to be its own.

Planned stack: Go + `systray`, one static binary per platform, no runtime dependency and
no dependency on the `bao` CLI.

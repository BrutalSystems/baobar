# Installing Baobar on Windows

Baobar sits in your system tray (the icons near the clock) and shows whether you
are signed in to OpenBao and how long your token has left. It has no window.

## Install

Pick whichever you already use.

**Chocolatey**

```powershell
choco install baobar
```

**winget**

```powershell
winget install BrutalSystems.baobar
```

**No package manager** — download `baobar_<version>_windows_amd64.zip` from
[the latest release](https://github.com/BrutalSystems/baobar/releases/latest) and
extract it somewhere permanent, such as `C:\Program Files\Baobar`.

> Do **not** leave it in Downloads or any temp folder. Windows empties those, and
> "Start at login" deliberately refuses to point at a location that will be
> cleared.

### A SmartScreen warning is expected

Windows builds are not yet code-signed, so on first run SmartScreen may say
"Windows protected your PC". Choose **More info → Run anyway**.

This is being fixed — see
[issue #11](https://github.com/BrutalSystems/baobar/issues/11).

## First run

1. Start **Baobar** from the Start Menu (or run `baobar.exe` if you used the zip).
2. A browser tab opens asking for your OpenBao server address. Enter it
   (for example `https://openbao.example.com`) and submit.
3. A bun icon appears in your system tray.

You only answer that once.

### If you cannot see the icon

**Windows hides new tray icons by default.** Click the **^** arrow next to the
clock — Baobar is probably there. To keep it visible permanently:

**Settings → Personalization → Taskbar → Other system tray icons**, then turn
**Baobar** on.

This is worth doing. On Windows the icon is the only thing Baobar can show you —
there is no text next to it as there is on a Mac.

## What the icon tells you

| Icon | Meaning |
|---|---|
| green filled circle | signed in |
| amber diamond | under 30 minutes left |
| red hollow ring | not signed in |
| grey hollow square | server unreachable |

Every state has its own shape as well as its own colour, so they are still
distinguishable if you cannot easily separate red from green.

**Hover over the icon** for the full status in words, for example
"OpenBao: signed in as you, 6h19m left".

## Signing in

Click the tray icon and choose either:

- **Login with SSO** — opens your browser and signs you in through your identity
  provider.
- **Login with password + TOTP** — opens a small form in your browser.

Both happen in the browser. You never need a command prompt, and Baobar does not
require the `bao` command line tool to be installed. The token it writes is the
same one the `bao` tool reads, so signing in through Baobar signs you in there
too.

## Start it automatically at login

Click the tray icon and tick **Start at login**. Untick it to stop.

This writes a real Windows startup entry for your account only — no administrator
rights needed — so the tick box always reflects what is actually set.

## Upgrading

```powershell
choco upgrade baobar
```

or

```powershell
winget upgrade BrutalSystems.baobar
```

Your server address, your login and your "Start at login" setting all carry over.

## Uninstalling

```powershell
choco uninstall baobar
```

That removes the program and its Start Menu entry. Your saved settings and your
OpenBao token stay on disk, because the token is shared with the `bao` command
line tool.

## If something looks wrong

**A ⚠️ icon.** Baobar could not read its settings. Click it — the menu names the
problem and the exact file it wants, which is
`%AppData%\baobar\config.toml`.

**It says you are signed out but you just signed in elsewhere.** Click **Refresh
now** in the menu. Baobar checks the server every five minutes by design, to keep
your audit log readable, so it can take a moment to notice.

**"Start at login" will not stay ticked.** Baobar refuses to set this when the
program is in a temporary folder, because the entry would break the next time
Windows clears it. Move `baobar.exe` somewhere permanent and try again.

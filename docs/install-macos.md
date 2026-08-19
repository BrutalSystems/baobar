# Installing Baobar on macOS

Baobar lives in your menu bar and shows whether you are signed in to OpenBao and
how long your token has left. It has no window and no Dock icon.

## Install

```sh
brew tap brutalsystems/tap
brew trust brutalsystems/tap
brew install --cask baobar
```

`brew trust` is needed once: Homebrew refuses casks from third-party taps until
you say you trust them.

No Homebrew? Download `Baobar-<version>-macOS.zip` from
[the latest release](https://github.com/BrutalSystems/baobar/releases/latest),
unzip it, and drag `Baobar.app` into your Applications folder.

## First run

1. Open **Baobar** from Spotlight or Launchpad.
2. A browser tab opens asking for your OpenBao server address. Enter it
   (for example `https://openbao.example.com`) and submit.
3. A bun icon appears in your menu bar, on the right-hand side.

You only answer that once. The address is saved and Baobar starts normally from
then on.

Baobar is signed and notarized by Apple, so there is no "unidentified developer"
warning and nothing to allow in System Settings.

## What the icon tells you

| Icon | Menu bar text | Meaning |
|---|---|---|
| green filled circle | `6h19m` | signed in, that much time left |
| amber diamond | `22m` | under 30 minutes left |
| red hollow ring | `login` | not signed in |
| grey hollow square | `~6h19m` | server unreachable, counting down from last known |

Every state has its own shape as well as its own colour.

## Signing in

Click the menu bar icon and choose either:

- **Login with SSO** — opens your browser and signs you in through your identity
  provider.
- **Login with password + TOTP** — opens a small form in your browser.

Both happen in the browser. You never need a terminal, and Baobar does not
require the `bao` command line tool to be installed.

## Start it automatically at login

Click the menu bar icon and tick **Start at login**. Untick it to stop.

This writes a real macOS login entry pointing at the app, so the tick box always
reflects what is actually set, not just what you last chose.

## Upgrading

```sh
brew upgrade --cask baobar
```

Your server address, your login and your "Start at login" setting all carry over.

### One extra step, once, if you upgraded to v0.1.7 with "Start at login" on

**Skip this if you never ticked "Start at login", or if you installed v0.1.7 or
later fresh.** Nothing is broken either way — this only affects how macOS
identifies the running app.

Before v0.1.7 Baobar was a plain command, and your login entry recorded the path
to it. The upgrade keeps that path working, but macOS will not connect the
running app to its new bundle when it is started that way: Baobar runs fine while
showing up as `baobar` with no version, rather than as **Baobar**.

To point your login entry at the app itself and restart it:

```sh
curl -fsSL https://raw.githubusercontent.com/BrutalSystems/baobar/main/tools/upgrade-macos-autostart.sh -o upgrade-baobar.sh
less upgrade-baobar.sh     # have a look before running it
sh upgrade-baobar.sh
```

It backs up your existing entry first, does nothing if it is already correct, and
tells you what it changed.

Prefer to do it by hand? Untick **Start at login** in the menu and tick it again.
Baobar rewrites the entry using wherever it is currently running from, which
achieves the same thing.

Tracked as [issue #22](https://github.com/BrutalSystems/baobar/issues/22) — once
that is fixed the step disappears.

## Uninstalling

```sh
brew uninstall --cask baobar
```

That removes the app and its login entry. Your saved address and your OpenBao
token stay on disk, because the token is shared with the `bao` command line tool
and removing it would sign you out there too.

To remove Baobar's own settings as well:

```sh
brew uninstall --zap --cask baobar
```

## If something looks wrong

**No icon in the menu bar.** If your menu bar is crowded, macOS may be hiding it.
Try quitting some other menu bar apps, or check whether Baobar is running with
Activity Monitor.

**A ⚠️ icon.** Baobar could not read its settings. Click it — the menu names the
problem and the exact file it wants.

**It says `login` but you just signed in elsewhere.** Click **Refresh now** in the
menu. Baobar checks the server every five minutes by design, to keep your audit
log readable, so it can take a moment to notice.

**"Start at login" will not stay ticked.** Baobar refuses to set this if the app
is somewhere temporary, like your Downloads folder, because the entry would break
at your next login. Move `Baobar.app` into Applications and try again.

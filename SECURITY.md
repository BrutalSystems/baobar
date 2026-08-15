# Security policy

## Reporting a vulnerability

Please report security issues privately through GitHub's
[private vulnerability reporting](https://github.com/BrutalSystems/baobar/security/advisories/new)
rather than opening a public issue.

Include what you did, what happened, and the platform. If a credential or token
appeared somewhere it should not have, say where you saw it — a log line, an
error message, a file on disk — and please redact the value itself.

Expect an acknowledgement within a week.

## Supported versions

The latest release. Baobar is pre-1.0; fixes land on `main` and go out in the
next tag rather than being backported.

## What Baobar handles

Baobar reads an OpenBao token, asks the server about it, and shows the result
in your menu bar. It also drives two login flows in your browser. That puts it
close to credential material, so the properties below are deliberate rather
than incidental — if you find one of them violated, that is a security bug
worth reporting.

- **The token is never written by Baobar.** It reads the token file that the
  `bao` CLI already maintains. The status cache it does write holds no token
  material.
- **Credentials must not reach error strings.** Three ways they have leaked
  before, each now guarded: Go's `*url.Error` embeds the full request URL; an
  unescaped username interpolated into a request path can redirect a password
  to another endpoint; and following an HTTP redirect re-sends the body, the
  token header, and a `Referer` carrying the auth code.
- **The loopback listener is short-lived** and bound to localhost for the
  duration of a login, not kept open.
- **The password form is protected by an unguessable nonce path** plus a
  `Sec-Fetch-Site` check. The Host guard defends against DNS rebinding; it is
  not CSRF protection, because a browser always sends the correct Host.
- **Login secrets never leave the process** except to the configured OpenBao
  server over the address you set.

## What is out of scope

- Anything requiring an attacker who already has local code execution as your
  user. Such an attacker can read the token file directly.
- The OpenBao server itself. Report those upstream.
- Unsigned release binaries triggering macOS Gatekeeper warnings. Known; see
  the README.

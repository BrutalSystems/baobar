// Command baobar shows OpenBao login status in the system tray.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/brutalsystems/baobar/internal/authflow"
	"github.com/brutalsystems/baobar/internal/autostart"
	"github.com/brutalsystems/baobar/internal/bao"
	"github.com/brutalsystems/baobar/internal/config"
	"github.com/brutalsystems/baobar/internal/notify"
	"github.com/brutalsystems/baobar/internal/tray"
)

func main() {
	// Baobar is usually launched by double-click, where there is no terminal to
	// read. Configuration problems therefore go to the tray, not to stderr —
	// exiting here would look identical to the app simply not starting.
	cfgPath, tokenPath, cachePath, err := config.DefaultPaths()
	if err != nil {
		tray.Run(tray.Options{ConfigError: "Cannot locate your home directory: " + err.Error()})
		return
	}

	cfg, err := config.Load(cfgPath, os.Getenv)
	if err != nil {
		tray.Run(tray.Options{ConfigError: fmt.Sprintf("%v (config: %s)", err, cfgPath)})
		return
	}

	client := bao.NewClient(cfg.Addr)
	poller := &bao.Poller{
		Client:    client,
		Addr:      cfg.Addr,
		TokenPath: tokenPath,
		CachePath: cachePath,
		Recheck:   cfg.Recheck,
		Warn:      cfg.Warn,
		Now:       time.Now,
	}
	tracker := notify.NewTracker(30*time.Minute, 5*time.Minute)

	starter, autostartErr := autostart.New()

	// One alert seam, used by logout, by the login flows, and by tray.Options.
	// M1 had this logic inline in the Options literal; the login flows need it
	// too, so it gets a name.
	alert := func(title, message string) { _ = notify.Send(title, message) }

	tray.Run(tray.Options{
		Addr:   cfg.Addr,
		Status: poller.Status,
		Logout: func() error {
			// The local session is cleared unconditionally — cache and token
			// file both go — but a failed revoke is reported rather than
			// swallowed: the token stays valid on the server until it
			// expires naturally, and the user deserves to know that.
			var revokeErr error
			token, err := bao.ReadToken(tokenPath)
			if err == nil {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				revokeErr = client.RevokeSelf(ctx, token)
			}
			_ = bao.DeleteCache(cachePath)
			if err := removeToken(tokenPath); err != nil {
				return err
			}
			return revokeErr
		},
		StartAtLoginEnabled: func() bool {
			if autostartErr != nil {
				return false
			}
			on, err := starter.Enabled()
			return err == nil && on
		},
		ToggleStartAtLogin: func(on bool) error {
			if autostartErr != nil {
				return autostartErr
			}
			if on {
				return starter.Enable()
			}
			return starter.Disable()
		},
		Login: func(method string) error {
			ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
			defer cancel()

			var token string
			var err error
			switch method {
			case "oidc":
				token, err = authflow.OIDC(ctx, authflow.OIDCConfig{
					Addr: cfg.Addr, Mount: cfg.OIDCMount, Role: cfg.OIDCRole,
					CallbackPort: cfg.CallbackPort, OpenURL: openURL,
				})
			default:
				token, err = authflow.UserpassBrowser(ctx, authflow.UserpassBrowserConfig{
					Userpass:        authflow.UserpassConfig{Addr: cfg.Addr, Mount: cfg.UserpassMount},
					DefaultUsername: cfg.Username,
					OpenURL:         openURL,
				})
			}
			// Failures go through the same alert seam as logout, rather than
			// calling notify.Send directly — one path, and an injectable one.
			if err != nil {
				if errors.Is(err, authflow.ErrBusy) {
					alert("OpenBao", "A login is already in progress.")
				} else {
					alert("OpenBao", "Login did not complete.")
				}
				return err
			}
			if err := bao.WriteToken(tokenPath, token); err != nil {
				alert("OpenBao", "Signed in, but the token could not be saved.")
				return err
			}
			// No explicit Force here on the poll goroutine's behalf beyond
			// this call: Poller's throttle state is mutex-guarded, so calling
			// Force from this login goroutine — rather than only via
			// tray.go's kick() — is safe (see bao.Poller's doc comment).
			poller.Force()
			return nil
		},
		Refresh:    func() { poller.Force() },
		Thresholds: tracker.Crossed,
		Notify: func(threshold time.Duration) {
			_ = notify.Send("OpenBao", fmt.Sprintf("Your token expires in less than %s", tray.Human(threshold)))
		},
		OpenURL: openURL,
		Alert:   alert,
	})
}

// browserCommand returns the command that opens url in the system browser.
// goos is a parameter so every platform's branch is testable from one machine.
func browserCommand(goos, url string) (string, []string) {
	switch goos {
	case "darwin":
		return "open", []string{url}
	case "windows":
		// rundll32 takes the URL as a single argument and does no shell
		// parsing, so query strings with & survive. `cmd /c start` does not:
		// cmd.exe re-parses the line and & terminates the command.
		return "rundll32", []string{"url.dll,FileProtocolHandler", url}
	default:
		return "xdg-open", []string{url}
	}
}

func openURL(url string) error {
	name, args := browserCommand(runtime.GOOS, url)
	return exec.Command(name, args...).Start()
}

func removeToken(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

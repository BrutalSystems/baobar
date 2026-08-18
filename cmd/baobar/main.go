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

	"github.com/BrutalSystems/baobar/internal/authflow"
	"github.com/BrutalSystems/baobar/internal/autostart"
	"github.com/BrutalSystems/baobar/internal/bao"
	"github.com/BrutalSystems/baobar/internal/config"
	"github.com/BrutalSystems/baobar/internal/notify"
	"github.com/BrutalSystems/baobar/internal/tray"
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
	if errors.Is(err, config.ErrNoAddr) {
		// First run. Ask in the browser rather than sending the user to a
		// terminal to hand-write TOML — the whole point of Baobar is that you
		// do not need one. Nothing is running yet, so there is no poller or
		// cached status to reconcile: on success we simply load again and
		// continue as though the file had been there all along.
		//
		// Any failure here — the user closes the tab, the browser will not
		// open, the write fails — falls through to the tray item below, which
		// still names the problem and the config path. First-run setup can
		// only improve on that path, never replace it.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		if _, serr := authflow.Setup(ctx, authflow.SetupConfig{
			OpenURL: openURL,
			Save:    func(addr string) error { return config.SaveAddr(cfgPath, addr) },
		}); serr == nil {
			cfg, err = config.Load(cfgPath, os.Getenv)
		}
		cancel()
	}
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
			return runLogin(ctx, method, loginDeps{
				oidc:     authflow.OIDC,
				userpass: authflow.UserpassBrowser,
				oidcConfig: authflow.OIDCConfig{
					Addr: cfg.Addr, Mount: cfg.OIDCMount, Role: cfg.OIDCRole,
					CallbackPort: cfg.CallbackPort, Prompt: cfg.OIDCPrompt,
					OpenURL: openURL,
				},
				userpassConfig: authflow.UserpassBrowserConfig{
					Userpass:        authflow.UserpassConfig{Addr: cfg.Addr, Mount: cfg.UserpassMount},
					DefaultUsername: cfg.Username,
					OpenURL:         openURL,
				},
				writeToken: func(token string) error { return bao.WriteToken(tokenPath, token) },
				// No explicit Force here on the poll goroutine's behalf
				// beyond this call: Poller's throttle state is
				// mutex-guarded, so calling Force from this login goroutine
				// — rather than only via tray.go's kick() — is safe (see
				// bao.Poller's doc comment).
				force: poller.Force,
				alert: alert,
			})
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

// loginDeps carries runLogin's dependencies as injected fields so the login
// flow — including its two failure alerts and its one success side effect —
// is reachable from a test without a live OpenBao server, a real browser, or
// a real autostart/token file. main wires these to authflow.OIDC,
// authflow.UserpassBrowser, bao.WriteToken, poller.Force, and the shared
// alert seam; tests wire fakes.
type loginDeps struct {
	oidc     func(context.Context, authflow.OIDCConfig) (string, error)
	userpass func(context.Context, authflow.UserpassBrowserConfig) (string, error)

	oidcConfig     authflow.OIDCConfig
	userpassConfig authflow.UserpassBrowserConfig

	writeToken func(token string) error
	force      func()
	alert      func(title, message string)
}

// runLogin drives one login attempt: it calls the configured authflow entry
// point for method, saves the resulting token, and forces a poller refresh.
// Every failure path alerts exactly once, through deps.alert, and the
// success path never alerts. writeToken failing does not call force — a
// token that was not actually saved must not make the tray look fresh.
func runLogin(ctx context.Context, method string, deps loginDeps) error {
	var token string
	var err error
	switch method {
	case "oidc":
		token, err = deps.oidc(ctx, deps.oidcConfig)
	default:
		token, err = deps.userpass(ctx, deps.userpassConfig)
	}
	// Failures go through the same alert seam as logout, rather than
	// calling notify.Send directly — one path, and an injectable one.
	if err != nil {
		var problem *authflow.ConfigProblem
		switch {
		case errors.Is(err, authflow.ErrBusy):
			deps.alert("OpenBao", "A login is already in progress.")
		case errors.As(err, &problem):
			// Configuration the user can fix, described by the server itself —
			// most often a redirect URI the OIDC role does not allow. Anything
			// else stays generic: a response body from a credential-bearing
			// request is not something to render into a notification.
			deps.alert("OpenBao login failed", problem.Error())
		default:
			deps.alert("OpenBao", "Login did not complete.")
		}
		return err
	}
	if err := deps.writeToken(token); err != nil {
		deps.alert("OpenBao", "Signed in, but the token could not be saved.")
		return err
	}
	deps.force()
	return nil
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

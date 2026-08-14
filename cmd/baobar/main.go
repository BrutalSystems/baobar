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

	"github.com/brutalsystems/baobar/internal/bao"
	"github.com/brutalsystems/baobar/internal/config"
	"github.com/brutalsystems/baobar/internal/login"
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
		TokenPath: tokenPath,
		CachePath: cachePath,
		Recheck:   cfg.Recheck,
		Warn:      cfg.Warn,
		Now:       time.Now,
	}
	tracker := notify.NewTracker(30*time.Minute, 5*time.Minute)

	tray.Run(tray.Options{
		Addr:         cfg.Addr,
		CLIAvailable: login.CLIAvailable(),
		Status:       poller.Status,
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
		Login: func(method string) error {
			// Force the next poll to hit the server as soon as login lands,
			// without discarding the cache: deleting it here would blow away
			// the only thing keeping a Degraded countdown alive if the server
			// is unreachable when the poll fires.
			poller.Force()
			return login.Launch(cfg.Addr, method)
		},
		Refresh:    func() { poller.Force() },
		Thresholds: tracker.Crossed,
		Notify: func(threshold time.Duration) {
			_ = notify.Send("OpenBao", fmt.Sprintf("Your token expires in less than %s", tray.Human(threshold)))
		},
		OpenURL: openURL,
		Alert: func(title, message string) {
			_ = notify.Send(title, message)
		},
	})
}

func openURL(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "windows":
		return exec.Command("cmd", "/c", "start", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}

func removeToken(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

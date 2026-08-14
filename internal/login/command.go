// Package login launches a terminal running `bao login`. This is M1's stopgap:
// M2 and M3 replace it with in-app auth, at which point the bao CLI stops being
// required for anything.
package login

import (
	"fmt"
	"os/exec"
	"runtime"

	"github.com/brutalsystems/baobar/internal/config"
)

const (
	MethodUserpass = "userpass"
	MethodOIDC     = "oidc"
)

// Command builds the terminal invocation for a platform. goos is a parameter
// rather than a build tag so every branch is testable from one machine.
func Command(goos, addr, method string) (string, []string, error) {
	switch method {
	case MethodUserpass, MethodOIDC:
	default:
		return "", nil, fmt.Errorf("unknown login method %q", method)
	}
	if err := config.ValidateAddr(addr); err != nil {
		return "", nil, fmt.Errorf("refusing to launch a login for %q: %w", addr, err)
	}

	baoCmd := fmt.Sprintf("bao login -method=%s", method)

	switch goos {
	case "darwin":
		script := fmt.Sprintf("export VAULT_ADDR=%s; %s", addr, baoCmd)
		return "osascript", []string{
			"-e", fmt.Sprintf("tell application \"Terminal\" to do script \"%s\"", script),
			"-e", "tell application \"Terminal\" to activate",
		}, nil

	case "windows":
		// `start` opens a new console; PowerShell keeps it open for the prompt.
		ps := fmt.Sprintf("$env:VAULT_ADDR='%s'; %s", addr, baoCmd)
		return "cmd", []string{"/c", "start", "powershell", "-NoExit", "-Command", ps}, nil

	case "linux":
		script := fmt.Sprintf("VAULT_ADDR=%s %s; exec $SHELL", addr, baoCmd)
		return "x-terminal-emulator", []string{"-e", "sh", "-c", script}, nil

	default:
		return "", nil, fmt.Errorf("unsupported OS %q", goos)
	}
}

// Launch starts the login terminal and returns without waiting for it.
func Launch(addr, method string) error {
	name, args, err := Command(runtime.GOOS, addr, method)
	if err != nil {
		return err
	}
	return exec.Command(name, args...).Start()
}

// CLIAvailable reports whether `bao` is on PATH. M1 cannot log in without it,
// and on Windows it is frequently absent — the tray checks this so the login
// items can explain themselves instead of opening a console that fails.
func CLIAvailable() bool { return cliAvailable(exec.LookPath) }

func cliAvailable(look func(string) (string, error)) bool {
	_, err := look("bao")
	return err == nil
}

package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/BrutalSystems/baobar/internal/authflow"
)

// interpreters that re-parse their command line: passing an OIDC auth_url as
// a single exec.Command argument is not enough if the command itself is one
// of these, because THEY are what re-splits on & (or other shell
// metacharacters), not exec.Command. exec.Command never parses or splits its
// arguments — that's the whole reason "single argument" was insufficient
// on its own to guard the Windows bug this test exists to catch.
var reparsingInterpreters = map[string]bool{
	"cmd": true, "cmd.exe": true,
	"sh": true, "bash": true,
	"powershell": true, "powershell.exe": true,
}

// An OIDC auth_url always carries & between its query parameters. Whatever
// command we build must pass it through as ONE argument, unsplit, AND that
// command must not be an interpreter that re-parses its own command line —
// `cmd /c start <url>` satisfies the first property (exec.Command never
// splits arguments) while still truncating the URL at the first & once
// cmd.exe gets hold of it. That exact form is what shipped on Windows before
// this task; a test that only checks "one argument" does not catch it.
func TestBrowserCommandKeepsQueryStringIntact(t *testing.T) {
	const raw = "https://idp.example.com/authorize?client_id=abc&state=xyz&nonce=123"

	wantName := map[string]string{
		"darwin":  "open",
		"windows": "rundll32",
		"linux":   "xdg-open",
	}

	for _, goos := range []string{"darwin", "windows", "linux"} {
		name, args := browserCommand(goos, raw)
		if name == "" {
			t.Fatalf("%s: no command", goos)
		}
		if name != wantName[goos] {
			t.Errorf("%s: command = %q, want %q", goos, name, wantName[goos])
		}
		if reparsingInterpreters[name] {
			t.Errorf("%s: command %q re-parses its command line; & in the URL would truncate it", goos, name)
		}

		var found bool
		for _, a := range args {
			if a == raw {
				found = true
			}
			if strings.Contains(a, "&") && a != raw {
				t.Errorf("%s: argument %q splits or mangles the URL", goos, a)
			}
			if a == "/c" || a == "-c" {
				t.Errorf("%s: argument %q hands the URL to a re-parsed shell command line", goos, a)
			}
		}
		if !found {
			t.Errorf("%s: the URL is not passed as a single argument: %v", goos, args)
		}
	}
}

// runLoginFake wires loginDeps to in-memory fakes and records what was
// called, so runLogin's alert/force/writeToken discipline can be pinned
// without a live OpenBao server or a real browser.
type runLoginFake struct {
	oidcErr, userpassErr  error
	oidcToken, upassToken string
	writeTokenErr         error
	writeTokenCalledWith  string
	forceCalled           bool
	alerts                []string
}

func (f *runLoginFake) deps() loginDeps {
	return loginDeps{
		oidc: func(context.Context, authflow.OIDCConfig) (string, error) {
			return f.oidcToken, f.oidcErr
		},
		userpass: func(context.Context, authflow.UserpassBrowserConfig) (string, error) {
			return f.upassToken, f.userpassErr
		},
		writeToken: func(token string) error {
			f.writeTokenCalledWith = token
			return f.writeTokenErr
		},
		force: func() { f.forceCalled = true },
		alert: func(title, message string) { f.alerts = append(f.alerts, title+": "+message) },
	}
}

func TestRunLoginErrBusyAlertsExactlyOnce(t *testing.T) {
	f := &runLoginFake{userpassErr: authflow.ErrBusy}

	err := runLogin(context.Background(), "userpass", f.deps())

	if !errors.Is(err, authflow.ErrBusy) {
		t.Fatalf("err = %v, want authflow.ErrBusy", err)
	}
	if len(f.alerts) != 1 {
		t.Fatalf("alerts = %v, want exactly one", f.alerts)
	}
	if f.forceCalled {
		t.Error("force was called on a failed login")
	}
	if f.writeTokenCalledWith != "" {
		t.Error("writeToken was called on a failed login")
	}
}

func TestRunLoginGenericFailureAlertsExactlyOnce(t *testing.T) {
	f := &runLoginFake{oidcErr: errors.New("identity provider returned an error")}

	err := runLogin(context.Background(), "oidc", f.deps())

	if err == nil || errors.Is(err, authflow.ErrBusy) {
		t.Fatalf("err = %v, want a non-ErrBusy failure", err)
	}
	if len(f.alerts) != 1 {
		t.Fatalf("alerts = %v, want exactly one", f.alerts)
	}
	if f.forceCalled {
		t.Error("force was called on a failed login")
	}
}

func TestRunLoginWriteTokenFailureAlertsOnceAndSkipsForce(t *testing.T) {
	f := &runLoginFake{
		upassToken:    "hvs.token",
		writeTokenErr: errors.New("disk full"),
	}

	err := runLogin(context.Background(), "userpass", f.deps())

	if err == nil {
		t.Fatal("err = nil, want the writeToken failure")
	}
	if len(f.alerts) != 1 {
		t.Fatalf("alerts = %v, want exactly one", f.alerts)
	}
	if f.forceCalled {
		t.Error("force was called even though the token was never saved")
	}
	if f.writeTokenCalledWith != "hvs.token" {
		t.Errorf("writeToken called with %q, want the token from the login", f.writeTokenCalledWith)
	}
}

func TestRunLoginSuccessWritesTokenForcesAndNeverAlerts(t *testing.T) {
	f := &runLoginFake{oidcToken: "hvs.oidc-token"}

	err := runLogin(context.Background(), "oidc", f.deps())

	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if f.writeTokenCalledWith != "hvs.oidc-token" {
		t.Errorf("writeToken called with %q, want the token from the login", f.writeTokenCalledWith)
	}
	if !f.forceCalled {
		t.Error("force was not called on a successful login")
	}
	if len(f.alerts) != 0 {
		t.Errorf("alerts = %v, want none on success", f.alerts)
	}
}

// A configuration failure the server described — an unauthorized redirect URI
// is the likely first-run case — must reach the user, not be flattened into
// "Login did not complete." That message told the user nothing actionable.
func TestRunLoginSurfacesAConfigProblem(t *testing.T) {
	var alerts []string
	deps := loginDeps{
		oidc: func(ctx context.Context, _ authflow.OIDCConfig) (string, error) {
			return "", &authflow.ConfigProblem{
				Detail: "unauthorized redirect_uri: http://127.0.0.1:8250/oidc/callback",
				Status: 400,
			}
		},
		alert:      func(title, msg string) { alerts = append(alerts, title+": "+msg) },
		writeToken: func(string) error { t.Fatal("must not write a token"); return nil },
		force:      func() { t.Fatal("must not force a poll") },
	}

	if err := runLogin(context.Background(), "oidc", deps); err == nil {
		t.Fatal("expected an error")
	}
	if len(alerts) != 1 {
		t.Fatalf("alerts = %v, want exactly one", alerts)
	}
	if !strings.Contains(alerts[0], "unauthorized redirect_uri") {
		t.Errorf("alert = %q, want it to name the server's reason", alerts[0])
	}
}

// Everything that is not a ConfigProblem stays generic, so no response body
// from a credential-bearing request can reach a notification.
func TestRunLoginKeepsOtherFailuresGeneric(t *testing.T) {
	var alerts []string
	deps := loginDeps{
		oidc: func(ctx context.Context, _ authflow.OIDCConfig) (string, error) {
			return "", errors.New("password=hunter2 leaked into an error somehow")
		},
		alert:      func(title, msg string) { alerts = append(alerts, title+": "+msg) },
		writeToken: func(string) error { return nil },
		force:      func() {},
	}

	runLogin(context.Background(), "oidc", deps)

	if len(alerts) != 1 {
		t.Fatalf("alerts = %v, want exactly one", alerts)
	}
	if strings.Contains(alerts[0], "hunter2") {
		t.Errorf("alert = %q — a non-ConfigProblem error must not be surfaced", alerts[0])
	}
	if !strings.Contains(alerts[0], "Login did not complete") {
		t.Errorf("alert = %q, want the generic message", alerts[0])
	}
}

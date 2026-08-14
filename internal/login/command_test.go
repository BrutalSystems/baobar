package login

import (
	"errors"
	"strings"
	"testing"
)

func TestCommandPerPlatform(t *testing.T) {
	tests := []struct {
		goos     string
		wantName string
		wantIn   string
	}{
		{"darwin", "osascript", "do script"},
		{"windows", "cmd", "bao login"},
		{"linux", "x-terminal-emulator", "bao login"},
	}

	for _, tc := range tests {
		t.Run(tc.goos, func(t *testing.T) {
			name, args, err := Command(tc.goos, "https://bao.example.com", MethodUserpass)
			if err != nil {
				t.Fatalf("Command: %v", err)
			}
			if name != tc.wantName {
				t.Errorf("name = %q, want %q", name, tc.wantName)
			}
			joined := strings.Join(args, " ")
			if !strings.Contains(joined, tc.wantIn) {
				t.Errorf("args = %q, want it to contain %q", joined, tc.wantIn)
			}
			if !strings.Contains(joined, "https://bao.example.com") {
				t.Errorf("args = %q, missing the address", joined)
			}
			if !strings.Contains(joined, "-method=userpass") {
				t.Errorf("args = %q, missing the method", joined)
			}
		})
	}
}

func TestCommandOIDCMethod(t *testing.T) {
	_, args, err := Command("darwin", "https://bao.example.com", MethodOIDC)
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	if !strings.Contains(strings.Join(args, " "), "-method=oidc") {
		t.Errorf("args = %q, want -method=oidc", args)
	}
}

func TestCommandRejectsUnknownMethod(t *testing.T) {
	if _, _, err := Command("darwin", "https://bao.example.com", "telepathy"); err == nil {
		t.Fatal("expected an error for an unknown method")
	}
}

func TestCommandRejectsUnknownOS(t *testing.T) {
	if _, _, err := Command("plan9", "https://bao.example.com", MethodUserpass); err == nil {
		t.Fatal("expected an error for an unsupported OS")
	}
}

// config.Load already rejects these, but this is the string that reaches a
// shell, so it refuses defensively too.
func TestCommandRejectsUnsafeAddr(t *testing.T) {
	unsafeAddrs := []string{
		"https://x.com>out",
		"https://x.com<in",
		"https://x.com;id",
		"https://x.com|id",
		"https://x.com&id",
		"https://x.com`id`",
		"https://x.com$(id)",
		"https://x.com ext",
		"https://x.com~root",
		"https://x.com(sub)",
		"https://x.com*",
		"https://x.com\\bad",
		"https://x.com\"; rm -rf ~",
	}

	for _, addr := range unsafeAddrs {
		t.Run(addr, func(t *testing.T) {
			name, args, err := Command("darwin", addr, MethodUserpass)
			if err == nil {
				t.Fatalf("expected an error for addr %q", addr)
			}
			// Verify the address does not appear in the command
			joined := strings.Join(args, " ")
			if strings.Contains(joined, addr) {
				t.Errorf("unsafe addr %q appeared in command: %s %s", addr, name, joined)
			}
		})
	}
}

func TestCLIAvailable(t *testing.T) {
	found := func(string) (string, error) { return "/opt/homebrew/bin/bao", nil }
	missing := func(string) (string, error) { return "", errors.New("executable file not found in $PATH") }

	if !cliAvailable(found) {
		t.Error("cliAvailable = false when bao is on PATH")
	}
	if cliAvailable(missing) {
		t.Error("cliAvailable = true when bao is absent")
	}
}

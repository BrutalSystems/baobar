package autostart

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestFileAutostart(t *testing.T) (*fileAutostart, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "baobar.entry")
	return &fileAutostart{
		path: path,
		exe:  func() (string, error) { return "/opt/baobar", nil },
		render: func(exe string) []byte {
			return []byte("run " + exe + "\n")
		},
	}, path
}

func TestEnableCreatesTheEntryAndReportsEnabled(t *testing.T) {
	a, path := newTestFileAutostart(t)

	if on, err := a.Enabled(); err != nil || on {
		t.Fatalf("Enabled() = %v, %v — want false before enabling", on, err)
	}
	if err := a.Enable(); err != nil {
		t.Fatalf("Enable: %v", err)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("entry not written: %v", err)
	}
	if !strings.Contains(string(b), "/opt/baobar") {
		t.Errorf("entry does not reference the executable: %s", b)
	}

	if on, err := a.Enabled(); err != nil || !on {
		t.Errorf("Enabled() = %v, %v — want true after enabling", on, err)
	}
}

func TestDisableRemovesTheEntryAndIsIdempotent(t *testing.T) {
	a, path := newTestFileAutostart(t)
	if err := a.Enable(); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if err := a.Disable(); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("entry still present after Disable")
	}
	if err := a.Disable(); err != nil {
		t.Errorf("second Disable: %v — must be idempotent", err)
	}
}

// Enabling can fail (read-only home, sandbox). It must report the failure
// rather than leaving the UI claiming a state that was never achieved.
func TestEnableFailureIsReportedAndLeavesItDisabled(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocked")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	a := &fileAutostart{
		path:   filepath.Join(blocker, "baobar.entry"), // parent is a file
		exe:    func() (string, error) { return "/opt/baobar", nil },
		render: func(exe string) []byte { return []byte(exe) },
	}

	if err := a.Enable(); err == nil {
		t.Fatal("expected Enable to fail when the entry cannot be written")
	}
	if on, _ := a.Enabled(); on {
		t.Error("Enabled() reports true after a failed Enable")
	}
}

func TestNewReturnsSomethingUsable(t *testing.T) {
	a, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := a.Enabled(); err != nil {
		t.Errorf("Enabled: %v", err)
	}
}

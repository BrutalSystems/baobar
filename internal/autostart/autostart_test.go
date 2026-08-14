package autostart

import (
	"bytes"
	"encoding/xml"
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
	if on, err := a.Enabled(); err != nil || !on {
		t.Errorf("Enabled() = %v, %v — want true after Enable", on, err)
	}
	if err := a.Disable(); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("entry still present after Disable")
	}
	if on, err := a.Enabled(); err != nil || on {
		t.Errorf("Enabled() = %v, %v — want false after Disable", on, err)
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

func TestEnableReplacesPreviousEntry(t *testing.T) {
	a, path := newTestFileAutostart(t)

	// Enable with first exe
	a.exe = func() (string, error) { return "/path/one", nil }
	if err := a.Enable(); err != nil {
		t.Fatalf("first Enable: %v", err)
	}

	// Enable with second exe
	a.exe = func() (string, error) { return "/path/two", nil }
	if err := a.Enable(); err != nil {
		t.Fatalf("second Enable: %v", err)
	}

	// Verify file contains only the second path
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	content := string(b)
	if !strings.Contains(content, "/path/two") {
		t.Errorf("entry does not contain second path: %s", content)
	}
	if strings.Count(content, "/path/one") > 0 {
		t.Errorf("entry still contains first path after replacement: %s", content)
	}
}

func TestDarwinPlistEscapesXML(t *testing.T) {
	// Test the actual renderPlist function with dangerous characters
	dangerousExe := "/Applications/My & Co/baobar<script>"
	b := renderPlist(label, dangerousExe)

	content := string(b)

	// Should contain escaped entities, not raw dangerous characters
	if strings.Contains(content, "&") && !strings.Contains(content, "&amp;") {
		t.Error("& not properly escaped in plist")
	}
	if strings.Contains(content, "<script>") {
		t.Error("< not properly escaped in plist")
	}

	// Verify plist is valid XML by checking it can be unmarshaled
	var d struct{}
	decoder := xml.NewDecoder(bytes.NewReader(b))
	if err := decoder.Decode(&d); err != nil {
		t.Errorf("rendered plist is not valid XML: %v", err)
	}
}

func TestLinuxDesktopEscapesShell(t *testing.T) {
	// Test the actual renderDesktop function with dangerous characters
	dangerousExe := `/opt/My App/baobar$SHELL`
	b := renderDesktop(dangerousExe)

	content := string(b)

	// Should be quoted
	if !strings.Contains(content, `Exec="`) {
		t.Error("Exec value not quoted")
	}

	// Should have space inside quotes (not split by space)
	if !strings.Contains(content, `"`) {
		t.Error("Exec value not properly quoted for spaces")
	}

	// $ should be escaped
	if !strings.Contains(content, `\$SHELL`) {
		t.Error("$ not properly escaped in desktop file")
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

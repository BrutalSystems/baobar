package autostart

import (
	"bytes"
	"encoding/xml"
	"errors"
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

// The bug this guards: the entry records an absolute path at Enable() time,
// and nothing keeps that path valid afterwards. A build in /tmp that macOS
// later purges leaves a well-formed plist pointing at nothing — launchd
// fails the spawn with EX_CONFIG and the checkbox still reads "on". Existence
// of the entry is not the state the user cares about; the program being
// there to run is.
func TestEnabledIsFalseWhenTheRecordedProgramIsGone(t *testing.T) {
	dir := t.TempDir()
	program := filepath.Join(dir, "baobar")
	if err := os.WriteFile(program, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	a := &fileAutostart{
		path:   filepath.Join(dir, "entry"),
		exe:    func() (string, error) { return program, nil },
		render: func(exe string) []byte { return []byte("run " + exe + "\n") },
		target: func(entry []byte) (string, bool) {
			return strings.TrimSpace(strings.TrimPrefix(string(entry), "run ")), true
		},
	}

	// Written directly rather than through Enable: t.TempDir() is itself a
	// volatile location, which Enable now refuses by design. The subject here
	// is Enabled, so the entry is staged the way a past Enable would have
	// left it, using the same renderer.
	if err := os.WriteFile(a.path, a.render(program), 0o644); err != nil {
		t.Fatal(err)
	}
	if on, err := a.Enabled(); err != nil || !on {
		t.Fatalf("Enabled() = %v, %v — want true while the program exists", on, err)
	}

	// The program goes away; the entry does not.
	if err := os.Remove(program); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(a.path); err != nil {
		t.Fatalf("entry should still exist: %v", err)
	}

	on, err := a.Enabled()
	if err != nil {
		t.Fatalf("Enabled: %v", err)
	}
	if on {
		t.Error("Enabled() = true for an entry whose program no longer exists")
	}
}

// Recording a path under a temp directory produces exactly the failure above,
// so it is refused at the point the user asks for it, while there is still a
// human present to be told why.
func TestEnableRefusesAVolatileExecutablePath(t *testing.T) {
	volatilePaths := []string{
		"/tmp/baobar",
		"/private/tmp/baobar",
		"/var/folders/xy/T/baobar",
		`C:\Users\me\AppData\Local\Temp\baobar.exe`,
		"/var/folders/1z/go-build123/b001/exe/baobar",
	}

	for _, exe := range volatilePaths {
		t.Run(exe, func(t *testing.T) {
			dir := t.TempDir()
			a := &fileAutostart{
				path:   filepath.Join(dir, "entry"),
				exe:    func() (string, error) { return exe, nil },
				render: func(exe string) []byte { return []byte(exe) },
			}

			err := a.Enable()
			if err == nil {
				t.Fatalf("Enable accepted a volatile path %q", exe)
			}
			if !errors.Is(err, ErrVolatilePath) {
				t.Errorf("error = %v — want it to wrap ErrVolatilePath", err)
			}
			if _, statErr := os.Stat(a.path); !os.IsNotExist(statErr) {
				t.Error("an entry was written despite the refusal")
			}
			if on, _ := a.Enabled(); on {
				t.Error("Enabled() reports true after a refused Enable")
			}
		})
	}
}

func TestEnableAcceptsAStableExecutablePath(t *testing.T) {
	stablePaths := []string{
		"/usr/local/bin/baobar",
		"/Applications/Baobar.app/Contents/MacOS/baobar",
		"/home/me/bin/baobar",
		`C:\Program Files\Baobar\baobar.exe`,
		"/Users/me/go/bin/baobar",
	}

	for _, exe := range stablePaths {
		t.Run(exe, func(t *testing.T) {
			dir := t.TempDir()
			a := &fileAutostart{
				path:   filepath.Join(dir, "entry"),
				exe:    func() (string, error) { return exe, nil },
				render: func(exe string) []byte { return []byte(exe) },
			}
			if err := a.Enable(); err != nil {
				t.Fatalf("Enable rejected a stable path %q: %v", exe, err)
			}
		})
	}
}

// The extractors are what make Enabled() honest on each platform, so they are
// tested against the real renderers rather than against a hand-written sample
// that could drift from what Enable actually writes.
func TestPlistTargetReadsBackWhatRenderPlistWrote(t *testing.T) {
	for _, exe := range []string{
		"/usr/local/bin/baobar",
		"/Applications/My & Co/baobar",
		"/opt/a<b>c/baobar",
	} {
		got, ok := plistTarget(renderPlist(label, exe))
		if !ok {
			t.Errorf("plistTarget could not read back %q", exe)
			continue
		}
		if got != exe {
			t.Errorf("plistTarget = %q, want %q", got, exe)
		}
	}
}

func TestDesktopTargetReadsBackWhatRenderDesktopWrote(t *testing.T) {
	for _, exe := range []string{
		"/usr/local/bin/baobar",
		"/opt/My App/baobar",
		`/opt/weird$SHELL/baobar`,
		`/opt/quo"te/baobar`,
	} {
		got, ok := desktopTarget(renderDesktop(exe))
		if !ok {
			t.Errorf("desktopTarget could not read back %q", exe)
			continue
		}
		if got != exe {
			t.Errorf("desktopTarget = %q, want %q", got, exe)
		}
	}
}

// The Windows Run value is written quoted (a path with a space is otherwise
// split into argv tokens). Enabled has to undo that before it can stat the
// program, so the inverse is kept next to the renderers and tested from any
// platform — the registry itself cannot be exercised off Windows.
func TestRegistryTargetUnquotes(t *testing.T) {
	for _, tc := range []struct{ value, want string }{
		{`"C:\Program Files\Baobar\baobar.exe"`, `C:\Program Files\Baobar\baobar.exe`},
		{`C:\Baobar\baobar.exe`, `C:\Baobar\baobar.exe`},
	} {
		got, ok := registryTarget(tc.value)
		if !ok || got != tc.want {
			t.Errorf("registryTarget(%q) = %q, %v — want %q, true", tc.value, got, ok, tc.want)
		}
	}
	if _, ok := registryTarget(""); ok {
		t.Error("registryTarget(\"\") = ok — an empty value names no program")
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

func TestRenderDesktopIncludesIconKey(t *testing.T) {
	entry := string(renderDesktop("/usr/bin/baobar"))
	if !strings.Contains(entry, "\nIcon=baobar\n") {
		t.Errorf("autostart entry has no Icon= key:\n%s", entry)
	}
}

// TestRefusesTranslocatedPath pins a macOS behaviour the volatile-path guard
// was not written for but does cover. Gatekeeper runs a quarantined app from a
// randomised read-only copy under AppTranslocation rather than from where the
// user put it — so os.Executable reports a path that vanishes. Enabling "Start
// at login" there would write a LaunchAgent pointing into a directory that will
// not exist at the next login: the exact failure the guard exists to prevent.
//
// The path below is a real one, observed launching a notarized Baobar.app from
// /tmp with the quarantine attribute set.
func TestRefusesTranslocatedPath(t *testing.T) {
	p := "/private/var/folders/n_/7mzxv0p52w5c61bgnxntljwh0000gn/T/AppTranslocation/96266093-239C-4DAD-A24E-C6D19CB0AE2A/d/Baobar.app/Contents/MacOS/baobar"
	err := checkStablePath(p)
	if err == nil {
		t.Fatal("checkStablePath accepted a translocated path")
	}
	if !errors.Is(err, ErrVolatilePath) {
		t.Fatalf("wrong error for a translocated path: %v", err)
	}
	if !strings.Contains(err.Error(), "/Applications") {
		t.Errorf("a bundle path should be told to move to /Applications, got: %v", err)
	}
}

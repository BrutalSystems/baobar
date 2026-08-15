package bao

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestReadTokenMissingFile(t *testing.T) {
	_, err := ReadToken(filepath.Join(t.TempDir(), "absent"))
	if !errors.Is(err, ErrNoToken) {
		t.Fatalf("err = %v, want ErrNoToken", err)
	}
}

func TestReadTokenTrimsWhitespace(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".vault-token")
	os.WriteFile(p, []byte("  hvs.CAESIabc123\n"), 0o600)

	got, err := ReadToken(p)
	if err != nil {
		t.Fatalf("ReadToken: %v", err)
	}
	if got != "hvs.CAESIabc123" {
		t.Errorf("token = %q, want the trimmed value", got)
	}
}

func TestReadTokenTreatsBlankFileAsAbsent(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".vault-token")
	os.WriteFile(p, []byte("\n  \n"), 0o600)

	if _, err := ReadToken(p); !errors.Is(err, ErrNoToken) {
		t.Fatalf("err = %v, want ErrNoToken", err)
	}
}

func TestWriteTokenIsPrivate(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".vault-token")
	if err := WriteToken(p, "hvs.new"); err != nil {
		t.Fatalf("WriteToken: %v", err)
	}
	got, err := ReadToken(p)
	if err != nil || got != "hvs.new" {
		t.Fatalf("round trip = %q, %v", got, err)
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(p)
		if perm := fi.Mode().Perm(); perm != 0o600 {
			t.Errorf("mode = %o, want 600", perm)
		}
	}
}

// A pre-existing, world-readable token file must end up private after a
// write: os.WriteFile only applies its mode bits when it creates the file,
// so a naive truncate-then-write would leave a world-readable
// ~/.vault-token world-readable after a login. WriteToken must instead
// write-then-rename, which always produces a fresh file with the right mode
// regardless of what was there before.
func TestWriteTokenTightensPermissionsOnAnExistingFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".vault-token")
	if err := os.WriteFile(p, []byte("hvs.old"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	if err := WriteToken(p, "hvs.new"); err != nil {
		t.Fatalf("WriteToken: %v", err)
	}

	got, err := ReadToken(p)
	if err != nil || got != "hvs.new" {
		t.Fatalf("round trip = %q, %v", got, err)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatalf("Stat: %v", err)
		}
		if perm := fi.Mode().Perm(); perm != 0o600 {
			t.Errorf("mode = %o, want 600 even though the file pre-existed as 0644", perm)
		}
	}
}

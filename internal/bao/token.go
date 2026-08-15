package bao

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// ErrNoToken means there is no usable token on disk — the user is signed out.
var ErrNoToken = errors.New("no token file")

// ReadToken reads the token the bao CLI and SOPS also use. Do not relocate this
// file: interoperability with those tools is the point.
func ReadToken(path string) (string, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrNoToken
	}
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(b))
	if token == "" {
		return "", ErrNoToken
	}
	return token, nil
}

// WriteToken stores a token for the CLI and SOPS to pick up.
//
// It writes to a temporary file in the same directory and renames it over
// path, rather than truncating path in place, for two reasons: os.WriteFile
// only applies its mode bits when creating a file, so a truncate-then-write
// against a pre-existing world-readable ~/.vault-token would leave it
// world-readable after a login; and a crash mid-write to path directly would
// leave a truncated, corrupt token behind. os.CreateTemp opens with 0o600 up
// front (O_CREATE|O_EXCL, mode 0600), so the permission is right from the
// temp file's first byte, and the rename is atomic on every platform this
// app targets.
func WriteToken(path, token string) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".vault-token-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	// Any failure past this point must not leave the temp file behind.
	success := false
	defer func() {
		if !success {
			os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.WriteString(token); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	success = true
	return nil
}

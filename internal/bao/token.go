package bao

import (
	"errors"
	"os"
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

// WriteToken stores a token for the CLI and SOPS to pick up. Unused in M1;
// M2 and M3 call it after an in-app login.
func WriteToken(path, token string) error {
	return os.WriteFile(path, []byte(token), 0o600)
}

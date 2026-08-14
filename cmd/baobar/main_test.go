package main

import (
	"strings"
	"testing"
)

// An OIDC auth_url always carries & between its query parameters. Whatever
// command we build must pass it through as ONE argument, unsplit.
func TestBrowserCommandKeepsQueryStringIntact(t *testing.T) {
	const raw = "https://idp.example.com/authorize?client_id=abc&state=xyz&nonce=123"

	for _, goos := range []string{"darwin", "windows", "linux"} {
		name, args := browserCommand(goos, raw)
		if name == "" {
			t.Fatalf("%s: no command", goos)
		}
		var found bool
		for _, a := range args {
			if a == raw {
				found = true
			}
			if strings.Contains(a, "&") && a != raw {
				t.Errorf("%s: argument %q splits or mangles the URL", goos, a)
			}
		}
		if !found {
			t.Errorf("%s: the URL is not passed as a single argument: %v", goos, args)
		}
	}
}

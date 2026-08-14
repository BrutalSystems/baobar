package bao

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCacheRoundTripCreatesParentDir(t *testing.T) {
	p := filepath.Join(t.TempDir(), "nested", "status.json")
	in := Cache{CheckedAt: 1000, ExpiresAt: 2000, Name: "userpass-dev", Policies: []string{"admin"}}

	if err := SaveCache(p, in); err != nil {
		t.Fatalf("SaveCache: %v", err)
	}
	got, ok := LoadCache(p)
	if !ok {
		t.Fatal("LoadCache: ok = false")
	}
	if got.ExpiresAt != 2000 || got.Name != "userpass-dev" || len(got.Policies) != 1 {
		t.Errorf("round trip = %+v", got)
	}
}

// The cache must never contain token material. Deleting it is always safe.
func TestCacheHoldsNoTokenMaterial(t *testing.T) {
	p := filepath.Join(t.TempDir(), "status.json")
	SaveCache(p, Cache{
		CheckedAt: 1000,
		ExpiresAt: 2000,
		Name:      "userpass-dev",
		Token:     TokenStamp{ModTime: 1_700_000_000_000_000_000, Size: 95},
	})

	b, _ := os.ReadFile(p)

	// Check for actual token material patterns (not just the word "token").
	for _, field := range []string{"hvs.", "client_token"} {
		if strings.Contains(string(b), field) {
			t.Errorf("cache file contains %q: %s", field, b)
		}
	}

	// Guard against future fields that might accidentally carry secrets:
	// assert the exact key set of the cache JSON.
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("cache JSON unmarshal: %v", err)
	}
	expectedKeys := map[string]bool{
		"checked_at":    true,
		"expires_at":    true,
		"never_expires": true,
		"name":          true,
		"policies":      true,
		"stamp":         true,
	}
	for k := range m {
		if !expectedKeys[k] {
			t.Errorf("unexpected key in cache: %q (allowed: checked_at, expires_at, never_expires, name, policies, stamp)", k)
		}
	}
	for k := range expectedKeys {
		if _, ok := m[k]; !ok {
			t.Errorf("missing expected key in cache: %q", k)
		}
	}
}

func TestLoadCacheMissingOrCorrupt(t *testing.T) {
	dir := t.TempDir()

	if _, ok := LoadCache(filepath.Join(dir, "absent.json")); ok {
		t.Error("ok = true for a missing file")
	}

	corrupt := filepath.Join(dir, "corrupt.json")
	os.WriteFile(corrupt, []byte("{not json"), 0o600)
	if _, ok := LoadCache(corrupt); ok {
		t.Error("ok = true for a corrupt file")
	}
}

func TestCacheFresh(t *testing.T) {
	now := time.Unix(10_000, 0)
	const recheck = 300 * time.Second
	stamp := TokenStamp{ModTime: 500, Size: 95}

	tests := []struct {
		name  string
		cache Cache
		want  bool
	}{
		{"checked recently, not expired",
			Cache{CheckedAt: 9_900, ExpiresAt: 20_000, Token: stamp}, true},
		{"checked too long ago",
			Cache{CheckedAt: 9_000, ExpiresAt: 20_000, Token: stamp}, false},
		{"exactly at the recheck boundary",
			Cache{CheckedAt: 9_700, ExpiresAt: 20_000, Token: stamp}, false},
		// Recent check but the token has since expired: must re-verify, not
		// serve a stale "signed in".
		{"recent but expired",
			Cache{CheckedAt: 9_900, ExpiresAt: 9_950, Token: stamp}, false},
		{"non-expiring token",
			Cache{CheckedAt: 9_900, ExpiresAt: 0, NeverExpires: true, Token: stamp}, true},
		// The user logged in again from a terminal: same time window, different
		// token. Serving the old expiry here is the bug this field prevents.
		{"token file changed underneath us",
			Cache{CheckedAt: 9_900, ExpiresAt: 20_000, Token: TokenStamp{ModTime: 400, Size: 95}}, false},
		{"token file changed size only",
			Cache{CheckedAt: 9_900, ExpiresAt: 20_000, Token: TokenStamp{ModTime: 500, Size: 12}}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cache.Fresh(now, recheck, stamp); got != tc.want {
				t.Errorf("Fresh() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestStampToken(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".vault-token")
	os.WriteFile(p, []byte("hvs.abc"), 0o600)

	first := StampToken(p)
	if first.Size != 7 || first.ModTime == 0 {
		t.Fatalf("StampToken = %+v, want size 7 and a real mtime", first)
	}
	if same := StampToken(p); same != first {
		t.Errorf("StampToken is not stable: %+v then %+v", first, same)
	}

	// A rewrite with different content must change the stamp.
	os.WriteFile(p, []byte("hvs.a-much-longer-token"), 0o600)
	if changed := StampToken(p); changed == first {
		t.Error("StampToken did not change after the token was rewritten")
	}

	// A missing file stamps as zero, which never equals a real stamp.
	if missing := StampToken(filepath.Join(t.TempDir(), "absent")); missing != (TokenStamp{}) {
		t.Errorf("StampToken(missing) = %+v, want zero", missing)
	}
}

func TestDeleteCacheIsIdempotent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "status.json")
	if err := DeleteCache(p); err != nil {
		t.Fatalf("DeleteCache on a missing file: %v", err)
	}
}

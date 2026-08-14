package bao

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// TokenStamp identifies which token a cache entry describes, using only file
// metadata — never the token itself.
type TokenStamp struct {
	ModTime int64 `json:"mod_time"`
	Size    int64 `json:"size"`
}

// StampToken returns the stamp for a token file, or the zero stamp if it cannot
// be read. The zero value never compares equal to a real stamp, so an unreadable
// token file always invalidates the cache.
func StampToken(path string) TokenStamp {
	fi, err := os.Stat(path)
	if err != nil {
		return TokenStamp{}
	}
	return TokenStamp{ModTime: fi.ModTime().UnixNano(), Size: fi.Size()}
}

// Cache is what lets the countdown run locally between server checks. It holds
// no token material — only an expiry, a display name, policy names, and the
// token file's mtime and size — so deleting it is always safe and merely forces
// a fresh lookup.
type Cache struct {
	CheckedAt    int64      `json:"checked_at"`
	ExpiresAt    int64      `json:"expires_at"`
	NeverExpires bool       `json:"never_expires"`
	Name         string     `json:"name"`
	Policies     []string   `json:"policies"`
	Token        TokenStamp `json:"stamp"`
}

// LoadCache reports ok=false for a missing or unreadable cache. A corrupt cache
// is not an error worth surfacing: the caller just re-checks the server.
func LoadCache(path string) (Cache, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Cache{}, false
	}
	var c Cache
	if err := json.Unmarshal(b, &c); err != nil {
		return Cache{}, false
	}
	return c, true
}

func SaveCache(path string, c Cache) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

func DeleteCache(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// Fresh reports whether the cache can be trusted without calling the server.
// stamp is the token file's current stamp: if it differs from the one recorded,
// the user logged in again and this entry describes a token that is gone.
func (c Cache) Fresh(now time.Time, recheck time.Duration, stamp TokenStamp) bool {
	if c.Token != stamp {
		return false
	}
	if now.Unix()-c.CheckedAt >= int64(recheck.Seconds()) {
		return false
	}
	if c.NeverExpires {
		return true
	}
	return c.ExpiresAt > now.Unix()
}

package bao

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ErrForbidden means the server rejected the token: it is revoked or expired.
// It is deliberately distinct from a transport error — the poller treats the
// former as signed out and the latter as degraded.
var ErrForbidden = errors.New("token rejected by server")

// Info is one successful lookup-self.
type Info struct {
	ExpiresAt    time.Time
	Name         string
	Policies     []string
	NeverExpires bool
}

type Client struct {
	Addr string
	HTTP *http.Client
}

func NewClient(addr string) *Client {
	return &Client{
		Addr: strings.TrimSuffix(addr, "/"),
		// CheckRedirect: OpenBao never legitimately redirects lookup-self or
		// revoke-self. Without this, a 307 from the configured address would
		// make net/http resend the request — including the X-Vault-Token
		// header, which Go strips only Authorization and Cookie on a
		// cross-host redirect, not custom headers — to whatever host the
		// redirect names. http.ErrUseLastResponse makes the client return the
		// redirect response itself instead of following it.
		HTTP: &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		}},
	}
}

func (c *Client) LookupSelf(ctx context.Context, token string, now time.Time) (Info, error) {
	resp, err := c.do(ctx, http.MethodGet, "/v1/auth/token/lookup-self", token)
	if err != nil {
		return Info{}, err
	}
	defer resp.Body.Close()

	// Cap the response body: a tray app polling forever against an uncontrolled server
	// should not decode unbounded payloads.
	limBody := io.LimitReader(resp.Body, 1<<20)

	var body struct {
		Data struct {
			ExpireTime  string   `json:"expire_time"`
			TTL         int64    `json:"ttl"`
			DisplayName string   `json:"display_name"`
			Policies    []string `json:"policies"`
			// Policies granted through an identity group rather than attached
			// to the token. An OIDC or JWT login puts everything here and
			// leaves "policies" as just ["default"], so reading only the
			// latter reports "none" for a user who has several.
			IdentityPolicies []string `json:"identity_policies"`
		} `json:"data"`
	}
	if err := json.NewDecoder(limBody).Decode(&body); err != nil {
		return Info{}, fmt.Errorf("decode lookup-self: %w", err)
	}

	info := Info{Name: body.Data.DisplayName}
	// Token policies first, then identity ones, deduplicated: a policy can
	// legitimately appear in both, and listing it twice in the menu is noise.
	// "default" is dropped because every token has it.
	seen := make(map[string]bool, len(body.Data.Policies)+len(body.Data.IdentityPolicies))
	for _, list := range [][]string{body.Data.Policies, body.Data.IdentityPolicies} {
		for _, p := range list {
			if p == "default" || seen[p] {
				continue
			}
			seen[p] = true
			info.Policies = append(info.Policies, p)
		}
	}

	switch {
	case body.Data.ExpireTime != "":
		t, err := time.Parse(time.RFC3339Nano, body.Data.ExpireTime)
		if err != nil {
			return Info{}, fmt.Errorf("parse expire_time %q: %w", body.Data.ExpireTime, err)
		}
		info.ExpiresAt = t.UTC()
	case body.Data.TTL > 0:
		// Guard against overflow: if TTL is very large (>100 years), treat as non-expiring.
		if body.Data.TTL > 3_153_600_000 {
			info.NeverExpires = true
			info.ExpiresAt = now.UTC()
		} else {
			info.ExpiresAt = now.Add(time.Duration(body.Data.TTL) * time.Second).UTC()
		}
	case body.Data.TTL < 0:
		// Negative TTL is malformed; safe reading is "already expired".
		info.ExpiresAt = now.UTC()
	default:
		// No expire_time and no ttl (TTL == 0): a root or otherwise non-expiring token.
		info.NeverExpires = true
	}
	return info, nil
}

func (c *Client) RevokeSelf(ctx context.Context, token string) error {
	resp, err := c.do(ctx, http.MethodPost, "/v1/auth/token/revoke-self", token)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func (c *Client) do(ctx context.Context, method, path, token string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.Addr+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Vault-Token", token)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err // transport failure: caller renders this as Degraded
	}
	switch resp.StatusCode {
	case http.StatusOK, http.StatusNoContent:
		return resp, nil
	case http.StatusForbidden, http.StatusUnauthorized:
		resp.Body.Close()
		return nil, ErrForbidden
	default:
		resp.Body.Close()
		return nil, fmt.Errorf("%s %s: unexpected status %d", method, path, resp.StatusCode)
	}
}

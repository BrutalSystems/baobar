package bao

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
		HTTP: &http.Client{Timeout: 5 * time.Second},
	}
}

func (c *Client) LookupSelf(ctx context.Context, token string, now time.Time) (Info, error) {
	resp, err := c.do(ctx, http.MethodGet, "/v1/auth/token/lookup-self", token)
	if err != nil {
		return Info{}, err
	}
	defer resp.Body.Close()

	var body struct {
		Data struct {
			ExpireTime  string   `json:"expire_time"`
			TTL         int64    `json:"ttl"`
			DisplayName string   `json:"display_name"`
			Policies    []string `json:"policies"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return Info{}, fmt.Errorf("decode lookup-self: %w", err)
	}

	info := Info{Name: body.Data.DisplayName}
	for _, p := range body.Data.Policies {
		if p != "default" {
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
		info.ExpiresAt = now.Add(time.Duration(body.Data.TTL) * time.Second).UTC()
	default:
		// No expire_time and no ttl: a root or otherwise non-expiring token.
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

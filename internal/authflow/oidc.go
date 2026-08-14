package authflow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// OIDCConfig describes one SSO login attempt.
type OIDCConfig struct {
	Addr  string // OpenBao base URL, no trailing slash
	Mount string // auth mount path, e.g. "oidc"
	Role  string // optional; omitted from the request when empty
	// CallbackPort must match the role's allowed redirect URI. It is not
	// randomised: a different port would fail the provider's redirect check
	// later, far from the cause.
	CallbackPort int
	HTTP         *http.Client
	OpenURL      func(string) error
	Timeout      time.Duration
}

func (c OIDCConfig) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 15 * time.Second}
}

// OIDC drives the browser through an OpenBao OIDC login and returns the token.
func OIDC(ctx context.Context, cfg OIDCConfig) (string, error) {
	if !acquire() {
		return "", ErrBusy
	}
	defer release()

	if cfg.Timeout == 0 {
		cfg.Timeout = DefaultTimeout
	}

	clientNonce, err := nonce()
	if err != nil {
		return "", err
	}

	s, err := newSession(cfg.CallbackPort, cfg.Timeout)
	if err != nil {
		return "", fmt.Errorf("cannot listen for the OIDC redirect on port %d "+
			"(it must match the role's allowed redirect URI): %w", cfg.CallbackPort, err)
	}

	redirectURI := s.baseURL() + "/oidc/callback"

	authURL, err := cfg.authURL(ctx, redirectURI, clientNonce)
	if err != nil {
		s.finish("", err)
		s.serve(ctx)
		return "", err
	}

	s.handle("/oidc/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if e := q.Get("error"); e != "" {
			desc := q.Get("error_description")
			writePage(w, "Login failed", "You can close this tab.")
			s.finish("", fmt.Errorf("identity provider returned %s: %s", e, desc))
			return
		}
		token, err := cfg.exchange(ctx, q.Get("state"), q.Get("code"), clientNonce)
		if err != nil {
			writePage(w, "Login failed", "You can close this tab.")
			s.finish("", err)
			return
		}
		writePage(w, "Signed in", "You can close this tab and return to Baobar.")
		s.finish(token, nil)
	})

	if err := cfg.OpenURL(authURL); err != nil {
		s.finish("", fmt.Errorf("open browser: %w", err))
	}

	return s.serve(ctx)
}

func (c OIDCConfig) authURL(ctx context.Context, redirectURI, clientNonce string) (string, error) {
	payload := map[string]string{
		"redirect_uri": redirectURI,
		"client_nonce": clientNonce,
	}
	if c.Role != "" {
		payload["role"] = c.Role
	}
	b, _ := json.Marshal(payload)

	endpoint := fmt.Sprintf("%s/v1/auth/%s/oidc/auth_url", c.Addr, c.Mount)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(b))
	if err != nil {
		return "", sanitize(err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client().Do(req)
	if err != nil {
		return "", fmt.Errorf("request auth URL: %w", sanitize(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("request auth URL: unexpected status %d", resp.StatusCode)
	}

	var body struct {
		Data struct {
			AuthURL string `json:"auth_url"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("decode auth URL: %w", err)
	}
	if body.Data.AuthURL == "" {
		return "", fmt.Errorf("OpenBao returned no auth URL (is the %q mount configured?)", c.Mount)
	}
	return body.Data.AuthURL, nil
}

func (c OIDCConfig) exchange(ctx context.Context, state, code, clientNonce string) (string, error) {
	q := url.Values{}
	q.Set("state", state)
	q.Set("code", code)
	q.Set("client_nonce", clientNonce)

	endpoint := fmt.Sprintf("%s/v1/auth/%s/oidc/callback?%s", c.Addr, c.Mount, q.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", sanitize(err)
	}

	resp, err := c.client().Do(req)
	if err != nil {
		return "", fmt.Errorf("exchange code: %w", sanitize(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("exchange code: unexpected status %d", resp.StatusCode)
	}

	var body struct {
		Auth struct {
			ClientToken string `json:"client_token"`
		} `json:"auth"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("decode token: %w", err)
	}
	if body.Auth.ClientToken == "" {
		return "", fmt.Errorf("OpenBao returned no token")
	}
	return body.Auth.ClientToken, nil
}

package authflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

var (
	// ErrBadCredentials means the username or password was rejected.
	ErrBadCredentials = errors.New("invalid username or password")
	// ErrBadPasscode means the TOTP passcode was rejected.
	ErrBadPasscode = errors.New("invalid passcode")
)

// UserpassConfig describes one username/password login attempt.
type UserpassConfig struct {
	Addr  string
	Mount string
	HTTP  *http.Client
}

func (c UserpassConfig) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 15 * time.Second}
}

// mfaChallenge is phase one's answer when MFA is enforced. MethodKey is the
// method's UUID when the server supplies one, otherwise its name — the API
// accepts either as the mfa_payload key.
type mfaChallenge struct {
	RequestID string
	MethodKey string
}

// authResponse covers both a completed login and an MFA-pending one.
type authResponse struct {
	Auth struct {
		ClientToken    string `json:"client_token"`
		MFARequirement *struct {
			MFARequestID   string `json:"mfa_request_id"`
			MFAConstraints map[string]struct {
				Any []struct {
					Type         string `json:"type"`
					ID           string `json:"id"`
					Name         string `json:"name"`
					UsesPasscode bool   `json:"uses_passcode"`
				} `json:"any"`
			} `json:"mfa_constraints"`
		} `json:"mfa_requirement"`
	} `json:"auth"`
}

// login performs phase one. It returns either a token (no MFA configured) or a
// challenge (MFA required) — never both.
func (c UserpassConfig) login(ctx context.Context, username, password string) (string, *mfaChallenge, error) {
	// The username becomes a path segment. Escaping alone would leave us
	// depending on OpenBao's router not re-splitting a decoded %2F, so
	// anything that could change the path is refused outright — the same
	// posture config.Load takes with mount names.
	if username == "" || strings.ContainsAny(username, "/\\?#") || strings.ContainsFunc(username, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return "", nil, fmt.Errorf("%w: username contains characters that are not allowed", ErrBadCredentials)
	}

	body, _ := json.Marshal(map[string]string{"password": password})
	endpoint := fmt.Sprintf("%s/v1/auth/%s/login/%s", c.Addr, c.Mount, url.PathEscape(username))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", nil, sanitize(err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client().Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("login request: %w", sanitize(err))
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden:
		return "", nil, ErrBadCredentials
	default:
		return "", nil, fmt.Errorf("login: unexpected status %d", resp.StatusCode)
	}

	var ar authResponse
	if err := json.NewDecoder(resp.Body).Decode(&ar); err != nil {
		return "", nil, fmt.Errorf("decode login response: %w", err)
	}

	if ar.Auth.ClientToken != "" {
		return ar.Auth.ClientToken, nil, nil
	}
	if ar.Auth.MFARequirement == nil {
		return "", nil, errors.New("login returned neither a token nor an MFA requirement")
	}

	// Iterate in sorted order: MFAConstraints is a map, and Go randomises map
	// iteration, so with more than one enforcement configured an unsorted loop
	// would pick a different method on different runs.
	names := make([]string, 0, len(ar.Auth.MFARequirement.MFAConstraints))
	for name := range ar.Auth.MFARequirement.MFAConstraints {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		constraint := ar.Auth.MFARequirement.MFAConstraints[name]
		for _, m := range constraint.Any {
			if !m.UsesPasscode {
				continue
			}
			key := m.ID
			if key == "" {
				key = m.Name
			}
			if key == "" {
				continue
			}
			return "", &mfaChallenge{
				RequestID: ar.Auth.MFARequirement.MFARequestID,
				MethodKey: key,
			}, nil
		}
	}
	return "", nil, errors.New("MFA is required but no passcode method was offered")
}

// validate performs phase two, exchanging the passcode for a token.
func (c UserpassConfig) validate(ctx context.Context, ch *mfaChallenge, passcode string) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"mfa_request_id": ch.RequestID,
		"mfa_payload":    map[string][]string{ch.MethodKey: {passcode}},
	})

	endpoint := c.Addr + "/v1/sys/mfa/validate"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", sanitize(err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client().Do(req)
	if err != nil {
		return "", fmt.Errorf("validate request: %w", sanitize(err))
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden:
		return "", ErrBadPasscode
	default:
		return "", fmt.Errorf("validate: unexpected status %d", resp.StatusCode)
	}

	var ar authResponse
	if err := json.NewDecoder(resp.Body).Decode(&ar); err != nil {
		return "", fmt.Errorf("decode validate response: %w", err)
	}
	if ar.Auth.ClientToken == "" {
		return "", errors.New("MFA validation returned no token")
	}
	return ar.Auth.ClientToken, nil
}

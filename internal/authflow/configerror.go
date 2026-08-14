package authflow

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// ConfigProblem is a failure the user can actually fix by changing
// configuration — a rejected redirect URI, an unknown role, a callback port
// already in use. Its Detail is safe to show in a notification.
//
// It exists because the most likely first-run failure is a redirect URI the
// OIDC role does not permit. OpenBao says so precisely; before this type the
// app reported only "Login did not complete", which tells the user nothing they
// can act on.
//
// Detail carries ONLY strings the server put in its `errors` array, or text we
// wrote ourselves. It never carries a raw response body, a URL, or anything
// from a credential-bearing request — those stay generic, because a body we do
// not control is not something to render into a notification.
type ConfigProblem struct {
	Detail string
	Status int
}

func (e *ConfigProblem) Error() string {
	if e.Detail == "" {
		return "server rejected the request"
	}
	return e.Detail
}

// maxDetail bounds what we will repeat back from a server response.
const maxDetail = 300

// configProblemFrom builds a ConfigProblem from a non-2xx response, using
// OpenBao's `errors` array when present. The body is read through a small
// LimitReader and never surfaced verbatim.
func configProblemFrom(resp *http.Response) *ConfigProblem {
	p := &ConfigProblem{Status: resp.StatusCode}

	var body struct {
		Errors []string `json:"errors"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&body); err != nil {
		return p
	}

	msgs := make([]string, 0, len(body.Errors))
	for _, m := range body.Errors {
		if m = strings.TrimSpace(m); m != "" {
			msgs = append(msgs, m)
		}
	}
	p.Detail = strings.Join(msgs, "; ")
	if len(p.Detail) > maxDetail {
		p.Detail = p.Detail[:maxDetail] + "…"
	}
	return p
}

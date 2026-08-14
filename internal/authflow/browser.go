package authflow

import (
	"context"
	_ "embed"
	"errors"
	"html/template"
	"net/http"
	"time"
)

//go:embed login.html
var loginHTML string

var loginTmpl = template.Must(template.New("login").Parse(loginHTML))

// UserpassBrowserConfig describes one form-based login attempt.
type UserpassBrowserConfig struct {
	Userpass        UserpassConfig
	DefaultUsername string
	OpenURL         func(string) error
	Timeout         time.Duration
}

type formData struct {
	Username string
	Error    string
}

// UserpassBrowser serves a login form on loopback, opens the browser at it, and
// returns the token once the user submits valid credentials. The password and
// passcode live only in the request they arrive on.
func UserpassBrowser(ctx context.Context, cfg UserpassBrowserConfig) (string, error) {
	if !acquire() {
		return "", ErrBusy
	}
	defer release()

	if cfg.Timeout == 0 {
		cfg.Timeout = DefaultTimeout
	}

	n, err := nonce()
	if err != nil {
		return "", err
	}

	s, err := newSession(0, cfg.Timeout)
	if err != nil {
		return "", err
	}

	path := "/login/" + n
	s.handle(path, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			render(w, formData{Username: cfg.DefaultUsername})
			return
		}

		// A browser sends Sec-Fetch-Site on every request it originates. Anything
		// other than same-origin (or none, for a direct navigation) means another
		// page drove this submission, which for a password form is never legitimate.
		// The session's Host guard cannot catch this: a cross-origin POST still
		// carries our own Host.
		if s := r.Header.Get("Sec-Fetch-Site"); s != "" && s != "same-origin" && s != "none" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}

		if err := r.ParseForm(); err != nil {
			render(w, formData{Username: cfg.DefaultUsername, Error: "Could not read the form."})
			return
		}
		username := r.PostFormValue("username")
		password := r.PostFormValue("password")
		passcode := r.PostFormValue("passcode")

		token, ch, err := cfg.Userpass.login(ctx, username, password)
		switch {
		case errors.Is(err, ErrBadCredentials):
			render(w, formData{Username: username, Error: "Invalid username or password."})
			return
		case err != nil:
			render(w, formData{Username: username, Error: "Could not reach OpenBao."})
			return
		}

		if ch != nil {
			token, err = cfg.Userpass.validate(ctx, ch, passcode)
			switch {
			case errors.Is(err, ErrBadPasscode):
				render(w, formData{Username: username, Error: "That passcode was not accepted."})
				return
			case err != nil:
				render(w, formData{Username: username, Error: "Could not validate the passcode."})
				return
			}
		}

		writePage(w, "Signed in", "You can close this tab and return to Baobar.")
		s.finish(token, nil)
	})

	if err := cfg.OpenURL(s.baseURL() + path); err != nil {
		s.finish("", err)
	}

	return s.serve()
}

func render(w http.ResponseWriter, d formData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = loginTmpl.Execute(w, d)
}

package authflow

import (
	"context"
	_ "embed"
	"html/template"
	"net/http"
	"time"
)

//go:embed setup.html
var setupHTML string

var setupTmpl = template.Must(template.New("setup").Parse(setupHTML))

// SetupConfig describes a first-run address prompt.
//
// Save is injected rather than calling internal/config directly: authflow does
// not import config, and keeping that boundary means this package stays
// testable without touching the filesystem.
type SetupConfig struct {
	OpenURL func(string) error
	Save    func(addr string) error
	Timeout time.Duration
}

type setupData struct {
	Addr  string
	Error string
}

// Setup serves a one-field form asking for the OpenBao address, opens the
// browser at it, and returns the address once Save accepts it.
//
// It exists because the alternative first run is "hand-write TOML in a
// terminal", which contradicts the one thing Baobar promises. A rejected
// address redisplays the form with the reason rather than ending the flow: a
// typo should not drop the user back to a tray icon with no way forward.
func Setup(ctx context.Context, cfg SetupConfig) (string, error) {
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

	path := "/setup/" + n
	s.handle(path, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			renderSetup(w, setupData{})
			return
		}

		// Same reasoning as the login form: a browser sends Sec-Fetch-Site on
		// every request it originates, and anything other than same-origin means
		// another page drove this submission. The Host guard cannot catch that.
		if v := r.Header.Get("Sec-Fetch-Site"); v != "" && v != "same-origin" && v != "none" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}

		if err := r.ParseForm(); err != nil {
			renderSetup(w, setupData{Error: "Could not read the form."})
			return
		}
		addr := r.PostFormValue("addr")

		if err := cfg.Save(addr); err != nil {
			renderSetup(w, setupData{Addr: addr, Error: err.Error()})
			return
		}

		writePage(w, "Baobar is set up", "You can close this tab. Baobar is starting.")
		s.finish(addr, nil)
	})

	if err := cfg.OpenURL(s.baseURL() + path); err != nil {
		s.close()
		return "", err
	}
	return s.serve(ctx)
}

func renderSetup(w http.ResponseWriter, d setupData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_ = setupTmpl.Execute(w, d)
}

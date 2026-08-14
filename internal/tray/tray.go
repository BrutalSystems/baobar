package tray

import (
	"context"
	"sync"
	"time"

	"fyne.io/systray"

	"github.com/brutalsystems/baobar/internal/bao"
)

type Options struct {
	Addr string

	// ConfigError, when non-empty, puts the tray into a permanent error state:
	// no polling, no login, just the message and Quit. A tray app launched from
	// Finder or Explorer has no terminal, so exiting with a log line would be
	// invisible — and "no VAULT_ADDR yet" is the most likely first-run state.
	ConfigError string

	// CLIAvailable reports whether `bao` is on PATH. M1's login shells out to
	// it; when it is missing the login items say so instead of opening a
	// console that fails.
	CLIAvailable bool

	// Status may block on the network. It is called ONLY from the poll
	// goroutine — never from the goroutine servicing menu clicks, or the menu
	// freezes for the duration of every server check.
	Status     func(context.Context) bao.Status
	Logout     func() error
	Login      func(method string) error
	Refresh    func()
	Thresholds func(tokenKey int64, remaining time.Duration) []time.Duration
	Notify     func(threshold time.Duration)
	OpenURL    func(string) error

	// Alert surfaces a failure the menu would otherwise swallow silently —
	// e.g. a logout whose revoke call never reached the server, or a login
	// terminal that failed to launch. title/message are short and never
	// contain a token value.
	Alert func(title, message string)

	// PollEvery defaults to one second. This is only how often the poller is
	// asked; the poller itself decides when that turns into a request.
	PollEvery time.Duration
}

// alert calls o.Alert if the caller set one; a nil Alert is silently a no-op
// rather than a panic, so callers that don't care about surfacing failures
// (tests, future embedders) don't have to supply a stub.
func (o Options) alert(title, message string) {
	if o.Alert != nil {
		o.Alert(title, message)
	}
}

// holder passes the latest Status from the poll goroutine to the UI goroutine.
type holder struct {
	mu sync.RWMutex
	s  bao.Status
}

func (h *holder) set(s bao.Status) { h.mu.Lock(); h.s = s; h.mu.Unlock() }
func (h *holder) get() bao.Status  { h.mu.RLock(); defer h.mu.RUnlock(); return h.s }

// Run blocks until the user quits. Must be called from main.
func Run(o Options) {
	systray.Run(func() { onReady(o) }, func() {})
}

func onReady(o Options) {
	if o.PollEvery == 0 {
		o.PollEvery = time.Second
	}
	if o.ConfigError != "" {
		onReadyError(o)
		return
	}
	onReadyNormal(o)
}

// onReadyError is the misconfigured state: visible, explicable, and quittable.
func onReadyError(o Options) {
	systray.SetTitle("⚠️ baobar")
	systray.SetTooltip("Baobar: " + o.ConfigError)
	// A dedicated triangle icon, not the signed-out circle: on Windows,
	// where SetTitle renders no text, reusing the signed-out icon here would
	// make a misconfigured Baobar indistinguishable from one that is merely
	// signed out.
	systray.SetIcon(IconConfigError())

	mMsg := systray.AddMenuItem(o.ConfigError, "Baobar cannot start until this is fixed")
	mMsg.Disable()
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Quit", "Quit Baobar")

	go func() {
		<-mQuit.ClickedCh
		systray.Quit()
	}()
}

func onReadyNormal(o Options) {
	h := &holder{}
	refresh := make(chan struct{}, 1)

	systray.SetIcon(Icon(bao.StateSignedOut))
	systray.SetTitle("🔒 login")
	systray.SetTooltip("OpenBao")

	mAddr := systray.AddMenuItem(o.Addr, "Open the OpenBao web UI")
	systray.AddSeparator()
	mWho := systray.AddMenuItem("Not signed in", "")
	mPolicies := systray.AddMenuItem("", "")
	mExpires := systray.AddMenuItem("", "")
	mWho.Disable()
	mPolicies.Disable()
	mExpires.Disable()
	systray.AddSeparator()
	mLogout := systray.AddMenuItem("Log out (revoke token)", "Revoke this token on the server")
	systray.AddSeparator()
	mUserpass := systray.AddMenuItem("Login with password + TOTP", "Opens a terminal")
	mOIDC := systray.AddMenuItem("Login with SSO", "Opens a terminal")
	systray.AddSeparator()
	mRefresh := systray.AddMenuItem("Refresh now", "Check the server immediately")
	mQuit := systray.AddMenuItem("Quit", "Quit Baobar")

	// Without the CLI, M1 cannot log in at all. Say so once, here, rather than
	// letting the buttons open a console that prints "bao: command not found".
	if !o.CLIAvailable {
		for _, m := range []*systray.MenuItem{mUserpass, mOIDC} {
			m.Disable()
		}
		mUserpass.SetTitle("Login needs the bao CLI (not installed)")
		mOIDC.SetTitle("Use the web UI above to sign in")
	}

	// The poll goroutine is the only caller of o.Status, so a slow or hanging
	// server cannot block menu clicks. It is also the only caller of
	// o.Refresh (which forces the poller's next Status call past its
	// throttle): Poller.Force documents that it must only ever be invoked
	// from the same goroutine that calls Status, so every other goroutine
	// that wants a forced recheck — a menu click, or a logout/login attempt
	// finishing in its own goroutine below — asks for one only by writing to
	// refresh, never by calling o.Refresh directly.
	go func() {
		ctx := context.Background()
		t := time.NewTicker(o.PollEvery)
		defer t.Stop()
		for {
			h.set(o.Status(ctx))
			select {
			case <-t.C:
			case <-refresh:
				o.Refresh()
			}
		}
	}()

	go uiLoop(o, h, refresh, menuItems{
		addr: mAddr, who: mWho, policies: mPolicies, expires: mExpires,
		logout: mLogout, userpass: mUserpass, oidc: mOIDC,
		refresh: mRefresh, quit: mQuit,
	})
}

type menuItems struct {
	addr, who, policies, expires *systray.MenuItem
	logout, userpass, oidc       *systray.MenuItem
	refresh, quit                *systray.MenuItem
}

func uiLoop(o Options, h *holder, refreshCh chan struct{}, m menuItems) {
	t := time.NewTicker(time.Second)
	defer t.Stop()

	var (
		lastLabel, lastTooltip             string
		lastWho, lastPolicies, lastExpires string
		lastState                          = bao.State(-1)
	)

	render := func() {
		s := h.get()

		// Recompute the countdown from the absolute expiry so it keeps ticking
		// even while the poll goroutine is blocked on a slow server. The state
		// itself can lag by one poll; the number never does.
		if !s.NeverExpires && !s.ExpiresAt.IsZero() {
			if r := time.Until(s.ExpiresAt); r > 0 {
				s.Remaining = r
			} else {
				s.Remaining = 0
			}
		}

		// Write to the tray only on change: this runs once a second, and
		// rewriting the icon every tick flickers on some Linux indicators.
		if l := Label(s); l != lastLabel {
			systray.SetTitle(l) // renders no text on Windows; see Tooltip
			lastLabel = l
		}
		if tt := Tooltip(s); tt != lastTooltip {
			systray.SetTooltip(tt)
			lastTooltip = tt
		}
		if s.State != lastState {
			systray.SetIcon(Icon(s.State))
		}

		who, policies, expires := MenuLines(s)
		if who != lastWho {
			m.who.SetTitle(who)
			lastWho = who
		}
		if policies != lastPolicies {
			m.policies.SetTitle(policies)
			lastPolicies = policies
		}
		if expires != lastExpires {
			m.expires.SetTitle(expires)
			lastExpires = expires
		}

		if s.State != lastState {
			if policies == "" {
				m.policies.Hide()
			} else {
				m.policies.Show()
			}
			if expires == "" {
				m.expires.Hide()
			} else {
				m.expires.Show()
			}
			if s.State == bao.StateSignedOut {
				m.logout.Hide()
			} else {
				m.logout.Show()
			}
			lastState = s.State
		}

		// One notification per tick: when the app wakes from sleep having
		// crossed both thresholds at once, the most urgent one is the only
		// useful message.
		if !s.NeverExpires && s.Remaining > 0 && !s.ExpiresAt.IsZero() {
			if crossed := o.Thresholds(s.ExpiresAt.Unix(), s.Remaining); len(crossed) > 0 {
				o.Notify(crossed[len(crossed)-1])
			}
		}
	}

	kick := func() {
		select {
		case refreshCh <- struct{}{}:
		default: // a poll is already pending
		}
	}

	render()
	for {
		select {
		case <-t.C:
			render()
		case <-m.addr.ClickedCh:
			_ = o.OpenURL(o.Addr + "/ui")
		case <-m.logout.ClickedCh:
			// Logout does network I/O (RevokeSelf, up to its own timeout).
			// Run it off the click-handling goroutine so the menu never
			// freezes, and signal the poll goroutine when it's done rather
			// than kicking it immediately (the token file may not be gone
			// yet at click time).
			go func() {
				if err := o.Logout(); err != nil {
					o.alert("Logout incomplete",
						"The local session was cleared, but the token could not be "+
							"revoked and remains valid on the server until it expires.")
				}
				kick()
			}()
		case <-m.userpass.ClickedCh:
			go func() {
				if err := o.Login("userpass"); err != nil {
					o.alert("Could not start login",
						"The terminal could not be started. Use the web UI link above to sign in instead.")
				}
				kick()
			}()
		case <-m.oidc.ClickedCh:
			go func() {
				if err := o.Login("oidc"); err != nil {
					o.alert("Could not start login",
						"The terminal could not be started. Use the web UI link above to sign in instead.")
				}
				kick()
			}()
		case <-m.refresh.ClickedCh:
			kick()
		case <-m.quit.ClickedCh:
			systray.Quit()
			return
		}
	}
}

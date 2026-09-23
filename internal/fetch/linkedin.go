package fetch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	// LinkedInCookieEnv names the environment variable that holds the user's own
	// already-authenticated LinkedIn session cookie. This is the same
	// configuration mechanism the project already uses for the evidence store
	// and the registry, so it adds no second way to configure anything.
	LinkedInCookieEnv = "WORDS_ON_THE_STREET_LINKEDIN_COOKIE"
	// LinkedInMinIntervalEnv overrides the conservative default minimum delay
	// between LinkedIn requests. Raising it is a disclosure, not a gate.
	LinkedInMinIntervalEnv = "WORDS_ON_THE_STREET_LINKEDIN_MIN_INTERVAL"
)

// defaultLinkedInMinInterval is deliberately conservative: LinkedIn treats rapid
// automated access as abuse. The user can raise it with
// LinkedInMinIntervalEnv, with the risk stated once in docs/LINKEDIN.md rather
// than enforced by a runtime prompt.
const defaultLinkedInMinInterval = 15 * time.Second

// ErrLinkedInCookieMissing reports that a LinkedIn fetch was attempted without a
// user-supplied session cookie.
var ErrLinkedInCookieMissing = errors.New("linkedin session cookie is not configured")

// LinkedInBanRiskDisclosure is shown as plain text at the CLI at the point the
// user configures a LinkedIn session cookie. It names LinkedIn's response to
// automated access explicitly rather than as a generic warning.
const LinkedInBanRiskDisclosure = `LinkedIn session cookie - read this before you configure one.

Automated access to LinkedIn is against its terms of service, even when you
supply your own session. A session cookie is not a limited key: it grants access
to your account without the password and past two-factor authentication, and
anyone who obtains it can act as you.

LinkedIn's response is severe. It bans accounts permanently and without warning,
and it has litigated against scrapers. Losing the account this cookie belongs to
is a real and likely outcome of automated fetching, not a remote one.

Use a separate, dedicated account that you can afford to lose. Never configure a
cookie for an account you depend on.

Rate limits are conservative by default. You may raise them, but doing so
increases the risk described above; see docs/LINKEDIN.md.

Set the cookie and then fetch:
  export WORDS_ON_THE_STREET_LINKEDIN_COOKIE='li_at=...; JSESSIONID=...'
  words-on-the-street fetch linkedin <query>

The cookie value itself is never printed, written to an evidence record, logged,
used in a synthesis, or passed to an agent.`

// LinkedInCookie returns the configured LinkedIn session cookie, or an error
// that explains how to configure one. The value is returned only so it can be
// passed to the backend as a request header; it is never logged here.
func LinkedInCookie() (string, error) {
	cookie := strings.TrimSpace(os.Getenv(LinkedInCookieEnv))
	if cookie == "" {
		return "", fmt.Errorf("%w: set %s; run \"words-on-the-street config linkedin\" to read the risks first", ErrLinkedInCookieMissing, LinkedInCookieEnv)
	}
	// A newline would let a crafted value inject a second header into the
	// backend's argument. Refuse it rather than pass it through.
	if strings.ContainsAny(cookie, "\r\n") {
		return "", fmt.Errorf("%s must not contain newlines", LinkedInCookieEnv)
	}
	return cookie, nil
}

// intervalLimiter serialises requests so no two are made closer together than
// interval. A caller reserves its slot under the lock and then waits outside it,
// so concurrent callers queue behind each other rather than racing.
type intervalLimiter struct {
	mu   sync.Mutex
	last time.Time
}

func (l *intervalLimiter) wait(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		return nil
	}

	l.mu.Lock()
	wait := interval - time.Since(l.last)
	if wait < 0 {
		wait = 0
	}
	l.last = time.Now().Add(wait)
	l.mu.Unlock()

	if wait == 0 {
		return nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

var linkedInLimiter intervalLimiter

// linkedInMinInterval reads the configured minimum interval, falling back to the
// conservative default when unset or invalid.
func linkedInMinInterval() time.Duration {
	if raw := strings.TrimSpace(os.Getenv(LinkedInMinIntervalEnv)); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d >= 0 {
			return d
		}
	}
	return defaultLinkedInMinInterval
}

// waitForLinkedInRateLimit is the pre-fetch hook attached to LinkedIn
// resolution. It runs before any bytes are retrieved.
func waitForLinkedInRateLimit(ctx context.Context) error {
	return linkedInLimiter.wait(ctx, linkedInMinInterval())
}

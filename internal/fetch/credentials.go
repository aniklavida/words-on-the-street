package fetch

import (
	"os"
	"strconv"
	"strings"

	"github.com/aniklavida/words-on-the-street/internal/evidence"
	"github.com/aniklavida/words-on-the-street/internal/keychain"
)

// The twitter source reaches a platform with the user's own already-authenticated
// session rather than an open endpoint. The session cookie is held in the OS keychain
// where available, with a fallback to WORDS_ON_THE_STREET_TWITTER_COOKIE.
const (
	// TwitterCookieEnv names the variable holding the session cookie fallback when
	// the OS keychain is not available or unconfigured.
	TwitterCookieEnv = "WORDS_ON_THE_STREET_TWITTER_COOKIE"

	// TwitterRateLimitEnv names the variable that raises the per-minute
	// request limit for the twitter source.
	TwitterRateLimitEnv = "WORDS_ON_THE_STREET_TWITTER_RATE_LIMIT"

	// DefaultTwitterRateLimitPerMinute is deliberately conservative. Twitter/X
	// bans permanently and without warning; a low default costs a little
	// latency and buys the account time. The limit is raisable, not gated.
	DefaultTwitterRateLimitPerMinute = 5
)

// TwitterCookie returns the configured session cookie and reports whether one
// is set.
//
// Resolution order:
//  1. OS keychain (words-on-the-street / twitter)
//  2. Environment variable: WORDS_ON_THE_STREET_TWITTER_COOKIE
//
// The value is registered with the evidence redaction set before it is
// returned, so no later boundary can emit it even if a caller mishandles it.
func TwitterCookie() (string, bool) {
	if val, err := keychain.TwitterCookie(); err == nil {
		cookie := strings.TrimSpace(val)
		if cookie != "" {
			if strings.ContainsAny(cookie, "\r\n") {
				return "", false
			}
			evidence.RegisterSecret(cookie)
			return cookie, true
		}
	}

	value := strings.TrimSpace(os.Getenv(TwitterCookieEnv))
	if value == "" {
		return "", false
	}
	if strings.ContainsAny(value, "\r\n") {
		return "", false
	}
	evidence.RegisterSecret(value)
	return value, true
}

// TwitterCredentialArgs returns the backend arguments that present the session
// cookie to the fetch tool. An empty cookie yields nil, so an unconfigured
// twitter fetch sends no credential. The returned slice is passed to the
// backend and then sanitized on the way into the record.
func TwitterCredentialArgs(cookie string) []string {
	if cookie == "" {
		return nil
	}
	evidence.RegisterSecret(cookie)
	return []string{"-H", "Cookie: " + cookie}
}

// TwitterRateLimitPerMinute returns the configured per-minute request limit,
// falling back to the conservative default when unset or unparseable. It
// reads the environment on each call so a caller that changes the setting sees
// the change without a restart; raising the limit is not gated behind a
// confirmation step.
func TwitterRateLimitPerMinute() int {
	raw := strings.TrimSpace(os.Getenv(TwitterRateLimitEnv))
	if raw == "" {
		return DefaultTwitterRateLimitPerMinute
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return DefaultTwitterRateLimitPerMinute
	}
	return n
}

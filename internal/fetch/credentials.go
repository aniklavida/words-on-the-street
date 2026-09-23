package fetch

import (
	"os"
	"strconv"
	"strings"

	"github.com/aniklavida/words-on-the-street/internal/evidence"
)

// The twitter source is the first that reaches a platform with the user's own
// already-authenticated session rather than an open endpoint. Two settings
// configure it, both read from the environment so no credential is written to
// a file this project controls. There is no second config mechanism: the
// registry and the store already take their settings from
// WORDS_ON_THE_STREET_* variables, and these follow the same pattern.
const (
	// TwitterCookieEnv names the variable holding the session cookie the user
	// exported from a browser where they are already logged in. The binary
	// never performs a login; it presents a session the user knowingly
	// supplied.
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
// is set. The value is registered with the evidence redaction set before it is
// returned, so no later boundary can emit it even if a caller mishandles it.
func TwitterCookie() (string, bool) {
	value := strings.TrimSpace(os.Getenv(TwitterCookieEnv))
	if value == "" {
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
